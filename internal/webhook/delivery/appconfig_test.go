package delivery

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSource struct {
	calls atomic.Int32
	mu    sync.Mutex
	cfg   map[string]*AppConfig
	err   error
}

func (f *fakeSource) AppConfig(_ context.Context, appID string) (*AppConfig, error) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	c, ok := f.cfg[appID]
	if !ok {
		return nil, ErrAppNotFound
	}
	return c, nil
}

func TestAppCacheTTL(t *testing.T) {
	src := &fakeSource{cfg: map[string]*AppConfig{"a": {Secret: "s1"}}}
	c := NewAppCache(src, time.Minute, 10*time.Second)
	now := time.Unix(0, 0)
	c.now = func() time.Time { return now }
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if cfg, err := c.Get(ctx, "a"); err != nil || cfg.Secret != "s1" {
			t.Fatalf("Get = %v, %v", cfg, err)
		}
	}
	if src.calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", src.calls.Load())
	}

	src.cfg["a"] = &AppConfig{Secret: "s2"}
	now = now.Add(time.Minute)
	if cfg, _ := c.Get(ctx, "a"); cfg.Secret != "s2" {
		t.Fatal("expired entry must reload")
	}
}

func TestAppCacheNotFoundIsCached(t *testing.T) {
	src := &fakeSource{cfg: map[string]*AppConfig{}}
	c := NewAppCache(src, time.Minute, 10*time.Second)
	now := time.Unix(0, 0)
	c.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if cfg, err := c.Get(context.Background(), "x"); cfg != nil || err != nil {
			t.Fatalf("Get = %v, %v; want nil, nil", cfg, err)
		}
	}
	if src.calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", src.calls.Load())
	}
	now = now.Add(10 * time.Second)
	_, _ = c.Get(context.Background(), "x")
	if src.calls.Load() != 2 {
		t.Fatal("negative entry must expire after not_found_expire")
	}
}

func TestAppCacheErrorsAreNotCached(t *testing.T) {
	src := &fakeSource{err: errors.New("core down")}
	c := NewAppCache(src, time.Minute, time.Minute)
	if _, err := c.Get(context.Background(), "a"); err == nil {
		t.Fatal("error expected")
	}
	src.err = nil
	src.cfg = map[string]*AppConfig{"a": {Secret: "s"}}
	if cfg, err := c.Get(context.Background(), "a"); err != nil || cfg.Secret != "s" {
		t.Fatalf("Get after recovery = %v, %v", cfg, err)
	}
}

func TestAppCachePutOverrides(t *testing.T) {
	src := &fakeSource{cfg: map[string]*AppConfig{"a": {Secret: "old"}}}
	c := NewAppCache(src, time.Minute, time.Minute)
	_, _ = c.Get(context.Background(), "a")
	c.Put("a", &AppConfig{Secret: "new"})
	if cfg, _ := c.Get(context.Background(), "a"); cfg.Secret != "new" {
		t.Fatal("Put must replace the cached config")
	}
	c.Put("b", &AppConfig{Secret: "pushed"})
	if cfg, _ := c.Get(context.Background(), "b"); cfg.Secret != "pushed" || src.calls.Load() != 1 {
		t.Fatal("pushed config must be served without asking core")
	}
}
