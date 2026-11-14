// Package builder queries whitelist compose containers through the Docker Engine API.
package builder

import (
	"time"

	"github.com/EdwinChanYi/orbikit/pkg/log"
)

// Config is the builder service configuration.
type Config struct {
	// HTTPAddr is the listen address of the builder HTTP server.
	HTTPAddr string `yaml:"http_addr"`
	// Socket is the Docker Engine API unix socket.
	Socket string `yaml:"socket"`
	// Project is the compose project prefix used in container names.
	Project string `yaml:"project"`
	// Log is the process logger configuration.
	Log log.Config `yaml:"log"`
	// ShutdownTimeout bounds graceful shutdown.
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

// DefaultConfig returns the defaults applied before the config file is loaded.
func DefaultConfig() Config {
	return Config{
		HTTPAddr:        ":8092",
		Socket:          "/var/run/docker.sock",
		Project:         "orbikit",
		ShutdownTimeout: 10 * time.Second,
		Log: log.Config{
			Level:      "info",
			MaxSizeMB:  1024,
			MaxAgeDays: 30,
			Console:    true,
		},
	}
}
