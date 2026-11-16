package status

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Services is the fixed platform table on the overview page.
var Services = []struct {
	Name     string
	GRPC     string
	Register bool
}{
	{Name: "core", GRPC: "core:9001", Register: true},
	{Name: "chatroom", GRPC: "chatroom:9002", Register: true},
	{Name: "webhook", GRPC: "webhook:9004", Register: true},
	{Name: "gateway", Register: false},
}

// Health checks one gRPC address. Only a "serving" result is healthy.
type Health interface {
	Check(ctx context.Context, addr string) (string, error)
}

// ServiceRow is one platform service on the overview page.
type ServiceRow struct {
	Name      string `json:"name"`
	Container string `json:"container"`
	GRPC      string `json:"grpc"`
	// Instances is nil when a registered service cannot be listed.
	Instances []Instance `json:"instances"`
	ImageID   string     `json:"image_id"`
	// LastRelease is unset until a release exists, so JSON encodes null.
	LastRelease *ReleaseStamp `json:"last_release"`
}

// ReleaseStamp is the newest successful release for one service.
type ReleaseStamp struct {
	Ref        string    `json:"ref"`
	FinishedAt time.Time `json:"finished_at"`
}

// ReleaseReader supplies the newest successful release. A nil reader leaves the column empty.
type ReleaseReader interface {
	Last(ctx context.Context, service string) (*ReleaseStamp, error)
}

// Overview is the aggregated platform status.
type Overview struct {
	Services []ServiceRow `json:"services"`
}

type containerInfo struct {
	Status  string `json:"status"`
	Running bool   `json:"running"`
	ImageID string `json:"image_id"`
}

// Collect reads container, gRPC, and registration state for every service.
// A builder miss marks that row unknown and still returns the full table.
func Collect(ctx context.Context, builderURL string, health Health, cluster Cluster) Overview {
	rows := make([]ServiceRow, 0, len(Services))
	for _, svc := range Services {
		row := ServiceRow{
			Name:      svc.Name,
			Container: "unknown",
			GRPC:      grpcState(ctx, health, svc.GRPC),
			Instances: instancesFor(svc.Name, svc.Register, cluster),
		}
		if info, ok := fetchContainer(ctx, builderURL, svc.Name); ok {
			row.Container = containerState(info)
			row.ImageID = info.ImageID
		}
		rows = append(rows, row)
	}
	return Overview{Services: rows}
}

func containerState(info containerInfo) string {
	if info.Running {
		return "running"
	}
	if info.Status == "missing" {
		return "missing"
	}
	return "exited"
}

func fetchContainer(ctx context.Context, builderURL, name string) (containerInfo, bool) {
	endpoint := strings.TrimRight(builderURL, "/") + "/v1/containers/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return containerInfo{}, false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return containerInfo{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return containerInfo{}, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return containerInfo{}, false
	}
	var info containerInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return containerInfo{}, false
	}
	return info, true
}

func grpcState(ctx context.Context, health Health, addr string) string {
	if addr == "" {
		return "skipped"
	}
	if health == nil {
		return "unknown"
	}
	state, err := health.Check(ctx, addr)
	if err != nil {
		return "unknown"
	}
	if state == "serving" {
		return "serving"
	}
	return "not_serving"
}

func instancesFor(name string, register bool, cluster Cluster) []Instance {
	if register && cluster.Status == "fault" {
		return nil
	}
	if !register {
		return []Instance{}
	}
	matched := make([]Instance, 0)
	for _, inst := range cluster.Instances {
		if inst.Service == name {
			matched = append(matched, inst)
		}
	}
	return matched
}
