// Command builder reports whitelist container status inside the compose network.
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

	"github.com/EdwinChanYi/orbikit/internal/builder"
	"github.com/EdwinChanYi/orbikit/pkg/config"
	"github.com/EdwinChanYi/orbikit/pkg/log"
	"github.com/EdwinChanYi/orbikit/pkg/nacos"
)

func main() {
	cfgPath := flag.String("config", "configs/builder.yaml", "path to the config file")
	flag.Parse()
	if err := run(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "builder:", err)
		os.Exit(1)
	}
}

func run(cfgPath string) error {
	cfg := builder.DefaultConfig()
	if err := config.Load(cfgPath, &cfg); err != nil {
		return err
	}
	if err := log.Init(cfg.Log, "builder"); err != nil {
		return err
	}
	defer log.Sync()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	local, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}
	if _, err := nacos.Overlay(ctx, cfg.Nacos.Client(), "builder.yaml", &cfg); err != nil {
		log.L().Warn("nacos overlay", zap.Error(err))
	}
	if cfg.Log.Level != "" {
		if err := log.SetLevel(cfg.Log.Level); err != nil {
			log.L().Warn("nacos log level", zap.Error(err))
		}
	}
	current := string(local)
	go nacos.Start(ctx, "builder", cfg.Nacos, local, func(content string) {
		hot, restart, err := nacos.Classify("builder", []byte(current), []byte(content))
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

	ln, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           builder.NewHandler(&builder.Docker{Socket: cfg.Socket}, cfg.Project),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.L().Info("builder started", zap.String("http_addr", cfg.HTTPAddr))
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
	log.L().Info("builder shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && runErr == nil {
		return err
	}
	return runErr
}
