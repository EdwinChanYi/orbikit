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
	"github.com/EdwinChanYi/orbikit/internal/admin/static"
	"github.com/EdwinChanYi/orbikit/pkg/config"
	"github.com/EdwinChanYi/orbikit/pkg/log"
)

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

	password := os.Getenv("ADMIN_PASSWORD")
	if password == "" {
		return errors.New("ADMIN_PASSWORD is required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := auth.OpenMySQL(ctx, cfg.MySQLDSN)
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

	ln, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           routes(store, []byte(password)),
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

// routes sends /api/ to the login API and every other path to the UI.
func routes(store auth.Store, password []byte) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/", auth.NewHandler(store, password, nil))
	mux.Handle("/", static.Handler())
	return mux
}
