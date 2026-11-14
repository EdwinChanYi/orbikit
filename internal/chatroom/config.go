// Package chatroom holds the configuration of the chatroom service.
package chatroom

import (
	"errors"
	"time"

	"github.com/EdwinChanYi/orbikit/internal/chatroom/event"
	"github.com/EdwinChanYi/orbikit/pkg/kafkax"
	"github.com/EdwinChanYi/orbikit/pkg/log"
	"github.com/EdwinChanYi/orbikit/pkg/mysqlx"
	"github.com/EdwinChanYi/orbikit/pkg/nacos"
	"github.com/EdwinChanYi/orbikit/pkg/redisx"
)

// Config is the chatroom service configuration file.
type Config struct {
	// GRPCAddr is the listen address of the Chatroom gRPC service.
	GRPCAddr string `yaml:"grpc_addr"`
	// DebugAddr serves net/http/pprof when non-empty.
	DebugAddr string `yaml:"debug_addr"`
	// CoreAddr is the Core gRPC target used to validate apps and ban users.
	CoreAddr string `yaml:"core_addr"`
	// ShutdownTimeout bounds graceful shutdown.
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	// EnterTokenTTL is how long an enter token stays valid.
	EnterTokenTTL time.Duration `yaml:"enter_token_ttl"`
	// OfflineLeaveAfter is how long a disconnected user stays in rooms.
	OfflineLeaveAfter time.Duration `yaml:"offline_leave_after"`
	// TraceGRPCReply logs every gRPC reply at debug level.
	TraceGRPCReply bool `yaml:"trace_grpc_reply"`

	Log    log.Config    `yaml:"log"`
	Redis  redisx.Config `yaml:"redis"`
	MySQL  mysqlx.Config `yaml:"mysql"`
	Kafka  kafkax.Config `yaml:"kafka"`
	Topics event.Topics   `yaml:"topics"`
	Nacos  nacos.NacosConfig `yaml:"nacos"`
}

// DefaultConfig returns the values used for settings missing from the file.
func DefaultConfig() Config {
	return Config{
		GRPCAddr:        ":9002",
		CoreAddr:        "localhost:9001",
		ShutdownTimeout: 10 * time.Second,
		Log:             log.Config{Level: "info", Console: true},
		Redis:           redisx.Config{Addrs: []string{"localhost:6379"}, PoolSize: 64},
		Topics: event.Topics{
			Broadcast:  "orbikit.room.broadcast",
			FullServer: "orbikit.room.fullserver",
			Kicked:     "orbikit.room.kicked",
			Event:      "orbikit.room.event",
		},
		EnterTokenTTL:     300 * time.Second,
		OfflineLeaveAfter: 60 * time.Second,
	}
}

// Validate reports settings the service cannot start without.
func (c *Config) Validate() error {
	switch {
	case len(c.Redis.Addrs) == 0:
		return errors.New("redis.addrs is required")
	case c.MySQL.DSN == "":
		return errors.New("mysql.dsn is required")
	case len(c.Kafka.Brokers) == 0:
		return errors.New("kafka.brokers is required")
	case c.Topics.Broadcast == "" || c.Topics.FullServer == "" || c.Topics.Kicked == "" || c.Topics.Event == "":
		return errors.New("all topics must be set")
	case c.EnterTokenTTL <= 0 || c.OfflineLeaveAfter <= 0:
		return errors.New("enter_token_ttl and offline_leave_after must be positive")
	}
	return nil
}
