package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
)

var serviceName = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

var allowed = map[string]bool{
	"core": true, "chatroom": true, "gateway": true, "webhook": true,
}

// Allowed reports whether name may be turned into a container query.
func Allowed(name string) bool {
	return allowed[name]
}

// ContainerName returns the compose container for one whitelist service.
func ContainerName(project, name string) (string, error) {
	if !serviceName.MatchString(project) || !Allowed(name) {
		return "", errors.New("服务名不在允许范围内")
	}
	return project + "-" + name + "-1", nil
}

// Info is one container's runtime state.
type Info struct {
	Status  string
	Running bool
	ImageID string
}

// Inspector reads one container by its exact Docker name.
type Inspector interface {
	Inspect(ctx context.Context, container string) (Info, error)
}

// Docker queries the Engine API through a Unix socket or a test base URL.
type Docker struct {
	Socket string
	Base   string
	Client *http.Client
}

// Inspect calls GET /containers/{container}/json.
func (d *Docker) Inspect(ctx context.Context, container string) (Info, error) {
	client := d.Client
	if client == nil {
		client = &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var dial net.Dialer
				return dial.DialContext(ctx, "unix", d.Socket)
			},
		}}
	}
	base := d.Base
	if base == "" {
		base = "http://docker"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/containers/"+container+"/json", nil)
	if err != nil {
		return Info{}, err
	}
	res, err := client.Do(req)
	if err != nil {
		return Info{}, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return Info{Status: "missing"}, nil
	}
	if res.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("docker status %d", res.StatusCode)
	}
	var body struct {
		Image string `json:"Image"`
		State struct {
			Status  string `json:"Status"`
			Running bool   `json:"Running"`
		} `json:"State"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return Info{}, err
	}
	return Info{Status: body.State.Status, Running: body.State.Running, ImageID: body.Image}, nil
}
