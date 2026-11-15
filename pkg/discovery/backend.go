package discovery

import (
	"context"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// backend is the slice of etcd the registrar and resolver use. Tests substitute
// an in-memory implementation.
type backend interface {
	Grant(ctx context.Context, ttl int64) (clientv3.LeaseID, error)
	Put(ctx context.Context, key, val string, id clientv3.LeaseID) error
	Revoke(ctx context.Context, id clientv3.LeaseID) error
	KeepAlive(ctx context.Context, id clientv3.LeaseID) (<-chan *clientv3.LeaseKeepAliveResponse, error)
	Get(ctx context.Context, prefix string) (*clientv3.GetResponse, error)
	Watch(ctx context.Context, prefix string) clientv3.WatchChan
	Close() error
}

type etcdBackend struct {
	c *clientv3.Client
}

func (e *etcdBackend) Grant(ctx context.Context, ttl int64) (clientv3.LeaseID, error) {
	resp, err := e.c.Grant(ctx, ttl)
	if err != nil {
		return 0, err
	}
	return resp.ID, nil
}

func (e *etcdBackend) Put(ctx context.Context, key, val string, id clientv3.LeaseID) error {
	_, err := e.c.Put(ctx, key, val, clientv3.WithLease(id))
	return err
}

func (e *etcdBackend) Revoke(ctx context.Context, id clientv3.LeaseID) error {
	_, err := e.c.Revoke(ctx, id)
	return err
}

func (e *etcdBackend) KeepAlive(ctx context.Context, id clientv3.LeaseID) (<-chan *clientv3.LeaseKeepAliveResponse, error) {
	return e.c.KeepAlive(ctx, id)
}

func (e *etcdBackend) Get(ctx context.Context, prefix string) (*clientv3.GetResponse, error) {
	return e.c.Get(ctx, prefix, clientv3.WithPrefix())
}

func (e *etcdBackend) Watch(ctx context.Context, prefix string) clientv3.WatchChan {
	return e.c.Watch(ctx, prefix, clientv3.WithPrefix())
}

func (e *etcdBackend) Close() error {
	return e.c.Close()
}
