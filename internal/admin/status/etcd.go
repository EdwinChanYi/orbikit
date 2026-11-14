package status

import (
	"context"
	"time"

	pb "go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
)

const defaultPrefix = "/orbikit/services"

// clusterClient is the etcd surface a snapshot reads. Tests pass a fake so
// they never dial a server. *clientv3.Client implements it.
type clusterClient interface {
	Endpoints() []string
	Status(ctx context.Context, endpoint string) (*clientv3.StatusResponse, error)
	MemberList(ctx context.Context) (*clientv3.MemberListResponse, error)
	AlarmList(ctx context.Context) (*clientv3.AlarmResponse, error)
	Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error)
}

var _ clusterClient = (*clientv3.Client)(nil)

type snapshot struct {
	api    clusterClient
	prefix string
	closer func() error
}

// NewSource reads the cluster through an already opened client.
// An empty prefix uses /orbikit/services.
func NewSource(c *clientv3.Client, prefix string) Source {
	if c == nil {
		return newClusterSource(nil, prefix)
	}
	return newClusterSource(c, prefix)
}

// Open dials etcd the same way service discovery does and returns a Source.
// Close releases the client.
func Open(endpoints []string, username, password string, dialTimeout time.Duration, prefix string) (Source, error) {
	if dialTimeout <= 0 {
		dialTimeout = 5 * time.Second
	}
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: dialTimeout,
		Username:    username,
		Password:    password,
		Logger:      zap.NewNop(),
	})
	if err != nil {
		return nil, err
	}
	src := newClusterSource(cli, prefix).(*snapshot)
	src.closer = cli.Close
	return src, nil
}

// Close releases the client opened by Open. It is safe on a Source that does
// not own a client.
func Close(src Source) error {
	s, ok := src.(*snapshot)
	if !ok || s == nil || s.closer == nil {
		return nil
	}
	return s.closer()
}

func newClusterSource(api clusterClient, prefix string) Source {
	if prefix == "" {
		prefix = defaultPrefix
	}
	return &snapshot{api: api, prefix: prefix}
}

func (s *snapshot) Cluster(ctx context.Context) (Cluster, error) {
	if s == nil || s.api == nil {
		return Interpret("", 0, nil, nil, ErrEtcd), nil
	}
	endpoints := s.api.Endpoints()
	if len(endpoints) == 0 || endpoints[0] == "" {
		return Interpret("", 0, nil, nil, ErrEtcd), nil
	}
	st, err := s.api.Status(ctx, endpoints[0])
	if err != nil || st == nil {
		return Interpret("", 0, nil, nil, ErrEtcd), nil
	}
	members, err := s.api.MemberList(ctx)
	if err != nil || members == nil {
		return Interpret("", 0, nil, nil, ErrEtcd), nil
	}
	alarms, err := s.api.AlarmList(ctx)
	if err != nil || alarms == nil {
		return Interpret("", 0, nil, nil, ErrEtcd), nil
	}
	resp, err := s.api.Get(ctx, s.prefix, clientv3.WithPrefix())
	if err != nil || resp == nil {
		return Interpret("", 0, nil, nil, ErrEtcd), nil
	}
	return Interpret(leaderName(st.Leader, members), st.DbSize, alarmNames(alarms), instancesOf(s.prefix, resp), nil), nil
}

func leaderName(id uint64, members *clientv3.MemberListResponse) string {
	if id == 0 || members == nil {
		return ""
	}
	for _, member := range members.Members {
		if member != nil && member.ID == id {
			return member.Name
		}
	}
	return ""
}

func alarmNames(resp *clientv3.AlarmResponse) []string {
	if resp == nil {
		return nil
	}
	var names []string
	for _, alarm := range resp.Alarms {
		if alarm == nil || alarm.Alarm == pb.AlarmType_NONE {
			continue
		}
		names = append(names, alarm.Alarm.String())
	}
	return names
}

func instancesOf(prefix string, resp *clientv3.GetResponse) []Instance {
	out := make([]Instance, 0)
	if resp == nil {
		return out
	}
	for _, kv := range resp.Kvs {
		if kv == nil {
			continue
		}
		instance, ok := ParseInstanceKey(prefix, string(kv.Key))
		if !ok {
			continue
		}
		instance.Addr = string(kv.Value)
		out = append(out, instance)
	}
	return out
}
