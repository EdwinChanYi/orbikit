package gateway

import (
	"testing"

	"github.com/EdwinChanYi/orbikit/pkg/config"
)

func TestLocalConfigLeavesNacosDisabled(t *testing.T) {
	cfg := DefaultConfig()
	if err := config.Load("../../configs/gateway.yaml", &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Nacos.Enabled {
		t.Fatal("nacos should be disabled")
	}
}
