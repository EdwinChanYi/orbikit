package builder

import (
	"testing"
	"time"

	"github.com/EdwinChanYi/orbikit/pkg/config"
)

func TestDefaultListenAddr(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.HTTPAddr != ":8092" {
		t.Fatalf("addr = %s", cfg.HTTPAddr)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("shutdown = %s", cfg.ShutdownTimeout)
	}
}

func TestExampleConfig(t *testing.T) {
	for _, path := range []string{"../../configs/builder.yaml", "../../deploy/configs/builder.yaml"} {
		var cfg Config
		if err := config.Load(path, &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.HTTPAddr != ":8092" {
			t.Fatalf("%s addr = %s", path, cfg.HTTPAddr)
		}
		if cfg.Socket != "/var/run/docker.sock" {
			t.Fatalf("%s socket = %s", path, cfg.Socket)
		}
		if cfg.Project != "orbikit" {
			t.Fatalf("%s project = %s", path, cfg.Project)
		}
		if cfg.ShutdownTimeout != 10*time.Second {
			t.Fatalf("%s shutdown = %s", path, cfg.ShutdownTimeout)
		}
		if cfg.Log.Level != "info" || cfg.Log.File != "" || !cfg.Log.Console || cfg.Log.MaxSizeMB != 1024 || cfg.Log.MaxAgeDays != 30 {
			t.Fatalf("%s log = %+v", path, cfg.Log)
		}
	}
}
