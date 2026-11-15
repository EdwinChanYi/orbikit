// Package discovery registers this process in etcd and resolves other
// processes by service name. A target of "etcd:///core" reaches every live
// instance registered under that name.
package discovery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	"google.golang.org/grpc/resolver"

	"github.com/EdwinChanYi/orbikit/pkg/log"
)

// Scheme is the gRPC target scheme handled by this package.
const Scheme = "etcd"

// DefaultPrefix is the etcd key prefix used when Config.Prefix is empty.
const DefaultPrefix = "/orbikit/services"

// Config is the etcd cluster used for registration and discovery.
// An empty Endpoints list disables both.
type Config struct {
	Endpoints   []string      `yaml:"endpoints"`
	Username    string        `yaml:"username"`
	Password    string        `yaml:"password"`
	DialTimeout time.Duration `yaml:"dial_timeout"`
	// Prefix is the key root. Instances of service "core" live at
	// Prefix/core/<id>.
	Prefix string `yaml:"prefix"`
	// LeaseTTL is how long a registration outlives a crashed process.
	LeaseTTL time.Duration `yaml:"lease_ttl"`
}

// Enabled reports whether service discovery is configured.
func (c Config) Enabled() bool {
	return len(c.Endpoints) > 0
}

func (c Config) withDefaults() Config {
	if c.DialTimeout <= 0 {
		c.DialTimeout = 5 * time.Second
	}
	if c.Prefix == "" {
		c.Prefix = DefaultPrefix
	}
	c.Prefix = strings.TrimRight(c.Prefix, "/")
	if c.LeaseTTL <= 0 {
		c.LeaseTTL = 10 * time.Second
	}
	return c
}

// RegisterConfig is how this process publishes itself.
type RegisterConfig struct {
	// Name is the service name callers use in etcd:///<name>.
	Name string `yaml:"name"`
	// AdvertiseAddr is the host:port other processes dial. Empty selects the
	// local address of the route toward the first etcd endpoint, which gives
	// each replica its own address.
	AdvertiseAddr string `yaml:"advertise_addr"`
}

// UsesEtcd reports whether target is an etcd discovery target.
func UsesEtcd(target string) bool {
	return strings.HasPrefix(target, Scheme+"://")
}

// Registry is one process's connection to etcd.
type Registry struct {
	cfg      Config
	backend  backend
	instance string

	mu      sync.Mutex
	service string
	addr    string
	lease   clientv3.LeaseID
}

// Open connects when cfg is enabled. A disabled config returns a nil Registry
// and a nil error; callers can pass that nil into Bind and Dial.
func Open(cfg Config) (*Registry, error) {
	if !cfg.Enabled() {
		return nil, nil
	}
	return New(cfg)
}

// New connects to etcd. cfg.Endpoints must be non-empty.
func New(cfg Config) (*Registry, error) {
	if !cfg.Enabled() {
		return nil, errors.New("etcd.endpoints is required")
	}
	cfg = cfg.withDefaults()
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return nil, err
	}
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   cfg.Endpoints,
		DialTimeout: cfg.DialTimeout,
		Username:    cfg.Username,
		Password:    cfg.Password,
		Logger:      zap.NewNop(),
	})
	if err != nil {
		return nil, fmt.Errorf("etcd: %w", err)
	}
	return newRegistry(cfg, &etcdBackend{c: cli}, hex.EncodeToString(buf[:])), nil
}

func newRegistry(cfg Config, b backend, instance string) *Registry {
	return &Registry{cfg: cfg.withDefaults(), backend: b, instance: instance}
}

// Close releases the etcd client. It is safe to call on a nil Registry.
func (r *Registry) Close() error {
	if r == nil || r.backend == nil {
		return nil
	}
	return r.backend.Close()
}

// Builder is the gRPC resolver for targets served by this registry.
func (r *Registry) Builder() resolver.Builder {
	if r == nil {
		return nil
	}
	return &builder{backend: r.backend, prefix: r.cfg.Prefix}
}

// Bind registers this process under rc.Name when reg is configured.
// listen is the address actually bound, used to fill in the port.
func Bind(ctx context.Context, reg *Registry, listen net.Addr, rc RegisterConfig) error {
	if reg == nil {
		return nil
	}
	if rc.Name == "" {
		return errors.New("register.name is required when etcd.endpoints is set")
	}
	addr, err := pickAdvertise(listen.String(), rc.AdvertiseAddr, func() (string, error) {
		return localIP(reg.cfg.Endpoints[0])
	})
	if err != nil {
		return err
	}
	return reg.Register(ctx, rc.Name, addr)
}

// Register writes addr under the service name and keeps the lease alive until
// ctx is cancelled, then deletes the key.
func (r *Registry) Register(ctx context.Context, service, addr string) error {
	if err := validateService(service); err != nil {
		return err
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("advertise address %q: %w", addr, err)
	}
	r.mu.Lock()
	if r.service != "" {
		r.mu.Unlock()
		return errors.New("service already registered")
	}
	r.service = service
	r.addr = addr
	r.mu.Unlock()
	if err := r.announce(ctx); err != nil {
		return err
	}
	go r.maintain(ctx)
	return nil
}

func (r *Registry) announce(ctx context.Context) error {
	r.mu.Lock()
	service, addr := r.service, r.addr
	r.mu.Unlock()
	ttl := int64(r.cfg.LeaseTTL / time.Second)
	if ttl < 1 {
		return fmt.Errorf("etcd.lease_ttl %s is shorter than 1s", r.cfg.LeaseTTL)
	}
	id, err := r.backend.Grant(ctx, ttl)
	if err != nil {
		return fmt.Errorf("etcd grant: %w", err)
	}
	key := r.cfg.Prefix + "/" + service + "/" + r.instance
	if err := r.backend.Put(ctx, key, addr, id); err != nil {
		_ = r.backend.Revoke(context.Background(), id)
		return fmt.Errorf("etcd put %s: %w", key, err)
	}
	r.mu.Lock()
	old := r.lease
	r.lease = id
	r.mu.Unlock()
	if old != 0 && old != id {
		_ = r.backend.Revoke(context.Background(), old)
	}
	log.L().Info("registered in etcd",
		zap.String("name", service),
		zap.String("addr", addr),
		zap.String("key", key))
	return nil
}

func (r *Registry) maintain(ctx context.Context) {
	defer r.revoke()
	for ctx.Err() == nil {
		ch, err := r.backend.KeepAlive(ctx, r.currentLease())
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.L().Warn("etcd keepalive", zap.Error(err))
			if !sleep(ctx, time.Second) {
				return
			}
			if err := r.announce(ctx); err != nil {
				log.L().Warn("etcd re-register", zap.Error(err))
			}
			continue
		}
		lost := false
		for !lost && ctx.Err() == nil {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-ch:
				if !ok {
					lost = true
				}
			}
		}
		if ctx.Err() != nil {
			return
		}
		log.L().Warn("etcd lease lost", zap.String("name", r.service), zap.String("addr", r.addr))
		if err := r.announce(ctx); err != nil {
			log.L().Warn("etcd re-register", zap.Error(err))
			if !sleep(ctx, time.Second) {
				return
			}
		}
	}
}

func (r *Registry) currentLease() clientv3.LeaseID {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lease
}

func (r *Registry) revoke() {
	r.mu.Lock()
	id := r.lease
	r.lease = 0
	r.mu.Unlock()
	if id == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := r.backend.Revoke(ctx, id); err != nil {
		log.L().Warn("etcd revoke", zap.Error(err))
	}
}

func validateService(service string) error {
	if service == "" || strings.Contains(service, "/") {
		return fmt.Errorf("invalid service name %q", service)
	}
	return nil
}

func pickAdvertise(listen, explicit string, local func() (string, error)) (string, error) {
	if explicit != "" {
		if _, _, err := net.SplitHostPort(explicit); err != nil {
			return "", fmt.Errorf("register.advertise_addr %q: %w", explicit, err)
		}
		return explicit, nil
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("register.advertise_addr is required (listen address %q)", listen)
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		ip, err := local()
		if err != nil {
			return "", fmt.Errorf("register.advertise_addr is required: %w", err)
		}
		host = ip
	}
	return net.JoinHostPort(host, port), nil
}

func localIP(endpoint string) (string, error) {
	addr := strings.TrimPrefix(strings.TrimPrefix(endpoint, "http://"), "https://")
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return "", fmt.Errorf("etcd endpoint %q: %w", endpoint, err)
	}
	conn, err := net.Dial("udp", addr)
	if err != nil {
		return "", fmt.Errorf("detect advertise address via %s: %w", addr, err)
	}
	defer conn.Close()
	ip, _, err := net.SplitHostPort(conn.LocalAddr().String())
	if err != nil {
		return "", err
	}
	return ip, nil
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
