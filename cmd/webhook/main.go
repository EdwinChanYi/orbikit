// Command webhook copies chatroom messages to the HTTP callbacks registered by each app.
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
	"github.com/EdwinChanYi/orbikit/internal/webhook"
	"github.com/EdwinChanYi/orbikit/internal/webhook/delivery"
	"github.com/EdwinChanYi/orbikit/internal/webhook/server"
	"github.com/EdwinChanYi/orbikit/pkg/appnotify"
	"github.com/EdwinChanYi/orbikit/pkg/config"
	"github.com/EdwinChanYi/orbikit/pkg/grpcx"
	"github.com/EdwinChanYi/orbikit/pkg/kafkax"
	"github.com/EdwinChanYi/orbikit/pkg/log"
	"github.com/EdwinChanYi/orbikit/pkg/nacos"
	"github.com/EdwinChanYi/orbikit/pkg/redisx"
)

func main() {
	cfgPath := flag.String("config", "configs/webhook.yaml", "path to the config file")
	flag.Parse()
	if err := run(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "webhook:", err)
		os.Exit(1)
	}
}

func run(cfgPath string) error {
	cfg := webhook.DefaultConfig()
	if err := config.Load(cfgPath, &cfg); err != nil {
		return err
	}
	if err := log.Init(cfg.Log, "webhook"); err != nil {
		return err
	}
	defer log.Sync()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	local, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}
	if _, err := nacos.Overlay(ctx, cfg.Nacos.Client(), "webhook.yaml", &cfg); err != nil {
		log.L().Warn("nacos overlay", zap.Error(err))
	}
	if cfg.Log.Level != "" {
		if err := log.SetLevel(cfg.Log.Level); err != nil {
			log.L().Warn("nacos log level", zap.Error(err))
		}
	}

	coreConn, err := grpcx.Dial(cfg.Core.Target)
	if err != nil {
		return fmt.Errorf("dial core: %w", err)
	}
	defer coreConn.Close()

	rdb, err := redisx.New(ctx, cfg.Redis)
	if err != nil {
		return err
	}
	defer rdb.Close()

	apps := delivery.NewAppCache(
		&delivery.CoreSource{Client: servicepb.NewCoreClient(coreConn), Timeout: cfg.Core.Timeout},
		cfg.Cache.Expire, cfg.Cache.NotFoundExpire)
	current := string(local)
	go nacos.Start(ctx, "webhook", cfg.Nacos, local, func(content string) {
		hot, restart, err := nacos.Classify("webhook", []byte(current), []byte(content))
		if err != nil {
			log.L().Warn("nacos classify", zap.Error(err))
			return
		}
		var expire, notFound time.Duration
		for _, change := range hot {
			switch change.Path {
			case "log.level":
				if err := log.SetLevel(change.Value); err != nil {
					log.L().Warn("nacos log level", zap.Error(err))
				}
			case "cache.expire":
				expire, err = time.ParseDuration(change.Value)
				if err != nil {
					log.L().Warn("nacos cache.expire", zap.Error(err))
				}
			case "cache.not_found_expire":
				notFound, err = time.ParseDuration(change.Value)
				if err != nil {
					log.L().Warn("nacos cache.not_found_expire", zap.Error(err))
				}
			}
		}
		if expire > 0 || notFound > 0 {
			apps.SetTTL(expire, notFound)
		}
		if len(restart) > 0 {
			log.L().Info("restart required", zap.Strings("paths", restart))
		}
		current = content
	})
	go appnotify.New(rdb, cfg.Notify).Run(ctx, func(req *servicepb.AppInfoUpdatedRequest) {
		server.Apply(apps, req)
	})

	copier := delivery.NewCopier(apps, delivery.NewLimiters(), delivery.NewSender(cfg.Delivery.SenderConfig))
	dispatcher := delivery.NewDispatcher(cfg.Delivery.WorkersPerApp, cfg.Delivery.QueueSize, copier.Handle)

	group, err := kafkax.NewConsumerGroup(cfg.Kafka.Config, cfg.Kafka.GroupID)
	if err != nil {
		return fmt.Errorf("kafka consumer group: %w", err)
	}

	gs := grpcx.NewServer()
	servicepb.RegisterMsgCopyServer(gs, server.New(apps))
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		_ = group.Close()
		return err
	}

	debug := startDebugServer(cfg.DebugAddr)

	serveErr := make(chan error, 1)
	go func() { serveErr <- gs.Serve(lis) }()

	consumeCtx, stopConsume := context.WithCancel(ctx)
	consumed := make(chan struct{})
	go func() {
		defer close(consumed)
		kafkax.Consume(consumeCtx, group, []string{cfg.Topics.Broadcast}, delivery.KafkaHandler(dispatcher))
	}()

	log.L().Info("webhook started",
		zap.String("grpc_addr", cfg.GRPCAddr),
		zap.String("topic", cfg.Topics.Broadcast),
		zap.String("group_id", cfg.Kafka.GroupID))

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		runErr = fmt.Errorf("grpc serve: %w", err)
	}
	log.L().Info("webhook shutting down")

	stopConsume()
	<-consumed
	if err := group.Close(); err != nil {
		log.L().Warn("close kafka consumer group", zap.Error(err))
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	dispatcher.Close(shutdownCtx)
	if n := dispatcher.Dropped(); n > 0 {
		log.L().Warn("webhook messages dropped during run", zap.Uint64("dropped", n))
	}
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
