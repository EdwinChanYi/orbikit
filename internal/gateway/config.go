// Package gateway holds the access gateway configuration.
package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/EdwinChanYi/orbikit/internal/gateway/moderation"
	"github.com/EdwinChanYi/orbikit/internal/gateway/protocol"
	"github.com/EdwinChanYi/orbikit/internal/gateway/push"
	"github.com/EdwinChanYi/orbikit/pkg/discovery"
	"github.com/EdwinChanYi/orbikit/pkg/kafkax"
	"github.com/EdwinChanYi/orbikit/pkg/log"
	"github.com/EdwinChanYi/orbikit/pkg/nacos"
	"github.com/EdwinChanYi/orbikit/pkg/redisx"
)

// Config is the gateway configuration.
type Config struct {
	// DebugAddr serves net/http/pprof when non-empty.
	DebugAddr string `yaml:"debug_addr"`
	// ShutdownTimeout bounds graceful shutdown.
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	// ClientVersion is written into server-initiated packets; clients drop
	// packets whose version differs from their own.
	ClientVersion uint32 `yaml:"client_version"`
	// MaxBodyLen bounds one packet body.
	MaxBodyLen int `yaml:"max_body_len"`

	// CoreAddr and ChatroomAddr are the downstream gRPC targets.
	CoreAddr     string        `yaml:"core_addr"`
	ChatroomAddr string        `yaml:"chatroom_addr"`
	RPCTimeout   time.Duration `yaml:"rpc_timeout"`
	// MessageReceipt asks clients to acknowledge every received room message.
	MessageReceipt bool `yaml:"message_receipt"`

	TCP     TCPConfig     `yaml:"tcp"`
	WS      WSConfig      `yaml:"ws"`
	HTTP    HTTPConfig    `yaml:"http"`
	Session SessionConfig `yaml:"session"`

	Log        log.Config        `yaml:"log"`
	Redis      redisx.Config     `yaml:"redis"`
	Kafka      KafkaConfig       `yaml:"kafka"`
	Topics     push.Topics       `yaml:"topics"`
	Moderation moderation.Config `yaml:"moderation"`
	Etcd       discovery.Config  `yaml:"etcd"`
	Nacos      nacos.NacosConfig `yaml:"nacos"`
}

// TCPConfig configures the raw TCP long link.
type TCPConfig struct {
	// Addr is the listen address; empty disables TCP.
	Addr  string `yaml:"addr"`
	Codec string `yaml:"codec"`
	// IdleTimeout closes connections silent for this long; 0 disables it.
	IdleTimeout time.Duration `yaml:"idle_timeout"`
	KeepAlive   time.Duration `yaml:"keepalive"`
}

// WSConfig configures the WebSocket long link.
type WSConfig struct {
	// Addr is the listen address; empty disables WebSocket.
	Addr        string        `yaml:"addr"`
	Path        string        `yaml:"path"`
	Codec       string        `yaml:"codec"`
	IdleTimeout time.Duration `yaml:"idle_timeout"`
	// AllowedOrigins restricts browser origins; empty allows all.
	AllowedOrigins []string `yaml:"allowed_origins"`
	TLSCertFile    string   `yaml:"tls_cert_file"`
	TLSKeyFile     string   `yaml:"tls_key_file"`
}

// HTTPConfig configures the HTTP short link.
type HTTPConfig struct {
	// Addr is the listen address; empty disables the short link.
	Addr string `yaml:"addr"`
}

// SessionConfig tunes per-connection behaviour.
type SessionConfig struct {
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`
	AuthTimeout       time.Duration `yaml:"auth_timeout"`
	WriteTimeout      time.Duration `yaml:"write_timeout"`
	MaxPendingBytes   int           `yaml:"max_pending_bytes"`
}

// KafkaConfig configures the broadcast consumer.
type KafkaConfig struct {
	kafkax.Config `yaml:",inline"`
	// GroupID must be unique per gateway instance; empty generates one.
	GroupID string `yaml:"group_id"`
}

// DefaultConfig returns the defaults applied before the config file is loaded.
func DefaultConfig() Config {
	return Config{
		ShutdownTimeout: 10 * time.Second,
		ClientVersion:   100000000,
		MaxBodyLen:      protocol.DefaultMaxBodyLen,
		CoreAddr:        "localhost:9001",
		ChatroomAddr:    "localhost:9002",
		RPCTimeout:      20 * time.Second,
		TCP: TCPConfig{
			Addr:      ":8000",
			Codec:     protocol.CodecXXTEA,
			KeepAlive: 3 * time.Minute,
		},
		WS: WSConfig{
			Addr:        ":8001",
			Path:        "/ws",
			Codec:       protocol.CodecPlain,
			IdleTimeout: 60 * time.Second,
		},
		HTTP: HTTPConfig{Addr: ":8080"},
		Session: SessionConfig{
			HeartbeatInterval: 5 * time.Second,
			AuthTimeout:       30 * time.Second,
			WriteTimeout:      5 * time.Second,
			MaxPendingBytes:   4 << 20,
		},
		Log:   log.Config{Level: "info"},
		Redis: redisx.Config{Addrs: []string{"localhost:6379"}, PoolSize: 64},
		Kafka: KafkaConfig{Config: kafkax.Config{Brokers: []string{"localhost:9092"}}},
		Topics: push.Topics{
			Broadcast:  "orbikit.room.broadcast",
			FullServer: "orbikit.room.fullserver",
			Kicked:     "orbikit.room.kicked",
			Event:      "orbikit.room.event",
		},
		Moderation: moderation.Config{Type: moderation.TypeNone},
	}
}

// Validate rejects configurations the gateway cannot run with.
func (c *Config) Validate() error {
	var errs []error
	if c.TCP.Addr == "" && c.WS.Addr == "" && c.HTTP.Addr == "" {
		errs = append(errs, errors.New("tcp.addr, ws.addr and http.addr are all empty"))
	}
	if c.TCP.Addr != "" {
		if _, err := protocol.NewCodec(c.TCP.Codec); err != nil {
			errs = append(errs, fmt.Errorf("tcp.codec: %w", err))
		}
	}
	if c.WS.Addr != "" {
		if _, err := protocol.NewCodec(c.WS.Codec); err != nil {
			errs = append(errs, fmt.Errorf("ws.codec: %w", err))
		}
		if (c.WS.TLSCertFile == "") != (c.WS.TLSKeyFile == "") {
			errs = append(errs, errors.New("ws.tls_cert_file and ws.tls_key_file must be set together"))
		}
	}
	if c.MaxBodyLen <= 0 {
		errs = append(errs, errors.New("max_body_len must be positive"))
	}
	for name, t := range map[string]string{
		"topics.broadcast":  c.Topics.Broadcast,
		"topics.fullserver": c.Topics.FullServer,
		"topics.kicked":     c.Topics.Kicked,
		"topics.event":      c.Topics.Event,
	} {
		if t == "" {
			errs = append(errs, fmt.Errorf("%s is empty", name))
		}
	}
	return errors.Join(errs...)
}

// ConsumerGroup returns kafka.group_id or a per-instance unique group, so
// that every gateway receives every broadcast.
func (c *Config) ConsumerGroup() string {
	if c.Kafka.GroupID != "" {
		return c.Kafka.GroupID
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "gateway-" + host + "-" + hex.EncodeToString(b[:])
}
