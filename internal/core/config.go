// Package core holds the core service configuration.
package core

import (
	"time"

	"github.com/EdwinChanYi/orbikit/pkg/appnotify"
	"github.com/EdwinChanYi/orbikit/pkg/discovery"
	"github.com/EdwinChanYi/orbikit/pkg/log"
	"github.com/EdwinChanYi/orbikit/pkg/nacos"
	"github.com/EdwinChanYi/orbikit/pkg/mysqlx"
	"github.com/EdwinChanYi/orbikit/pkg/redisx"
)

// Config is the core service configuration.
type Config struct {
	// GRPCAddr is the listen address of the Core gRPC service.
	GRPCAddr string `yaml:"grpc_addr"`
	// DebugAddr serves net/http/pprof when non-empty.
	DebugAddr string `yaml:"debug_addr"`
	// CacheExpire is how long app lookups are cached in memory.
	CacheExpire time.Duration `yaml:"cache_expire"`
	// ShutdownTimeout bounds graceful shutdown.
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`

	Log      log.Config               `yaml:"log"`
	MySQL    mysqlx.Config            `yaml:"mysql"`
	Redis    redisx.Config            `yaml:"redis"`
	Notify   appnotify.Config         `yaml:"notify"`
	Etcd     discovery.Config         `yaml:"etcd"`
	Register discovery.RegisterConfig `yaml:"register"`
	Nacos    nacos.NacosConfig        `yaml:"nacos"`
}

// DefaultConfig returns the defaults applied before the config file is loaded.
func DefaultConfig() Config {
	return Config{
		GRPCAddr:        ":9001",
		CacheExpire:     60 * time.Second,
		ShutdownTimeout: 10 * time.Second,
		Log:             log.Config{Level: "info"},
		Redis:           redisx.Config{Addrs: []string{"localhost:6379"}, PoolSize: 64},
		Notify:          appnotify.Config{Channel: appnotify.DefaultChannel},
	}
}
