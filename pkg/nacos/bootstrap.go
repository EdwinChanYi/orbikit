package nacos

import (
	"context"
	"crypto/md5"
	"fmt"

	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/pkg/log"
)

// NacosConfig is the local bootstrap for an optional Nacos server.
type NacosConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Addr     string `yaml:"addr"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// Client returns a Nacos client, or nil when the switch is off.
func (cfg NacosConfig) Client() *Client {
	if !cfg.Enabled || cfg.Addr == "" {
		return nil
	}
	return New(cfg.Addr, cfg.Username, cfg.Password)
}

// Start watches dataID when Nacos is enabled and calls apply with each new body.
// It returns when ctx is cancelled. A disabled config returns immediately.
func Start(ctx context.Context, service string, cfg NacosConfig, local []byte, apply func(content string)) {
	if !cfg.Enabled || cfg.Addr == "" {
		return
	}
	dataID, err := DataID(service)
	if err != nil {
		log.L().Warn("nacos data id", zap.Error(err))
		return
	}
	client := New(cfg.Addr, cfg.Username, cfg.Password)
	content := string(local)
	remote, err := client.Get(ctx, dataID)
	if err != nil {
		log.L().Warn("nacos overlay", zap.Error(fmt.Errorf("using local config: %w", err)))
	} else {
		content = remote
	}
	sum := fmt.Sprintf("%x", md5.Sum([]byte(content)))
	client.Watch(ctx, dataID, sum, apply)
}
