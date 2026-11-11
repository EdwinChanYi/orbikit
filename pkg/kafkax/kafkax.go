// Package kafkax wraps sarama producer and consumer-group setup.
package kafkax

import (
	"context"
	"errors"
	"time"

	"github.com/IBM/sarama"
	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/pkg/log"
)

// Config describes the Kafka cluster.
type Config struct {
	Brokers []string `yaml:"brokers"`
}

func baseConfig() *sarama.Config {
	c := sarama.NewConfig()
	c.Version = sarama.V2_1_0_0
	return c
}

// NewSyncProducer returns a producer that waits for all in-sync replicas.
func NewSyncProducer(cfg Config) (sarama.SyncProducer, error) {
	c := baseConfig()
	c.Producer.RequiredAcks = sarama.WaitForAll
	c.Producer.Partitioner = sarama.NewHashPartitioner
	c.Producer.Return.Successes = true
	return sarama.NewSyncProducer(cfg.Brokers, c)
}

// NewConsumerGroup returns a consumer group that starts from the newest offset
// when the group has no committed offset yet.
func NewConsumerGroup(cfg Config, groupID string) (sarama.ConsumerGroup, error) {
	c := baseConfig()
	c.Consumer.Return.Errors = true
	c.Consumer.Offsets.Initial = sarama.OffsetNewest
	c.Consumer.Fetch.Default = 1 << 20
	return sarama.NewConsumerGroup(cfg.Brokers, groupID, c)
}

// HandlerFunc processes one message. Returning an error only logs it; the
// offset is still marked so a poison message cannot block the partition.
type HandlerFunc func(ctx context.Context, msg *sarama.ConsumerMessage) error

// Consume runs the consumer group until ctx is cancelled, rejoining after rebalances.
func Consume(ctx context.Context, group sarama.ConsumerGroup, topics []string, fn HandlerFunc) {
	go func() {
		for err := range group.Errors() {
			log.L().Warn("kafka consumer error", zap.Error(err))
		}
	}()
	h := handler{fn: fn}
	for {
		if err := group.Consume(ctx, topics, h); err != nil {
			if errors.Is(err, sarama.ErrClosedConsumerGroup) {
				return
			}
			log.L().Error("kafka consume", zap.Strings("topics", topics), zap.Error(err))
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
		if ctx.Err() != nil {
			return
		}
	}
}

type handler struct{ fn HandlerFunc }

func (handler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (handler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (h handler) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		if err := h.fn(sess.Context(), msg); err != nil {
			log.L().Error("kafka handle message",
				zap.String("topic", msg.Topic),
				zap.Int32("partition", msg.Partition),
				zap.Int64("offset", msg.Offset),
				zap.Error(err))
		}
		sess.MarkMessage(msg, "")
	}
	return nil
}
