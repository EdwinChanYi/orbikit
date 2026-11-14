package logic

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EdwinChanYi/orbikit/internal/core/store"
)

func TestKeysFormat(t *testing.T) {
	cases := map[string]string{
		LoginTokenKey("app1", 42):  "{CORE_SERVICE}:INFO_2_LOGIN_TOKEN:APP_ID:app1:UID:42",
		LoginTokenInfoKey("tok"):   "{CORE_SERVICE}:LOGIN_TOKEN_2_INFO:LOGIN_TOKEN:tok",
		RestrainUserKey("app1", 7): "{CORE_SERVICE}:RESTRAIN_USER:APP_ID:app1:UID:7",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("key = %q, want %q", got, want)
		}
	}
}

func TestNewLoginToken(t *testing.T) {
	tok := newLoginToken("app", 1)
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(tok) {
		t.Fatalf("token %q is not 64 lower hex chars", tok)
	}
	if tok == newLoginToken("app", 1) {
		t.Fatal("tokens must be random")
	}
}

func TestParseTokenInfo(t *testing.T) {
	appID, uid, ok, err := parseTokenInfo(tokenInfoValue("app", 123))
	if err != nil || !ok || appID != "app" || uid != 123 {
		t.Fatalf("got %q %d %v %v", appID, uid, ok, err)
	}
	if _, _, ok, err := parseTokenInfo("noseparator"); ok || err != nil {
		t.Fatal("value without separator is malformed but not an error")
	}
	if _, _, ok, err := parseTokenInfo("app,abc"); ok || err == nil {
		t.Fatal("non-numeric uid must be an error")
	}
}

func TestTTLCacheExpiry(t *testing.T) {
	c := newTTLCache[int]()
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }

	c.Set("k", 1, time.Second)
	if v, ok := c.Get("k"); !ok || v != 1 {
		t.Fatal("value should be cached")
	}
	now = now.Add(time.Second)
	if _, ok := c.Get("k"); ok {
		t.Fatal("value should expire")
	}

	loads := 0
	load := func() (int, time.Duration) { loads++; return 2, time.Minute }
	first, second := c.GetOrLoad("k", load), c.GetOrLoad("k", load)
	if first != 2 || second != 2 || loads != 1 {
		t.Fatalf("loads = %d, want 1", loads)
	}
	c.Delete("k")
	c.GetOrLoad("k", load)
	if loads != 2 {
		t.Fatal("delete must force reload")
	}
}

func TestTTLCacheSingleLoad(t *testing.T) {
	c := newTTLCache[int]()
	var loads atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.GetOrLoad("k", func() (int, time.Duration) {
				loads.Add(1)
				time.Sleep(10 * time.Millisecond)
				return 1, time.Minute
			})
		}()
	}
	wg.Wait()
	if loads.Load() != 1 {
		t.Fatalf("loads = %d, want 1", loads.Load())
	}
}

type fakeAppStore struct {
	mu      sync.Mutex
	apps    map[string]*store.AppInfo
	err     error
	queries int
}

func (f *fakeAppStore) HasApp(ctx context.Context, appID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries++
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if f.err != nil {
		return false, f.err
	}
	_, ok := f.apps[appID]
	return ok, nil
}

func (f *fakeAppStore) ListApps(context.Context) ([]*store.AppInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ret []*store.AppInfo
	for _, a := range f.apps {
		ret = append(ret, a)
	}
	return ret, f.err
}

func (f *fakeAppStore) AppByAppID(_ context.Context, appID string) (*store.AppInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries++
	if f.err != nil {
		return nil, f.err
	}
	return f.apps[appID], nil
}

func (f *fakeAppStore) GdAppByAppID(context.Context, string) (*store.GdAppInfo, error) {
	return nil, nil
}

func (f *fakeAppStore) PcAppByAppID(context.Context, string) (*store.PcAppInfo, error) {
	return nil, nil
}

func TestSetCacheExpireAppliesToLaterWrites(t *testing.T) {
	fs := &fakeAppStore{apps: map[string]*store.AppInfo{"a": {AppId: "a", Qps: 1}}}
	svc := NewAppService(fs, time.Hour)
	now := time.Unix(0, 0)
	svc.apps.now = func() time.Time { return now }
	svc.SetCacheExpire(time.Millisecond)
	if app := svc.App(context.Background(), "a"); app == nil || app.Qps != 1 {
		t.Fatal("first load failed")
	}
	fs.apps["a"] = &store.AppInfo{AppId: "a", Qps: 2}
	now = now.Add(2 * time.Millisecond)
	if app := svc.App(context.Background(), "a"); app == nil || app.Qps != 2 {
		t.Fatalf("qps = %+v, want reload after the new ttl", app)
	}
}

func TestAppServiceCaching(t *testing.T) {
	ctx := context.Background()
	fs := &fakeAppStore{apps: map[string]*store.AppInfo{"a": {AppId: "a", Qps: 5}}}
	svc := NewAppService(fs, time.Minute)

	validOnce, validTwice := svc.IsAppValid(ctx, "a"), svc.IsAppValid(ctx, "a")
	if !validOnce || !validTwice {
		t.Fatal("app a should be valid")
	}
	missingOnce, missingTwice := svc.IsAppValid(ctx, "missing"), svc.IsAppValid(ctx, "missing")
	if missingOnce || missingTwice {
		t.Fatal("missing app should be invalid")
	}
	if fs.queries != 2 {
		t.Fatalf("queries = %d, want 2 (results must be cached)", fs.queries)
	}

	if app := svc.App(ctx, "a"); app == nil || app.Qps != 5 {
		t.Fatal("App should return the stored app")
	}
	if svc.App(ctx, "missing") != nil {
		t.Fatal("missing app should be nil")
	}

	fs.apps["a"] = &store.AppInfo{AppId: "a", Qps: 9}
	if svc.App(ctx, "a").Qps != 5 {
		t.Fatal("cached value expected before invalidation")
	}
	svc.InvalidateApp("a")
	if svc.App(ctx, "a").Qps != 9 {
		t.Fatal("invalidation must reload")
	}
}

func TestAppServiceErrorIsNotValid(t *testing.T) {
	fs := &fakeAppStore{err: errors.New("db down")}
	svc := NewAppService(fs, time.Minute)
	if svc.IsAppValid(context.Background(), "a") {
		t.Fatal("lookup failure must report invalid")
	}
	if svc.App(context.Background(), "a") != nil {
		t.Fatal("lookup failure must return nil")
	}
	if got := svc.AllAppQPS(context.Background()); got != nil {
		t.Fatalf("AllAppQPS on failure = %v, want nil", got)
	}
}

func TestAppServiceIgnoresCallerCancellation(t *testing.T) {
	fs := &fakeAppStore{apps: map[string]*store.AppInfo{"a": {AppId: "a"}}}
	svc := NewAppService(fs, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !svc.IsAppValid(ctx, "a") {
		t.Fatal("a cancelled caller must not poison the shared cache")
	}
}

func TestAllAppQPSWarmsCache(t *testing.T) {
	fs := &fakeAppStore{apps: map[string]*store.AppInfo{"a": {AppId: "a", Qps: 1, TextQps: 2, CustomQps: 3}}}
	svc := NewAppService(fs, time.Minute)
	qps := svc.AllAppQPS(context.Background())
	if len(qps) != 1 || qps[0].AppId != "a" || qps[0].Qps != 1 || qps[0].TextQps != 2 || qps[0].CustomQps != 3 {
		t.Fatalf("unexpected qps %v", qps)
	}
	before := fs.queries
	if svc.App(context.Background(), "a") == nil || fs.queries != before {
		t.Fatal("App should be served from the warmed cache")
	}
}
