// Package notify tells other service instances that app configuration changed.
package notify

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/pkg/grpcx"
	"github.com/EdwinChanYi/orbikit/pkg/log"
)

// Config lists the instances to notify. Every instance must be listed
// separately: a load-balanced target only reaches one of them.
type Config struct {
	// Timeout bounds each notification call.
	Timeout time.Duration `yaml:"timeout"`
	// WebhookTargets are the gRPC addresses of every webhook instance.
	WebhookTargets []string `yaml:"webhook_targets"`
	// CorePeers are the gRPC addresses of the other core instances.
	CorePeers []string `yaml:"core_peers"`
}

// Notifier fans out app change notifications. Failures are logged only.
type Notifier struct {
	timeout  time.Duration
	webhooks []servicepb.MsgCopyClient
	peers    []servicepb.CoreClient
	conns    []*grpc.ClientConn
}

// New dials every configured target.
func New(cfg Config) (*Notifier, error) {
	n := &Notifier{timeout: cfg.Timeout}
	if n.timeout <= 0 {
		n.timeout = 5 * time.Second
	}
	dial := func(target string) (*grpc.ClientConn, error) {
		conn, err := grpcx.Dial(target)
		if err != nil {
			return nil, fmt.Errorf("dial %s: %w", target, err)
		}
		n.conns = append(n.conns, conn)
		return conn, nil
	}
	for _, t := range cfg.WebhookTargets {
		conn, err := dial(t)
		if err != nil {
			n.Close()
			return nil, err
		}
		n.webhooks = append(n.webhooks, servicepb.NewMsgCopyClient(conn))
	}
	for _, t := range cfg.CorePeers {
		conn, err := dial(t)
		if err != nil {
			n.Close()
			return nil, err
		}
		n.peers = append(n.peers, servicepb.NewCoreClient(conn))
	}
	return n, nil
}

// Close releases all connections.
func (n *Notifier) Close() {
	for _, c := range n.conns {
		_ = c.Close()
	}
}

// AppInfoUpdated pushes the new message copy settings to every webhook instance.
func (n *Notifier) AppInfoUpdated(ctx context.Context, req *servicepb.AppInfoUpdatedRequest) {
	n.fanOut(ctx, len(n.webhooks), func(ctx context.Context, i int) error {
		_, err := n.webhooks[i].AppInfoUpdated(ctx, req)
		return err
	}, "notify webhook app info updated", zap.String("app_id", req.GetAppId()))
}

// AppCacheInvalidated asks every core peer to drop its cached copy of the app.
func (n *Notifier) AppCacheInvalidated(ctx context.Context, appID, traceID string) {
	req := &servicepb.UpdateAppCacheInfoRequest{AppId: appID, MsgTraceId: traceID}
	n.fanOut(ctx, len(n.peers), func(ctx context.Context, i int) error {
		_, err := n.peers[i].UpdateAppCacheInfo(ctx, req)
		return err
	}, "notify core peer app cache invalidated", zap.String("app_id", appID))
}

func (n *Notifier) fanOut(ctx context.Context, count int, call func(context.Context, int) error,
	msg string, fields ...zap.Field) {
	if count == 0 {
		return
	}
	ctx = context.WithoutCancel(ctx)
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, n.timeout)
			defer cancel()
			if err := call(cctx, i); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		log.L().Error(msg, append(fields, zap.Error(err))...)
	}
}
