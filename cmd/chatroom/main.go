// Command chatroom runs the chatroom service: rooms, members, roles, mic
// seats and room messages.
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
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/internal/chatroom"
	"github.com/yourname/roomkit/internal/chatroom/event"
	"github.com/yourname/roomkit/internal/chatroom/lock"
	"github.com/yourname/roomkit/internal/chatroom/logic"
	"github.com/yourname/roomkit/internal/chatroom/model"
	"github.com/yourname/roomkit/internal/chatroom/reqctx"
	"github.com/yourname/roomkit/internal/chatroom/scheduler"
	"github.com/yourname/roomkit/internal/chatroom/server"
	"github.com/yourname/roomkit/internal/chatroom/watcher"
	"github.com/yourname/roomkit/pkg/config"
	"github.com/yourname/roomkit/pkg/grpcx"
	"github.com/yourname/roomkit/pkg/idgen"
	"github.com/yourname/roomkit/pkg/kafkax"
	"github.com/yourname/roomkit/pkg/log"
	"github.com/yourname/roomkit/pkg/mysqlx"
	"github.com/yourname/roomkit/pkg/redisx"
)

func main() {
	cfgPath := flag.String("config", "configs/chatroom.yaml", "path to the config file")
	flag.Parse()
	if err := run(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "chatroom:", err)
		os.Exit(1)
	}
}

func run(cfgPath string) error {
	cfg := chatroom.DefaultConfig()
	if err := config.Load(cfgPath, &cfg); err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("config %s: %w", cfgPath, err)
	}
	if err := log.Init(cfg.Log, "chatroom"); err != nil {
		return err
	}
	defer log.Sync()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rdb, err := redisx.New(ctx, cfg.Redis)
	if err != nil {
		return err
	}
	defer rdb.Close()

	db, err := mysqlx.New(cfg.MySQL)
	if err != nil {
		return err
	}
	if sqlDB, err := db.DB(); err == nil {
		defer sqlDB.Close()
	}
	if err := model.AutoMigrate(db.WithContext(ctx)); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}

	producer, err := kafkax.NewSyncProducer(cfg.Kafka)
	if err != nil {
		return fmt.Errorf("kafka producer: %w", err)
	}
	defer producer.Close()

	coreConn, err := grpcx.Dial(cfg.CoreAddr)
	if err != nil {
		return fmt.Errorf("dial core: %w", err)
	}
	defer coreConn.Close()

	ids, err := idgen.New(ctx, rdb, "chatroom")
	if err != nil {
		return err
	}
	defer ids.Close()

	reqctx.SetIDGenerator(ids.Next)
	reqctx.SetTraceReply(cfg.TraceGRPCReply)
	lock.Init(rdb)
	logic.Init(logic.Deps{
		DB:     db,
		Redis:  rdb,
		Core:   servicepb.NewCoreClient(coreConn),
		Events: event.NewProducer(producer, cfg.Topics),
		Settings: logic.Settings{
			EnterTokenTTL:     cfg.EnterTokenTTL,
			OfflineLeaveAfter: cfg.OfflineLeaveAfter,
		},
	})

	var bg sync.WaitGroup
	bg.Add(2)
	go func() {
		defer bg.Done()
		scheduler.Run(ctx)
	}()
	go func() {
		defer bg.Done()
		if err := watcher.Run(ctx, rdb); err != nil {
			log.L().Error("redis expiration watcher stopped", zap.Error(err))
		}
	}()

	gs := grpcx.NewServer()
	servicepb.RegisterChatroomServer(gs, server.New())
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		stop()
		bg.Wait()
		return err
	}

	debug := startDebugServer(cfg.DebugAddr)

	serveErr := make(chan error, 1)
	go func() { serveErr <- gs.Serve(lis) }()
	log.L().Info("chatroom started", zap.String("grpc_addr", cfg.GRPCAddr), zap.String("debug_addr", cfg.DebugAddr))

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		runErr = fmt.Errorf("grpc serve: %w", err)
	}
	log.L().Info("chatroom shutting down")
	stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if debug != nil {
		_ = debug.Shutdown(shutdownCtx)
	}
	stopped := make(chan struct{})
	go func() {
		gs.GracefulStop()
		bg.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-shutdownCtx.Done():
		gs.Stop()
	}
	return runErr
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
