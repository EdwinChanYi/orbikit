package gateway

import (
	"testing"

	"github.com/EdwinChanYi/orbikit/pkg/config"
)

func TestDeployConfigUsesEtcd(t *testing.T) {
	cfg := DefaultConfig()
	if err := config.Load("../../deploy/configs/gateway.yaml", &cfg); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.CoreAddr != "etcd:///core" || cfg.ChatroomAddr != "etcd:///chatroom" {
		t.Fatalf("targets = %q %q", cfg.CoreAddr, cfg.ChatroomAddr)
	}
	if !cfg.Etcd.Enabled() || cfg.Etcd.Endpoints[0] != "etcd:2379" {
		t.Fatalf("etcd = %+v", cfg.Etcd)
	}
}
