package core

import (
	"testing"
	"time"

	"github.com/yourname/roomkit/pkg/config"
)

func TestExampleConfigLoads(t *testing.T) {
	t.Setenv("MYSQL_PASSWORD", "pw")
	cfg := DefaultConfig()
	if err := config.Load("../../configs/core.yaml", &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.GRPCAddr != ":9001" || cfg.CacheExpire != time.Minute || cfg.Notify.Timeout != 5*time.Second {
		t.Fatalf("unexpected config %+v", cfg)
	}
	if len(cfg.Notify.WebhookTargets) != 1 || cfg.Notify.WebhookTargets[0] != "localhost:9004" || len(cfg.Notify.CorePeers) != 0 {
		t.Fatalf("notify = %+v", cfg.Notify)
	}
	if want := "root:pw@tcp(localhost:3306)/roomkit?charset=utf8mb4&parseTime=true&loc=Local"; cfg.MySQL.DSN != want {
		t.Fatalf("dsn = %s", cfg.MySQL.DSN)
	}
	if len(cfg.Redis.Addrs) != 1 || cfg.Redis.Addrs[0] != "localhost:6379" {
		t.Fatalf("redis = %+v", cfg.Redis)
	}
}
