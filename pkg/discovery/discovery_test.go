package discovery

import (
	"context"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	mvccpb "go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc/resolver"
)

func TestPickAdvertise(t *testing.T) {
	got, err := pickAdvertise("10.1.0.8:9001", "", func() (string, error) {
		t.Fatal("local address should not be queried")
		return "", nil
	})
	if err != nil || got != "10.1.0.8:9001" {
		t.Fatalf("got %q %v", got, err)
	}
	got, err = pickAdvertise("0.0.0.0:9001", "core:9001", nil)
	if err != nil || got != "core:9001" {
		t.Fatalf("explicit got %q %v", got, err)
	}
	got, err = pickAdvertise("[::]:9002", "", func() (string, error) { return "10.2.0.3", nil })
	if err != nil || got != "10.2.0.3:9002" {
		t.Fatalf("wildcard got %q %v", got, err)
	}
	if _, err := pickAdvertise(":9001", "not-a-host-port", nil); err == nil {
		t.Fatal("invalid advertise address should fail")
	}
}

func TestAddressesIgnoreDuplicatesAndGarbage(t *testing.T) {
	resp := &clientv3.GetResponse{Kvs: []*mvccpb.KeyValue{
		{Key: []byte("b"), Value: []byte("10.0.0.2:9001")},
		{Key: []byte("a"), Value: []byte("10.0.0.1:9001")},
		{Key: []byte("c"), Value: []byte("10.0.0.2:9001")},
		{Key: []byte("d"), Value: []byte("not-an-address")},
	}}
	got := addressesOf(resp)
	want := []string{"10.0.0.1:9001", "10.0.0.2:9001"}
	if !slices.Equal(got, want) {
		t.Fatalf("addresses = %v", got)
	}
}

func TestRegisterAndResolve(t *testing.T) {
	mem := newMem()
	cfg := Config{Endpoints: []string{"etcd:2379"}, Prefix: DefaultPrefix, LeaseTTL: time.Second}.withDefaults()
	first := newRegistry(cfg, mem, "one")
	second := newRegistry(cfg, mem, "two")

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	if err := first.Register(ctx1, "core", "10.0.0.1:9001"); err != nil {
		t.Fatal(err)
	}
	cc := &fakeCC{ch: make(chan struct{}, 8)}
	res, err := first.Builder().Build(mustTarget(t, "etcd:///core"), cc, resolver.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Close()
	waitAddrs(t, cc, []string{"10.0.0.1:9001"})

	if err := second.Register(ctx2, "core", "10.0.0.2:9001"); err != nil {
		t.Fatal(err)
	}
	waitAddrs(t, cc, []string{"10.0.0.1:9001", "10.0.0.2:9001"})

	cancel1()
	waitAddrs(t, cc, []string{"10.0.0.2:9001"})

	other, err := second.Builder().Build(mustTarget(t, "etcd:///chatroom"), &fakeCC{ch: make(chan struct{}, 1)}, resolver.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
}

func TestLeaseLossReregisters(t *testing.T) {
	mem := newMem()
	cfg := Config{Endpoints: []string{"etcd:2379"}, LeaseTTL: time.Second}.withDefaults()
	reg := newRegistry(cfg, mem, "one")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := reg.Register(ctx, "core", "10.0.0.1:9001"); err != nil {
		t.Fatal(err)
	}
	old := reg.currentLease()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && mem.keepCount(old) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if mem.keepCount(old) == 0 {
		t.Fatal("keepalive was not started")
	}
	mem.loseKeepAlive(old)
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if id := reg.currentLease(); id != 0 && id != old {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("lease stayed %d", reg.currentLease())
}

func TestBindUsesExplicitAddress(t *testing.T) {
	mem := newMem()
	cfg := Config{Endpoints: []string{"127.0.0.1:1"}, LeaseTTL: time.Second}.withDefaults()
	reg := newRegistry(cfg, mem, "one")
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := Bind(ctx, reg, lis.Addr(), RegisterConfig{Name: "core", AdvertiseAddr: "core:9001"}); err != nil {
		t.Fatal(err)
	}
	if reg.addr != "core:9001" {
		t.Fatalf("addr = %s", reg.addr)
	}
	if err := Bind(ctx, nil, lis.Addr(), RegisterConfig{}); err != nil {
		t.Fatal(err)
	}
}

func mustTarget(t *testing.T, raw string) resolver.Target {
	t.Helper()
	var target resolver.Target
	// grpc parses etcd:///core with an empty host and path /core.
	target.URL.Scheme = Scheme
	target.URL.Path = "/core"
	if raw == "etcd:///chatroom" {
		target.URL.Path = "/chatroom"
	}
	return target
}

func waitAddrs(t *testing.T, cc *fakeCC, want []string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var got []string
	for time.Now().Before(deadline) {
		cc.mu.Lock()
		got = append([]string(nil), cc.addrs...)
		cc.mu.Unlock()
		if slices.Equal(got, want) {
			return
		}
		select {
		case <-cc.ch:
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatalf("addrs = %v, want %v", got, want)
}

type fakeCC struct {
	resolver.ClientConn
	mu    sync.Mutex
	addrs []string
	ch    chan struct{}
}

func (f *fakeCC) UpdateState(state resolver.State) error {
	f.mu.Lock()
	f.addrs = f.addrs[:0]
	for _, addr := range state.Addresses {
		f.addrs = append(f.addrs, addr.Addr)
	}
	f.mu.Unlock()
	select {
	case f.ch <- struct{}{}:
	default:
	}
	return nil
}

func (f *fakeCC) ReportError(err error) {
	panic("resolver error: " + err.Error())
}

type memKeep struct {
	ch     chan *clientv3.LeaseKeepAliveResponse
	mu     sync.Mutex
	closed bool
}

func (k *memKeep) close() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.closed {
		k.closed = true
		close(k.ch)
	}
}

type memWatch struct {
	prefix string
	ch     chan clientv3.WatchResponse
	mu     sync.Mutex
	closed bool
}

func (w *memWatch) send() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	select {
	case w.ch <- clientv3.WatchResponse{}:
	default:
	}
}

func (w *memWatch) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed {
		w.closed = true
		close(w.ch)
	}
}

type memRecord struct {
	value string
	lease clientv3.LeaseID
}

type memBackend struct {
	mu       sync.Mutex
	next     clientv3.LeaseID
	records  map[string]memRecord
	keeps    map[clientv3.LeaseID][]*memKeep
	watchers []*memWatch
}

func newMem() *memBackend {
	return &memBackend{
		records: map[string]memRecord{},
		keeps:   map[clientv3.LeaseID][]*memKeep{},
	}
}

func (m *memBackend) Grant(context.Context, int64) (clientv3.LeaseID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	return m.next, nil
}

func (m *memBackend) Put(_ context.Context, key, val string, id clientv3.LeaseID) error {
	m.mu.Lock()
	m.records[key] = memRecord{value: val, lease: id}
	m.mu.Unlock()
	m.notify(key)
	return nil
}

func (m *memBackend) Revoke(_ context.Context, id clientv3.LeaseID) error {
	m.mu.Lock()
	var keys []string
	for key, rec := range m.records {
		if rec.lease == id {
			keys = append(keys, key)
			delete(m.records, key)
		}
	}
	keeps := m.keeps[id]
	delete(m.keeps, id)
	m.mu.Unlock()
	for _, keep := range keeps {
		keep.close()
	}
	for _, key := range keys {
		m.notify(key)
	}
	return nil
}

func (m *memBackend) KeepAlive(ctx context.Context, id clientv3.LeaseID) (<-chan *clientv3.LeaseKeepAliveResponse, error) {
	keep := &memKeep{ch: make(chan *clientv3.LeaseKeepAliveResponse)}
	m.mu.Lock()
	m.keeps[id] = append(m.keeps[id], keep)
	m.mu.Unlock()
	go func() {
		<-ctx.Done()
		keep.close()
	}()
	return keep.ch, nil
}

func (m *memBackend) keepCount(id clientv3.LeaseID) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.keeps[id])
}

func (m *memBackend) loseKeepAlive(id clientv3.LeaseID) {
	m.mu.Lock()
	keeps := append([]*memKeep(nil), m.keeps[id]...)
	m.mu.Unlock()
	for _, keep := range keeps {
		keep.close()
	}
}

func (m *memBackend) Get(_ context.Context, prefix string) (*clientv3.GetResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	resp := &clientv3.GetResponse{}
	for key, rec := range m.records {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			resp.Kvs = append(resp.Kvs, &mvccpb.KeyValue{Key: []byte(key), Value: []byte(rec.value)})
		}
	}
	return resp, nil
}

func (m *memBackend) Watch(ctx context.Context, prefix string) clientv3.WatchChan {
	w := &memWatch{prefix: prefix, ch: make(chan clientv3.WatchResponse, 32)}
	m.mu.Lock()
	m.watchers = append(m.watchers, w)
	m.mu.Unlock()
	go func() {
		<-ctx.Done()
		m.mu.Lock()
		kept := m.watchers[:0]
		for _, cur := range m.watchers {
			if cur != w {
				kept = append(kept, cur)
			}
		}
		m.watchers = kept
		m.mu.Unlock()
		w.close()
	}()
	return w.ch
}

func (m *memBackend) notify(key string) {
	m.mu.Lock()
	watchers := append([]*memWatch(nil), m.watchers...)
	m.mu.Unlock()
	for _, w := range watchers {
		if len(key) >= len(w.prefix) && key[:len(w.prefix)] == w.prefix {
			w.send()
		}
	}
}

func (m *memBackend) Close() error { return nil }
