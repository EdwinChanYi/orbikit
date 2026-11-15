package discovery

import (
	"context"
	"net"
	"slices"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	"google.golang.org/grpc/resolver"

	"github.com/EdwinChanYi/orbikit/pkg/log"
)

type builder struct {
	backend backend
	prefix  string
}

func (b *builder) Scheme() string { return Scheme }

func (b *builder) Build(target resolver.Target, cc resolver.ClientConn, _ resolver.BuildOptions) (resolver.Resolver, error) {
	service := target.Endpoint()
	if err := validateService(service); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &etcdResolver{
		ctx:     ctx,
		cancel:  cancel,
		cc:      cc,
		backend: b.backend,
		prefix:  b.prefix + "/" + service + "/",
		service: service,
	}
	go r.watch()
	return r, nil
}

type etcdResolver struct {
	ctx     context.Context
	cancel  context.CancelFunc
	cc      resolver.ClientConn
	backend backend
	prefix  string
	service string

	mu   sync.Mutex
	seen bool
	cur  []string
}

func (r *etcdResolver) ResolveNow(resolver.ResolveNowOptions) {
	if err := r.refresh(); err != nil {
		r.cc.ReportError(err)
	}
}

func (r *etcdResolver) Close() { r.cancel() }

func (r *etcdResolver) watch() {
	for r.ctx.Err() == nil {
		if err := r.refresh(); err != nil {
			r.cc.ReportError(err)
		}
		ch := r.backend.Watch(r.ctx, r.prefix)
		for wr := range ch {
			if r.ctx.Err() != nil {
				return
			}
			if err := wr.Err(); err != nil {
				r.cc.ReportError(err)
				break
			}
			if err := r.refresh(); err != nil {
				r.cc.ReportError(err)
			}
		}
		if !sleep(r.ctx, time.Second) {
			return
		}
	}
}

func (r *etcdResolver) refresh() error {
	if r.ctx.Err() != nil {
		return nil
	}
	resp, err := r.backend.Get(r.ctx, r.prefix)
	if err != nil {
		if r.ctx.Err() != nil {
			return nil
		}
		return err
	}
	addrs := addressesOf(resp)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen && slices.Equal(r.cur, addrs) {
		return nil
	}
	r.seen = true
	r.cur = append([]string(nil), addrs...)
	log.L().Info("service endpoints changed", zap.String("name", r.service), zap.Strings("addrs", addrs))
	out := make([]resolver.Address, len(addrs))
	for i, addr := range addrs {
		out[i] = resolver.Address{Addr: addr}
	}
	return r.cc.UpdateState(resolver.State{Addresses: out})
}

func addressesOf(resp *clientv3.GetResponse) []string {
	if resp == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(resp.Kvs))
	out := make([]string, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		addr := string(kv.Value)
		if _, _, err := net.SplitHostPort(addr); err != nil {
			continue
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		out = append(out, addr)
	}
	slices.Sort(out)
	return out
}
