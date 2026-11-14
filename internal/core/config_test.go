package core

import (
	"testing"
	"time"

	"github.com/EdwinChanYi/orbikit/pkg/config"
)

func TestExampleConfigLoads(t *testing.T) {
	t.Setenv("MYSQL_PASSWORD", "pw")
	cfg := DefaultConfig()
	if err := config.Load("../../configs/core.yaml", &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.GRPCAddr != ":9001" || cfg.CacheExpire != time.Minute || cfg.Notify.Channel != "orbikit:app_config" {
		t.Fatalf("unexpected config %+v", cfg)
	}
	if want := "root:pw@tcp(localhost:3306)/orbikit?charset=utf8mb4&parseTime=true&loc=Local"; cfg.MySQL.DSN != want {
		t.Fatalf("dsn = %s", cfg.MySQL.DSN)
	}
	if len(cfg.Redis.Addrs) != 1 || cfg.Redis.Addrs[0] != "localhost:6379" {
		t.Fatalf("redis = %+v", cfg.Redis)
	}
	if cfg.Nacos.Enabled {
		t.Fatal("nacos should be disabled")
	}
}
