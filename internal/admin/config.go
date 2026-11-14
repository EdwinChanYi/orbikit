// Package admin holds the admin service configuration.
package admin

import (
	"time"

	"github.com/EdwinChanYi/orbikit/pkg/log"
)

// Config is the admin service configuration.
type Config struct {
	// HTTPAddr is the listen address of the admin HTTP server.
	HTTPAddr string `yaml:"http_addr"`
	// MySQLDSN opens the orbikit_admin database.
	MySQLDSN string `yaml:"mysql_dsn"`
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
