// Command core runs the core service: app registry, login tokens and user bans.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/core"
	"github.com/EdwinChanYi/orbikit/internal/core/logic"
	"github.com/EdwinChanYi/orbikit/internal/core/server"
	"github.com/EdwinChanYi/orbikit/internal/core/store"
	"github.com/EdwinChanYi/orbikit/pkg/appnotify"
	"github.com/EdwinChanYi/orbikit/pkg/config"
	"github.com/EdwinChanYi/orbikit/pkg/grpcx"
	"github.com/EdwinChanYi/orbikit/pkg/log"
	"github.com/EdwinChanYi/orbikit/pkg/mysqlx"
	"github.com/EdwinChanYi/orbikit/pkg/redisx"
)

func main() {
	cfgPath := flag.String("config", "configs/core.yaml", "path to the config file")
	flag.Parse()
	if err := run(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "core:", err)
		os.Exit(1)
	}
}

func run(cfgPath string) error {
	cfg := core.DefaultConfig()
	if err := config.Load(cfgPath, &cfg); err != nil {
		return err
	}
	if err := log.Init(cfg.Log, "core"); err != nil {
		return err
	}
	defer log.Sync()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := mysqlx.New(cfg.MySQL)
	if err != nil {
		return err
	}
	if sqlDB, err := db.DB(); err == nil {
		defer sqlDB.Close()
	}
	st := store.New(db)
	if err := st.AutoMigrate(ctx); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}

	rdb, err := redisx.New(ctx, cfg.Redis)
	if err != nil {
		return err
	}
	defer rdb.Close()

	bus := appnotify.New(rdb, cfg.Notify)
	apps := logic.NewAppService(st, cfg.CacheExpire)
	go bus.Run(ctx, func(req *servicepb.AppInfoUpdatedRequest) {
		apps.InvalidateApp(req.GetAppId())
	})
	srv := server.New(st, apps,
		logic.NewTokenService(rdb, apps),
		logic.NewRestrainService(st, rdb),
		bus)

	gs := grpcx.NewServer()
	servicepb.RegisterCoreServer(gs, srv)
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}

	debug := startDebugServer(cfg.DebugAddr)

	serveErr := make(chan error, 1)
	go func() { serveErr <- gs.Serve(lis) }()
	log.L().Info("core started", zap.String("grpc_addr", cfg.GRPCAddr), zap.String("debug_addr", cfg.DebugAddr))

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		return fmt.Errorf("grpc serve: %w", err)
	}
	log.L().Info("core shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if debug != nil {
		_ = debug.Shutdown(shutdownCtx)
	}
	stopped := make(chan struct{})
	go func() {
		gs.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-shutdownCtx.Done():
		gs.Stop()
	}
	return nil
}

func startDebugServer(addr string) *http.Server {
	if addr == "" {
		return nil
	}
	s := &http.Server{Addr: addr, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.L().Error("debug server", zap.Error(err))
		}
	}()
	return s
}
