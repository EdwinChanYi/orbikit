// Package admin holds the admin service configuration.
package admin

import (
	"time"

	"github.com/EdwinChanYi/orbikit/pkg/discovery"
	"github.com/EdwinChanYi/orbikit/pkg/log"
	"github.com/EdwinChanYi/orbikit/pkg/nacos"
)

// Config is the admin service configuration.
type Config struct {
	// HTTPAddr is the listen address of the admin HTTP server.
	HTTPAddr string `yaml:"http_addr"`
	// MySQLDSN opens the orbikit_admin database.
	MySQLDSN string `yaml:"mysql_dsn"`
	// BuilderURL is the builder base URL. Overview reads
	// {BuilderURL}/v1/containers/{name}.
	BuilderURL string `yaml:"builder_url"`
	// Etcd lists registered service instances.
	Etcd discovery.Config `yaml:"etcd"`
	// Nacos is the optional config center. Disabled leaves the config page empty.
	Nacos nacos.NacosConfig `yaml:"nacos"`
	// Log is the process logger configuration.
	Log log.Config `yaml:"log"`
	// ShutdownTimeout bounds graceful shutdown.
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

// DefaultConfig returns the defaults applied before the config file is loaded.
func DefaultConfig() Config {
	return Config{
		HTTPAddr:        ":8091",
		ShutdownTimeout: 10 * time.Second,
		Log: log.Config{
			Level:      "info",
			MaxSizeMB:  1024,
			MaxAgeDays: 30,
			Console:    true,
		},
	}
}
