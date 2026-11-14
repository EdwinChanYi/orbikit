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
