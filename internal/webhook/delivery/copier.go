package delivery

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/IBM/sarama"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/pkg/kafkax"
	"github.com/yourname/roomkit/pkg/log"
)

const maxLoggedBody = 256

// Copier delivers one broadcast to the app's callback URL.
type Copier struct {
	apps     *AppCache
	limiters *Limiters
	sender   *Sender
}

// NewCopier returns a Copier.
func NewCopier(apps *AppCache, limiters *Limiters, sender *Sender) *Copier {
	return &Copier{apps: apps, limiters: limiters, sender: sender}
}

// Handle looks up the app configuration, converts msg, waits for the app's
// rate limit and posts it. Messages the app did not subscribe to are skipped.
func (c *Copier) Handle(ctx context.Context, msg *servicepb.ChatroomBcst) {
	appID := msg.GetAppId()
	l := log.L().With(
		zap.String("app_id", appID),
		zap.Int64("room_id", msg.GetChatroomId()),
		zap.String("msg_id", msg.GetMsgId()),
		zap.Stringer("msg_type", msg.GetMsgType()))

	cfg, err := c.apps.Get(ctx, appID)
	if err != nil {
		l.Error("load app config", zap.Error(err))
		return
	}
	if cfg == nil || cfg.Secret == "" {
		return
	}
	out := ToPostData(msg, cfg)
	if out == nil {
		return
	}
	url, qps := cfg.Route(msg.GetMsgType())
	if url == "" {
		return
	}
	body, err := json.Marshal(out)
	if err != nil {
		l.Error("marshal webhook body", zap.Error(err))
		return
	}
	if err := c.limiters.Wait(ctx, appID, msg.GetMsgType(), qps); err != nil {
		l.Warn("rate limit wait aborted", zap.Error(err))
		return
	}
	l.Debug("post webhook", zap.String("url", url), zap.Int("len", len(body)))
	if err := c.sender.Post(ctx, url, appID, cfg.Secret, body); err != nil {
		l.Error("post webhook",
			zap.String("url", url),
			zap.ByteString("body_head", body[:min(len(body), maxLoggedBody)]),
			zap.Error(err))
	}
}

// KafkaHandler decodes ChatroomBcst messages from Kafka and submits them to d.
func KafkaHandler(d *Dispatcher) kafkax.HandlerFunc {
	return func(_ context.Context, m *sarama.ConsumerMessage) error {
		msg := &servicepb.ChatroomBcst{}
		if err := proto.Unmarshal(m.Value, msg); err != nil {
			return fmt.Errorf("unmarshal ChatroomBcst: %w", err)
		}
		if msg.GetAppId() == "" {
			return nil
		}
		d.Submit(msg)
		return nil
	}
}
