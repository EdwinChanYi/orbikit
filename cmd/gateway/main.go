// Command gateway runs the access gateway: client long links over TCP and
// WebSocket, the HTTP short link, and push delivery of chatroom events.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/gateway"
	"github.com/EdwinChanYi/orbikit/internal/gateway/handler"
	"github.com/EdwinChanYi/orbikit/internal/gateway/httpapi"
	"github.com/EdwinChanYi/orbikit/internal/gateway/moderation"
	"github.com/EdwinChanYi/orbikit/internal/gateway/protocol"
	"github.com/EdwinChanYi/orbikit/internal/gateway/push"
	"github.com/EdwinChanYi/orbikit/internal/gateway/session"
	"github.com/EdwinChanYi/orbikit/internal/gateway/store"
	"github.com/EdwinChanYi/orbikit/internal/gateway/transport"
	"github.com/EdwinChanYi/orbikit/pkg/config"
	"github.com/EdwinChanYi/orbikit/pkg/discovery"
	"github.com/EdwinChanYi/orbikit/pkg/grpcx"
	"github.com/EdwinChanYi/orbikit/pkg/idgen"
	"github.com/EdwinChanYi/orbikit/pkg/kafkax"
	"github.com/EdwinChanYi/orbikit/pkg/log"
	"github.com/EdwinChanYi/orbikit/pkg/nacos"
	"github.com/EdwinChanYi/orbikit/pkg/redisx"

	"gopkg.in/yaml.v3"
)

func main() {
	cfgPath := flag.String("config", "configs/gateway.yaml", "path to the config file")
	flag.Parse()
	if err := run(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "gateway:", err)
		os.Exit(1)
	}
}

func run(cfgPath string) error {
	cfg := gateway.DefaultConfig()
	if err := config.Load(cfgPath, &cfg); err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	if err := log.Init(cfg.Log, "gateway"); err != nil {
		return err
	}
	defer log.Sync()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	local, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}
	if _, err := nacos.Overlay(ctx, cfg.Nacos.Client(), "gateway.yaml", &cfg); err != nil {
		log.L().Warn("nacos overlay", zap.Error(err))
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	if cfg.Log.Level != "" {
		if err := log.SetLevel(cfg.Log.Level); err != nil {
			log.L().Warn("nacos log level", zap.Error(err))
		}
	}

	rdb, err := redisx.New(ctx, cfg.Redis)
	if err != nil {
		return err
	}
	defer rdb.Close()
	st := store.New(rdb)

	ids, err := idgen.New(ctx, rdb, "gateway")
	if err != nil {
		return fmt.Errorf("idgen: %w", err)
	}
	defer ids.Close()

	reg, err := discovery.Open(cfg.Etcd)
	if err != nil {
		return err
	}
	defer reg.Close()

	coreConn, err := grpcx.DialDiscovered(cfg.CoreAddr, reg)
	if err != nil {
		return fmt.Errorf("dial core: %w", err)
	}
	defer coreConn.Close()
	chatroomConn, err := grpcx.DialDiscovered(cfg.ChatroomAddr, reg)
	if err != nil {
		return fmt.Errorf("dial chatroom: %w", err)
	}
	defer chatroomConn.Close()

	moderator, err := moderation.New(cfg.Moderation)
	if err != nil {
		return fmt.Errorf("moderation: %w", err)
	}

	sessions := session.NewManager(session.Options{
		ClientVersion:     cfg.ClientVersion,
		HeartbeatInterval: cfg.Session.HeartbeatInterval,
		AuthTimeout:       cfg.Session.AuthTimeout,
		WriteTimeout:      cfg.Session.WriteTimeout,
		MaxPendingBytes:   cfg.Session.MaxPendingBytes,
	})
	svc := handler.New(handler.Deps{
		Core:      servicepb.NewCoreClient(coreConn),
		Chatroom:  servicepb.NewChatroomClient(chatroomConn),
		Store:     st,
		IDs:       ids,
		Moderator: moderator,
		Sessions:  sessions,
	}, handler.Options{
		RPCTimeout:     cfg.RPCTimeout,
		MessageReceipt: cfg.MessageReceipt,
	})
	current := string(local)
	go nacos.Start(ctx, "gateway", cfg.Nacos, local, func(content string) {
		hot, restart, err := nacos.Classify("gateway", []byte(current), []byte(content))
		if err != nil {
			log.L().Warn("nacos classify", zap.Error(err))
			return
		}
		for _, change := range hot {
			switch change.Path {
			case "log.level":
				if err := log.SetLevel(change.Value); err != nil {
					log.L().Warn("nacos log level", zap.Error(err))
				}
			case "moderation":
				var mod moderation.Config
				if err := yaml.Unmarshal([]byte(change.Value), &mod); err != nil {
					log.L().Warn("nacos moderation", zap.Error(err))
					continue
				}
				next, err := moderation.New(mod)
				if err != nil {
					log.L().Warn("nacos moderation", zap.Error(err))
					continue
				}
				svc.SetModerator(next)
			}
		}
		if len(restart) > 0 {
			log.L().Info("restart required", zap.Strings("paths", restart))
		}
		current = content
	})

	serveErr := make(chan error, 3)
	var listeners []interface {
		Close() error
		Wait(context.Context) error
	}

	if cfg.TCP.Addr != "" {
		codec, _ := protocol.NewCodec(cfg.TCP.Codec)
		srv, err := transport.ListenTCP(ctx, cfg.TCP.Addr, transport.TCPOptions{
			Codec:       codec,
			IdleTimeout: cfg.TCP.IdleTimeout,
			KeepAlive:   cfg.TCP.KeepAlive,
			MaxBodyLen:  cfg.MaxBodyLen,
		})
		if err != nil {
			return fmt.Errorf("listen tcp: %w", err)
		}
		listeners = append(listeners, srv)
		go func() { serveErr <- wrap("tcp", srv.Serve(sessions.Handler(svc))) }()
		log.L().Info("tcp long link listening", zap.Stringer("addr", srv.Addr()), zap.String("codec", codec.Name()))
	}
	if cfg.WS.Addr != "" {
		codec, _ := protocol.NewCodec(cfg.WS.Codec)
		srv, err := transport.ListenWS(ctx, cfg.WS.Addr, transport.WSOptions{
			Codec:          codec,
			Path:           cfg.WS.Path,
			IdleTimeout:    cfg.WS.IdleTimeout,
			MaxBodyLen:     cfg.MaxBodyLen,
			AllowedOrigins: cfg.WS.AllowedOrigins,
			TLSCertFile:    cfg.WS.TLSCertFile,
			TLSKeyFile:     cfg.WS.TLSKeyFile,
		})
		if err != nil {
			return fmt.Errorf("listen ws: %w", err)
		}
		listeners = append(listeners, srv)
		go func() { serveErr <- wrap("ws", srv.Serve(sessions.Handler(svc))) }()
		log.L().Info("websocket long link listening", zap.Stringer("addr", srv.Addr()), zap.String("path", cfg.WS.Path), zap.String("codec", codec.Name()))
	}
	var short *httpapi.Server
	if cfg.HTTP.Addr != "" {
		short, err = httpapi.Listen(ctx, cfg.HTTP.Addr, svc, st, cfg.MaxBodyLen+httpapi.Overhead)
		if err != nil {
			return fmt.Errorf("listen http: %w", err)
		}
		go func() { serveErr <- wrap("http", short.Serve()) }()
		log.L().Info("http short link listening", zap.Stringer("addr", short.Addr()))
	}

	groupID := cfg.ConsumerGroup()
	group, err := kafkax.NewConsumerGroup(cfg.Kafka.Config, groupID)
	if err != nil {
		return fmt.Errorf("kafka consumer group: %w", err)
	}

	bgCtx, bgCancel := context.WithCancel(context.Background())
	var bg sync.WaitGroup
	runBG := func(fn func(context.Context)) {
		bg.Add(1)
		go func() {
			defer bg.Done()
			fn(bgCtx)
		}()
	}
	dispatcher := push.NewDispatcher(sessions, cfg.Topics, cfg.MessageReceipt)
	runBG(func(ctx context.Context) { kafkax.Consume(ctx, group, cfg.Topics.List(), dispatcher.Handle) })
	runBG(push.NewKickWatcher(st, sessions, svc, ids.MachineID()).Run)
	runBG(st.RunDailyActiveRoller)

	debug := startDebugServer(cfg.DebugAddr)
	log.L().Info("gateway started",
		zap.Uint16("machine_id", ids.MachineID()),
		zap.String("kafka_group", groupID),
		zap.String("moderation", cfg.Moderation.Type),
		zap.String("debug_addr", cfg.DebugAddr))

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-serveErr:
	}
	log.L().Info("gateway shutting down", zap.Int("connections", sessions.Len()))

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	for _, l := range listeners {
		_ = l.Close()
	}
	if short != nil {
		if err := short.Shutdown(shutdownCtx); err != nil {
			log.L().Warn("http shutdown", zap.Error(err))
		}
	}
	bgCancel()
	if err := group.Close(); err != nil {
		log.L().Warn("kafka consumer close", zap.Error(err))
	}
	sessions.CloseAll()
	for _, l := range listeners {
		if err := l.Wait(shutdownCtx); err != nil {
			log.L().Warn("connections still open at shutdown", zap.Int("connections", sessions.Len()))
			break
		}
	}
	bg.Wait()
	if debug != nil {
		_ = debug.Shutdown(shutdownCtx)
	}
	return runErr
}

func wrap(name string, err error) error {
	if err == nil {
		return fmt.Errorf("%s server stopped", name)
	}
	return fmt.Errorf("%s serve: %w", name, err)
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
