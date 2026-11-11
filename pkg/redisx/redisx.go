// Package redisx creates Redis clients that work with both a single node and a cluster.
package redisx

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Config describes a Redis deployment. More than one address selects cluster mode.
type Config struct {
	Addrs    []string `yaml:"addrs"`
	Password string   `yaml:"password"`
	DB       int      `yaml:"db"`
	PoolSize int      `yaml:"pool_size"`
}

// New connects and pings Redis.
func New(ctx context.Context, cfg Config) (redis.UniversalClient, error) {
	if len(cfg.Addrs) == 0 {
		return nil, fmt.Errorf("redis: no addrs configured")
	}
	c := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs:        cfg.Addrs,
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     cfg.PoolSize,
		DialTimeout:  3 * time.Second,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
	})
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := c.Ping(pingCtx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("redis ping %v: %w", cfg.Addrs, err)
	}
	return c, nil
}
