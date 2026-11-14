package status

import (
	"context"
	"errors"
	"strings"
)

// Instance is one registered process.
type Instance struct {
	Service string `json:"service"`
	ID      string `json:"id"`
	Addr    string `json:"addr"`
}

// Cluster is the etcd snapshot shown on the cluster page.
type Cluster struct {
	Status    string     `json:"status"`
	Leader    string     `json:"leader,omitempty"`
	DBSize    int64      `json:"db_size,omitempty"`
	Alarms    []string   `json:"alarms,omitempty"`
	Instances []Instance `json:"instances"`
	Error     string     `json:"error,omitempty"`
}

// Source loads one cluster snapshot.
type Source interface {
	Cluster(ctx context.Context) (Cluster, error)
}

// Interpret maps an etcd read to the page status.
func Interpret(leader string, dbSize int64, alarms []string, instances []Instance, queryErr error) Cluster {
	if queryErr != nil {
		return Cluster{Status: "fault", Error: queryErr.Error()}
	}
	out := Cluster{Status: "ok", Leader: leader, DBSize: dbSize, Alarms: alarms, Instances: instances}
	if len(alarms) > 0 {
		out.Status = "degraded"
	}
	return out
}

// ParseInstanceKey reads /orbikit/services/<service>/<id>.
func ParseInstanceKey(prefix, key string) (Instance, bool) {
	rest, ok := strings.CutPrefix(key, strings.TrimRight(prefix, "/")+"/")
	if !ok {
		return Instance{}, false
	}
	service, id, ok := strings.Cut(rest, "/")
	if !ok || service == "" || id == "" || strings.Contains(id, "/") {
		return Instance{}, false
	}
	return Instance{Service: service, ID: id}, true
}

// ErrEtcd is returned when the cluster cannot be read.
var ErrEtcd = errors.New("etcd unreachable")
