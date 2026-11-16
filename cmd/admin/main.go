// Command admin serves the operator console and its login API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/EdwinChanYi/orbikit/internal/admin"
	"github.com/EdwinChanYi/orbikit/internal/admin/auth"
	"github.com/EdwinChanYi/orbikit/internal/admin/nacosapi"
	"github.com/EdwinChanYi/orbikit/internal/admin/releaseapi"
	"github.com/EdwinChanYi/orbikit/internal/admin/static"
	"github.com/EdwinChanYi/orbikit/internal/admin/status"
	"github.com/EdwinChanYi/orbikit/internal/release"
	"github.com/EdwinChanYi/orbikit/pkg/config"
	"github.com/EdwinChanYi/orbikit/pkg/log"
	"github.com/EdwinChanYi/orbikit/pkg/nacos"
)

const defaultAdminPassword = "123456"

func main() {
	cfgPath := flag.String("config", "configs/admin.yaml", "path to the config file")
	flag.Parse()
	if err := run(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "admin:", err)
		os.Exit(1)
	}
}

func run(cfgPath string) error {
	cfg := admin.DefaultConfig()
	if err := config.Load(cfgPath, &cfg); err != nil {
		return err
	}
	if err := log.Init(cfg.Log, "admin"); err != nil {
		return err
	}
	defer log.Sync()

	password := operatorPassword()
	if os.Getenv("ADMIN_PASSWORD") == "" {
		log.L().Info("ADMIN_PASSWORD is unset, using the default password")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if _, err := nacos.Overlay(ctx, cfg.Nacos.Client(), "admin.yaml", &cfg); err != nil {
		log.L().Warn("nacos overlay", zap.Error(err))
	}
	if cfg.Log.Level != "" {
		if err := log.SetLevel(cfg.Log.Level); err != nil {
			log.L().Warn("nacos log level", zap.Error(err))
		}
	}
	local, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}
	current := string(local)
	go nacos.Start(ctx, "admin", cfg.Nacos, local, func(content string) {
		hot, restart, err := nacos.Classify("admin", []byte(current), []byte(content))
		if err != nil {
			log.L().Warn("nacos classify", zap.Error(err))
			return
		}
		for _, change := range hot {
			if change.Path == "log.level" {
				if err := log.SetLevel(change.Value); err != nil {
					log.L().Warn("nacos log level", zap.Error(err))
				}
			}
		}
		if len(restart) > 0 {
			log.L().Info("restart required", zap.Strings("paths", restart))
		}
		current = content
	})

	store, err := auth.OpenMySQL(ctx, cfg.MySQLDSN)
	if err != nil {
		return err
	}
	releases, err := release.OpenMySQL(ctx, cfg.MySQLDSN)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash admin password: %w", err)
	}
	if err := store.EnsureOperator(ctx, hash); err != nil {
		return fmt.Errorf("ensure operator: %w", err)
	}

	snapshots, err := status.Open(
		cfg.Etcd.Endpoints,
		cfg.Etcd.Username,
		cfg.Etcd.Password,
		cfg.Etcd.DialTimeout,
		cfg.Etcd.Prefix,
	)
	if err != nil {
		return fmt.Errorf("etcd: %w", err)
	}
	defer status.Close(snapshots)

	ln, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           routes(store, releases, []byte(password), cfg.BuilderURL, snapshots, cfg.Nacos.Client()),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.L().Info("admin started", zap.String("http_addr", cfg.HTTPAddr))
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			runErr = fmt.Errorf("http serve: %w", err)
		}
	}
	log.L().Info("admin shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && runErr == nil {
		return err
	}
	return runErr
}

// operatorPassword returns ADMIN_PASSWORD, or 123456 when it is unset or empty.
func operatorPassword() string {
	if password := os.Getenv("ADMIN_PASSWORD"); password != "" {
		return password
	}
	return defaultAdminPassword
}

// routes serves overview and cluster behind the session cookie, then the
// login API, then the UI. The status paths are registered before /api/
// so the login mux does not swallow them.
func routes(store auth.Store, releases *release.MySQL, password []byte, builderURL string, etcdSource status.Source, configs *nacos.Client) http.Handler {
	statusHandler := status.NewHandler(status.View{
		BuilderURL: builderURL,
		Health:     status.GRPCHealth{},
		Source:     etcdSource,
		Releases:   releaseReader{store: releases},
	})
	releaseHandler := releaseapi.NewHandler(releases, builderURL, nil)
	mux := http.NewServeMux()
	mux.Handle("/api/overview", auth.RequireSession(store, nil, statusHandler))
	mux.Handle("/api/cluster", auth.RequireSession(store, nil, statusHandler))
	mux.Handle("/api/releases", auth.RequireSession(store, nil, releaseHandler))
	mux.Handle("/api/releases/", auth.RequireSession(store, nil, releaseHandler))
	mux.Handle("/api/config/", auth.RequireSession(store, nil, nacosapi.NewHandler(configs)))
	mux.Handle("/api/", auth.NewHandler(store, password, nil))
	mux.Handle("/", static.Handler())
	return mux
}

type releaseReader struct {
	store release.Store
}

func (r releaseReader) Last(ctx context.Context, service string) (*status.ReleaseStamp, error) {
	task, ok, err := r.store.LatestSuccess(ctx, service)
	if err != nil || !ok {
		return nil, err
	}
	finished := time.Time{}
	if task.FinishedAt != nil {
		finished = *task.FinishedAt
	}
	return &status.ReleaseStamp{Ref: task.GitRef, FinishedAt: finished}, nil
}
