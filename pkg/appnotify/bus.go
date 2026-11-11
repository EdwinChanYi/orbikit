// Package appnotify broadcasts app configuration changes over Redis pub/sub,
// so every core and webhook instance hears them without a per-instance address list.
package appnotify

import (
	"context"
	"io"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/pkg/log"
)

// DefaultChannel is the pub/sub channel used when none is configured.
const DefaultChannel = "orbikit:app_config"

// Config selects the channel. Pub/sub is node-local in Redis Cluster, so all
// publishers and subscribers must reach the same node; a single-node Redis does.
type Config struct {
	Channel string `yaml:"channel"`
}

// Bus publishes and subscribes to app configuration changes.
type Bus struct {
	rdb     redis.UniversalClient
	channel string
}

// New returns a bus. An empty channel selects DefaultChannel.
func New(rdb redis.UniversalClient, cfg Config) *Bus {
	ch := cfg.Channel
	if ch == "" {
		ch = DefaultChannel
	}
	return &Bus{rdb: rdb, channel: ch}
}

// Publish sends the new copy settings. Delivery failures are logged.
func (b *Bus) Publish(ctx context.Context, req *servicepb.AppInfoUpdatedRequest) {
	raw, err := proto.Marshal(req)
	if err != nil {
		log.L().Error("marshal app config event", zap.Error(err))
		return
	}
	if err := b.rdb.Publish(ctx, b.channel, raw).Err(); err != nil {
		log.L().Error("publish app config event", zap.String("app_id", req.GetAppId()), zap.Error(err))
	}
}

// Run subscribes until ctx is cancelled, reconnecting after errors.
func (b *Bus) Run(ctx context.Context, handle func(*servicepb.AppInfoUpdatedRequest)) {
	for ctx.Err() == nil {
		err := b.consume(ctx, handle)
		if err == nil || ctx.Err() != nil {
			return
		}
		log.L().Warn("app config subscription dropped", zap.Error(err))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (b *Bus) consume(ctx context.Context, handle func(*servicepb.AppInfoUpdatedRequest)) error {
	sub := b.rdb.Subscribe(ctx, b.channel)
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		return err
	}
	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-ch:
			if !ok {
				return io.ErrClosedPipe
			}
			req := &servicepb.AppInfoUpdatedRequest{}
			if err := proto.Unmarshal([]byte(msg.Payload), req); err != nil {
				log.L().Warn("bad app config event", zap.Error(err))
				continue
			}
			handle(req)
		}
	}
}
