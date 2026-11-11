package webhook

import (
	"testing"
	"time"

	"github.com/EdwinChanYi/orbikit/pkg/config"
)

func TestExampleConfigLoads(t *testing.T) {
	cfg := DefaultConfig()
	if err := config.Load("../../configs/webhook.yaml", &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.GRPCAddr != ":9004" || cfg.Topics.Broadcast != "orbikit.room.broadcast" || cfg.Kafka.GroupID != "webhook" {
		t.Fatalf("unexpected config %+v", cfg)
	}
	if len(cfg.Kafka.Brokers) != 1 || cfg.Kafka.Brokers[0] != "localhost:9092" {
		t.Fatalf("brokers = %v", cfg.Kafka.Brokers)
	}
	d := cfg.Delivery
	if d.WorkersPerApp != 32 || d.QueueSize != 10000 || d.Timeout != 2*time.Second || d.MaxRetries != 2 ||
		d.RetryBackoff != 200*time.Millisecond || d.MaxRetryBackoff != 2*time.Second || d.InsecureSkipVerify {
		t.Fatalf("delivery = %+v", d)
	}
	if cfg.Cache.Expire != time.Minute || cfg.Cache.NotFoundExpire != time.Minute || cfg.Core.Timeout != 3*time.Second {
		t.Fatalf("cache/core = %+v %+v", cfg.Cache, cfg.Core)
	}
}
