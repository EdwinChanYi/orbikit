package logic

import (
	"hash/fnv"
	"sync"
	"time"
)

const lockStripes = 64

// ttlCache is a small expiring cache with per-key load de-duplication.
type ttlCache[V any] struct {
	mu      sync.RWMutex
	entries map[string]ttlEntry[V]
	locks   [lockStripes]sync.Mutex
	now     func() time.Time
}

type ttlEntry[V any] struct {
	val      V
	expireAt time.Time
}

func newTTLCache[V any]() *ttlCache[V] {
	return &ttlCache[V]{entries: make(map[string]ttlEntry[V]), now: time.Now}
}

// Get returns a cached, unexpired value.
func (c *ttlCache[V]) Get(key string) (V, bool) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || !c.now().Before(e.expireAt) {
		var zero V
		return zero, false
	}
	return e.val, true
}

// Set stores v for ttl.
func (c *ttlCache[V]) Set(key string, v V, ttl time.Duration) {
	c.mu.Lock()
	c.entries[key] = ttlEntry[V]{val: v, expireAt: c.now().Add(ttl)}
	c.mu.Unlock()
}

// Delete evicts key.
func (c *ttlCache[V]) Delete(key string) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

// GetOrLoad returns the cached value or calls load once per key across
// concurrent callers. load returns the value and how long to keep it.
func (c *ttlCache[V]) GetOrLoad(key string, load func() (V, time.Duration)) V {
	if v, ok := c.Get(key); ok {
		return v
	}
	l := &c.locks[stripe(key)]
	l.Lock()
	defer l.Unlock()
	if v, ok := c.Get(key); ok {
		return v
	}
	v, ttl := load()
	c.Set(key, v, ttl)
	return v
}

func stripe(key string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return h.Sum32() % lockStripes
}
