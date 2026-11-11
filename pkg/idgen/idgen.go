// Package idgen generates cluster-unique 64-bit IDs with sonyflake. Each
// process leases a machine ID in Redis so replicas never share one.
package idgen

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sony/sonyflake"
	"go.uber.org/zap"

	"github.com/yourname/roomkit/pkg/log"
)

const (
	leaseTTL     = 10 * time.Minute
	renewEvery   = 3 * time.Minute
	maxAttempts  = 64
	leaseKeyBase = "roomkit:machine_id:"
)

// Generator issues IDs until Close is called.
type Generator struct {
	sf        *sonyflake.Sonyflake
	machineID uint16
	rdb       redis.UniversalClient
	key       string
	cancel    context.CancelFunc
}

// New leases a machine ID scoped to service and starts renewing it.
func New(ctx context.Context, rdb redis.UniversalClient, service string) (*Generator, error) {
	var id uint16
	var key string
	for i := 0; ; i++ {
		if i == maxAttempts {
			return nil, errors.New("idgen: no free machine id")
		}
		id = uint16(rand.UintN(1 << 16))
		key = fmt.Sprintf("%s%s:%d", leaseKeyBase, service, id)
		ok, err := rdb.SetNX(ctx, key, 1, leaseTTL).Result()
		if err != nil {
			return nil, fmt.Errorf("idgen: lease machine id: %w", err)
		}
		if ok {
			break
		}
	}

	sf, err := sonyflake.New(sonyflake.Settings{
		StartTime: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		MachineID: func() (uint16, error) { return id, nil },
	})
	if err != nil {
		return nil, fmt.Errorf("idgen: %w", err)
	}

	renewCtx, cancel := context.WithCancel(context.Background())
	g := &Generator{sf: sf, machineID: id, rdb: rdb, key: key, cancel: cancel}
	go g.renew(renewCtx)
	return g, nil
}

// MachineID returns the leased machine ID.
func (g *Generator) MachineID() uint16 { return g.machineID }

// Next returns a new unique ID.
func (g *Generator) Next() uint64 {
	id, err := g.sf.NextID()
	if err != nil {
		// Only happens after sonyflake's ~174 year lifetime.
		panic(err)
	}
	return id
}

// Close stops renewal and releases the lease.
func (g *Generator) Close() {
	g.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = g.rdb.Del(ctx, g.key).Err()
}

func (g *Generator) renew(ctx context.Context) {
	t := time.NewTicker(renewEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := g.rdb.Expire(ctx, g.key, leaseTTL).Err(); err != nil {
				log.L().Error("idgen: renew machine id lease", zap.Error(err))
			}
		}
	}
}
