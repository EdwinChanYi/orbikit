package chatroom

import (
	"testing"
	"time"

	"github.com/EdwinChanYi/orbikit/pkg/config"
)

func TestSampleConfig(t *testing.T) {
	t.Setenv("REDIS_PASSWORD", "redis-secret")
	t.Setenv("MYSQL_PASSWORD", "mysql-secret")
	cfg := DefaultConfig()
	if err := config.Load("../../configs/chatroom.yaml", &cfg); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.GRPCAddr != ":9002" || cfg.CoreAddr != "localhost:9001" || cfg.DebugAddr != "" {
		t.Fatalf("addresses = %q %q %q", cfg.GRPCAddr, cfg.CoreAddr, cfg.DebugAddr)
	}
	if cfg.Redis.Password != "redis-secret" {
		t.Fatalf("redis password not expanded: %q", cfg.Redis.Password)
	}
	if cfg.EnterTokenTTL != 300*time.Second || cfg.OfflineLeaveAfter != 60*time.Second {
		t.Fatalf("durations = %v %v", cfg.EnterTokenTTL, cfg.OfflineLeaveAfter)
	}
	want := map[string]string{
		"broadcast":  "orbikit.room.broadcast",
		"fullserver": "orbikit.room.fullserver",
		"kicked":     "orbikit.room.kicked",
		"event":      "orbikit.room.event",
	}
	got := map[string]string{
		"broadcast":  cfg.Topics.Broadcast,
		"fullserver": cfg.Topics.FullServer,
		"kicked":     cfg.Topics.Kicked,
		"event":      cfg.Topics.Event,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("topics.%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestValidate(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err == nil {
		t.Fatal("defaults lack mysql and kafka and should not validate")
	}
	cfg.MySQL.DSN = "dsn"
	cfg.Kafka.Brokers = []string{"k:9092"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.Topics.Kicked = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("missing topic should fail")
	}
	cfg.Topics.Kicked = "k"
	cfg.OfflineLeaveAfter = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("zero offline_leave_after should fail")
	}
}
