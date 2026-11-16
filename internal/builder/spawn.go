package builder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
)

// ReleaseEnv is the configuration copied into a one-shot release container.
// The token is never written to an HTTP response.
type ReleaseEnv struct {
	Username string
	Token    string
	AdminDSN string
	Image    string
	Network  string
	GoProxy  string
}

// Configured reports whether both Git credentials are present.
func (e ReleaseEnv) Configured() bool {
	return e.Username != "" && e.Token != ""
}

// Spawn creates and starts orbikit-release-<id>, then returns without waiting.
func (d *Docker) Spawn(ctx context.Context, id uint64, project string, env ReleaseEnv) error {
	if d == nil {
		return fmt.Errorf("docker socket is not configured")
	}
	socket := d.Socket
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	image := env.Image
	if image == "" {
		image = "orbikit-builder"
	}
	network := env.Network
	if network == "" {
		network = "orbikit_default"
	}
	payload := map[string]any{
		"Image": image,
		"User":  "0",
		"Cmd":   []string{"-release", strconv.FormatUint(id, 10)},
		"Env": []string{
			"ADMIN_DSN=" + env.AdminDSN,
			"ORBIKIT_GIT_USERNAME=" + env.Username,
			"ORBIKIT_GIT_TOKEN=" + env.Token,
			"ORBIKIT_PROJECT=" + project,
			"GOPROXY=" + env.GoProxy,
			"DOCKER_SOCKET=" + socket,
		},
		"HostConfig": map[string]any{
			"Binds":       []string{socket + ":" + socket},
			"AutoRemove":  true,
			"NetworkMode": network,
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	name := "orbikit-release-" + strconv.FormatUint(id, 10)
	status, resp, err := d.call(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(name), raw)
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("docker status %d: %s", status, resp)
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal(resp, &created); err != nil {
		return err
	}
	status, resp, err = d.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(created.ID)+"/start", nil)
	if err != nil {
		return err
	}
	if status != http.StatusNoContent && status != http.StatusOK {
		return fmt.Errorf("docker status %d: %s", status, resp)
	}
	return nil
}

func (d *Docker) call(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	client := d.Client
	if client == nil {
		socket := d.Socket
		if socket == "" {
			socket = "/var/run/docker.sock"
		}
		client = &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var dial net.Dialer
				return dial.DialContext(ctx, "unix", socket)
			},
		}}
	}
	base := d.Base
	if base == "" {
		base = "http://docker"
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return res.StatusCode, nil, err
	}
	return res.StatusCode, raw, nil
}
