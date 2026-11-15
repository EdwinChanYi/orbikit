// Package webhook holds the webhook service configuration.
package webhook

import (
	"time"

	"github.com/EdwinChanYi/orbikit/internal/webhook/delivery"
	"github.com/EdwinChanYi/orbikit/pkg/appnotify"
	"github.com/EdwinChanYi/orbikit/pkg/discovery"
	"github.com/EdwinChanYi/orbikit/pkg/kafkax"
	"github.com/EdwinChanYi/orbikit/pkg/log"
	"github.com/EdwinChanYi/orbikit/pkg/nacos"
	"github.com/EdwinChanYi/orbikit/pkg/redisx"
)

// Config is the webhook service configuration.
type Config struct {
	// GRPCAddr is the listen address of the MsgCopy gRPC service.
	GRPCAddr string `yaml:"grpc_addr"`
	// DebugAddr serves net/http/pprof when non-empty.
	DebugAddr string `yaml:"debug_addr"`
	// ShutdownTimeout bounds draining queued deliveries on exit.
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`

	Log      log.Config               `yaml:"log"`
	Redis    redisx.Config            `yaml:"redis"`
	Notify   appnotify.Config         `yaml:"notify"`
	Kafka    KafkaConfig              `yaml:"kafka"`
	Topics   TopicsConfig             `yaml:"topics"`
	Core     CoreConfig               `yaml:"core"`
	Cache    CacheConfig              `yaml:"cache"`
	Delivery DeliveryConfig           `yaml:"delivery"`
	Etcd     discovery.Config         `yaml:"etcd"`
	Register discovery.RegisterConfig `yaml:"register"`
	Nacos    nacos.NacosConfig        `yaml:"nacos"`
}

// KafkaConfig selects the cluster and the shared consumer group.
type KafkaConfig struct {
	kafkax.Config `yaml:",inline"`
	GroupID       string `yaml:"group_id"`
}

// TopicsConfig names the consumed topics.
type TopicsConfig struct {
	Broadcast string `yaml:"broadcast"`
}

// CoreConfig locates the core service.
type CoreConfig struct {
	Target  string        `yaml:"target"`
	Timeout time.Duration `yaml:"timeout"`
}

// CacheConfig controls the app configuration cache.
type CacheConfig struct {
	// Expire is how long a loaded app configuration is used before reloading.
	Expire time.Duration `yaml:"expire"`
	// NotFoundExpire is how long an unknown app id is remembered.
	NotFoundExpire time.Duration `yaml:"not_found_expire"`
}

// DeliveryConfig controls queuing and HTTP delivery.
type DeliveryConfig struct {
	WorkersPerApp         int `yaml:"workers_per_app"`
	QueueSize             int `yaml:"queue_size"`
	delivery.SenderConfig `yaml:",inline"`
}

// DefaultConfig returns the defaults applied before the config file is loaded.
func DefaultConfig() Config {
	return Config{
		GRPCAddr:        ":9004",
		ShutdownTimeout: 10 * time.Second,
		Log:             log.Config{Level: "info"},
		Redis:           redisx.Config{Addrs: []string{"localhost:6379"}, PoolSize: 32},
		Notify:          appnotify.Config{Channel: appnotify.DefaultChannel},
		Kafka: KafkaConfig{
			Config:  kafkax.Config{Brokers: []string{"localhost:9092"}},
			GroupID: "webhook",
		},
		Topics: TopicsConfig{Broadcast: "orbikit.room.broadcast"},
		Core:   CoreConfig{Target: "localhost:9001", Timeout: 3 * time.Second},
		Cache:  CacheConfig{Expire: 60 * time.Second, NotFoundExpire: time.Minute},
		Delivery: DeliveryConfig{
			WorkersPerApp: 32,
			QueueSize:     10000,
			SenderConfig: delivery.SenderConfig{
				Timeout:         2 * time.Second,
				MaxRetries:      2,
				RetryBackoff:    200 * time.Millisecond,
				MaxRetryBackoff: 2 * time.Second,
			},
		},
	}
}
