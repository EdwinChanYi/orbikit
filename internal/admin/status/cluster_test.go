package status

import (
	"context"
	"errors"
	"reflect"
	"testing"

	pb "go.etcd.io/etcd/api/v3/etcdserverpb"
	mvccpb "go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type fakeSource struct {
	snap Cluster
	err  error
}

func (f fakeSource) Cluster(context.Context) (Cluster, error) {
	return f.snap, f.err
}

func TestFakeSourceReturnsSnapshot(t *testing.T) {
	want := Cluster{
		Status:    "ok",
		Instances: []Instance{{Service: "core", ID: "abc", Addr: "10.0.0.1:9001"}},
	}
	var src Source = fakeSource{snap: want}
	got, err := src.Cluster(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %#v", got)
	}
}

func TestInterpretFaultOnQueryError(t *testing.T) {
	instances := []Instance{{Service: "core", ID: "abc", Addr: "10.0.0.1:9001"}}
	got := Interpret("etcd-1", 42, []string{"NOSPACE"}, instances, errors.New("dial tcp: connection refused"))
	if got.Status != "fault" {
		t.Fatalf("status = %q", got.Status)
	}
	if got.Instances != nil {
		t.Fatalf("instances = %#v, want nil", got.Instances)
	}
	if got.Error == "" {
		t.Fatal("error is empty")
	}
}

func TestInterpretOKReturnsInstances(t *testing.T) {
	instances := []Instance{{Service: "core", ID: "abc", Addr: "10.0.0.1:9001"}}
	got := Interpret("etcd-1", 42, nil, instances, nil)
	if got.Status != "ok" {
		t.Fatalf("status = %q", got.Status)
	}
	if got.Leader != "etcd-1" || got.DBSize != 42 {
		t.Fatalf("leader/size = %q %d", got.Leader, got.DBSize)
	}
	if !reflect.DeepEqual(got.Instances, instances) {
		t.Fatalf("instances = %#v", got.Instances)
	}
	if got.Error != "" {
		t.Fatalf("error = %q", got.Error)
	}
}

func TestInterpretEmptyInstancesStayEmpty(t *testing.T) {
	got := Interpret("etcd-1", 1, nil, []Instance{}, nil)
	if got.Status != "ok" {
		t.Fatalf("status = %q", got.Status)
	}
	if got.Instances == nil {
		t.Fatal("empty instances became nil")
	}
	if len(got.Instances) != 0 {
		t.Fatalf("len(instances) = %d", len(got.Instances))
	}
}

func TestInterpretDegradedOnNOSPACE(t *testing.T) {
	instances := []Instance{{Service: "core", ID: "abc", Addr: "10.0.0.1:9001"}}
	got := Interpret("etcd-1", 42, []string{"NOSPACE"}, instances, nil)
	if got.Status != "degraded" {
		t.Fatalf("status = %q", got.Status)
	}
	if !reflect.DeepEqual(got.Instances, instances) {
		t.Fatalf("instances = %#v", got.Instances)
	}
	if len(got.Alarms) != 1 || got.Alarms[0] != "NOSPACE" {
		t.Fatalf("alarms = %#v", got.Alarms)
	}
}

func TestParseInstanceKey(t *testing.T) {
	got, ok := ParseInstanceKey("/orbikit/services", "/orbikit/services/core/abc")
	if !ok {
		t.Fatal("key not parsed")
	}
	if got.Service != "core" || got.ID != "abc" || got.Addr != "" {
		t.Fatalf("instance = %+v", got)
	}
	if _, ok := ParseInstanceKey("/orbikit/services/", "/orbikit/services/core/abc"); !ok {
		t.Fatal("trailing slash on prefix rejected")
	}
	if _, ok := ParseInstanceKey("/orbikit/services", "/other/core/abc"); ok {
		t.Fatal("wrong prefix parsed")
	}
	if _, ok := ParseInstanceKey("/orbikit/services", "/orbikit/services/core"); ok {
		t.Fatal("missing id parsed")
	}
	if _, ok := ParseInstanceKey("/orbikit/services", "/orbikit/services/core/abc/extra"); ok {
		t.Fatal("extra segment parsed")
	}
}

type fakeCluster struct {
	endpoints []string
	status    *clientv3.StatusResponse
	statusErr error
	members   *clientv3.MemberListResponse
	memberErr error
	alarms    *clientv3.AlarmResponse
	alarmErr  error
	kvs       *clientv3.GetResponse
	getErr    error

	calls    []string
	endpoint string
	key      string
	opts     []clientv3.OpOption
}

func (f *fakeCluster) Endpoints() []string { return f.endpoints }

func (f *fakeCluster) Status(_ context.Context, endpoint string) (*clientv3.StatusResponse, error) {
	f.calls = append(f.calls, "status")
	f.endpoint = endpoint
	return f.status, f.statusErr
}

func (f *fakeCluster) MemberList(context.Context) (*clientv3.MemberListResponse, error) {
	f.calls = append(f.calls, "members")
	return f.members, f.memberErr
}

func (f *fakeCluster) AlarmList(context.Context) (*clientv3.AlarmResponse, error) {
	f.calls = append(f.calls, "alarms")
	return f.alarms, f.alarmErr
}

func (f *fakeCluster) Get(_ context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	f.calls = append(f.calls, "get")
	f.key = key
	f.opts = opts
	return f.kvs, f.getErr
}

func TestEtcdSourceReadsSnapshot(t *testing.T) {
	api := &fakeCluster{
		endpoints: []string{"127.0.0.1:2379"},
		status:    &clientv3.StatusResponse{Leader: 7, DbSize: 128},
		members: &clientv3.MemberListResponse{Members: []*pb.Member{
			{ID: 8, Name: "etcd-b"},
			{ID: 7, Name: "etcd-a"},
		}},
		alarms: &clientv3.AlarmResponse{Alarms: []*pb.AlarmMember{
			{Alarm: pb.AlarmType_NONE},
			{Alarm: pb.AlarmType_NOSPACE},
			{Alarm: pb.AlarmType_CORRUPT},
		}},
		kvs: &clientv3.GetResponse{Kvs: []*mvccpb.KeyValue{
			{Key: []byte("/orbikit/services/chatroom/id-2"), Value: []byte("10.0.0.2:9002")},
			{Key: []byte("/orbikit/services/core"), Value: []byte("ignored")},
			{Key: []byte("/orbikit/services/core/abc"), Value: []byte("10.0.0.1:9001")},
			{Key: []byte("/orbikit/services/core/abc/extra"), Value: []byte("ignored")},
		}},
	}
	src := newClusterSource(api, "/orbikit/services")
	got, err := src.Cluster(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(api.calls, []string{"status", "members", "alarms", "get"}) {
		t.Fatalf("calls = %v", api.calls)
	}
	if api.endpoint != "127.0.0.1:2379" {
		t.Fatalf("status endpoint = %q", api.endpoint)
	}
	if api.key != "/orbikit/services" {
		t.Fatalf("get key = %q", api.key)
	}
	op := clientv3.OpGet(api.key, api.opts...)
	if string(op.RangeBytes()) != clientv3.GetPrefixRangeEnd(api.key) {
		t.Fatal("get was not a prefix read")
	}
	want := Cluster{
		Status: "degraded",
		Leader: "etcd-a",
		DBSize: 128,
		Alarms: []string{"NOSPACE", "CORRUPT"},
		Instances: []Instance{
			{Service: "chatroom", ID: "id-2", Addr: "10.0.0.2:9002"},
			{Service: "core", ID: "abc", Addr: "10.0.0.1:9001"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %#v", got)
	}
}

func TestEtcdSourceDefaultsPrefix(t *testing.T) {
	api := readyCluster()
	if _, err := newClusterSource(api, "").Cluster(context.Background()); err != nil {
		t.Fatal(err)
	}
	if api.key != "/orbikit/services" {
		t.Fatalf("get key = %q", api.key)
	}
}

func TestEtcdSourceEmptyKeys(t *testing.T) {
	api := &fakeCluster{
		endpoints: []string{"127.0.0.1:2379"},
		status:    &clientv3.StatusResponse{Leader: 7, DbSize: 4},
		members:   &clientv3.MemberListResponse{Members: []*pb.Member{{ID: 7, Name: "etcd-a"}}},
		alarms:    &clientv3.AlarmResponse{},
		kvs:       &clientv3.GetResponse{},
	}
	got, err := newClusterSource(api, "/orbikit/services").Cluster(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "ok" || got.Instances == nil || len(got.Instances) != 0 {
		t.Fatalf("snapshot = %#v", got)
	}
}

func TestEtcdSourceQueryFailure(t *testing.T) {
	steps := []struct {
		name string
		fail func(*fakeCluster)
		want []string
	}{
		{"status", func(f *fakeCluster) { f.statusErr = errors.New("boom") }, []string{"status"}},
		{"members", func(f *fakeCluster) { f.memberErr = errors.New("boom") }, []string{"status", "members"}},
		{"alarms", func(f *fakeCluster) { f.alarmErr = errors.New("boom") }, []string{"status", "members", "alarms"}},
		{"get", func(f *fakeCluster) { f.getErr = errors.New("boom") }, []string{"status", "members", "alarms", "get"}},
		{"no endpoints", func(f *fakeCluster) { f.endpoints = nil }, nil},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			api := readyCluster()
			step.fail(api)
			got, err := newClusterSource(api, "/orbikit/services").Cluster(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != "fault" || got.Instances != nil || got.Error != ErrEtcd.Error() {
				t.Fatalf("snapshot = %#v", got)
			}
			if got.Leader != "" || got.DBSize != 0 || got.Alarms != nil {
				t.Fatalf("partial snapshot = %#v", got)
			}
			if !reflect.DeepEqual(api.calls, step.want) {
				t.Fatalf("calls = %v", api.calls)
			}
		})
	}
}

func readyCluster() *fakeCluster {
	return &fakeCluster{
		endpoints: []string{"127.0.0.1:2379", "127.0.0.1:2380"},
		status:    &clientv3.StatusResponse{Leader: 7, DbSize: 4},
		members:   &clientv3.MemberListResponse{Members: []*pb.Member{{ID: 7, Name: "etcd-a"}}},
		alarms:    &clientv3.AlarmResponse{Alarms: []*pb.AlarmMember{{Alarm: pb.AlarmType_NONE}}},
		kvs: &clientv3.GetResponse{Kvs: []*mvccpb.KeyValue{
			{Key: []byte("/orbikit/services/core/abc"), Value: []byte("10.0.0.1:9001")},
		}},
	}
}
