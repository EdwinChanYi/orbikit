package release

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Docker talks to the Engine API over a Unix socket or a test base URL.
type Docker struct {
	Socket string
	Base   string
	Client *http.Client
}

type portBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type restartPolicy struct {
	Name              string `json:"Name"`
	MaximumRetryCount int    `json:"MaximumRetryCount"`
}

// Build sends the directory as a tar context and returns the new image id.
func (d *Docker) Build(ctx context.Context, dir, service, tag, goproxy string) (string, string, error) {
	body, err := tarContext(dir)
	if err != nil {
		return "", "", err
	}
	args, err := json.Marshal(map[string]string{"SERVICE": service, "GOPROXY": goproxy})
	if err != nil {
		return "", "", err
	}
	query := url.Values{}
	query.Set("t", tag)
	query.Set("dockerfile", "Dockerfile")
	query.Set("buildargs", string(args))
	status, raw, err := d.do(ctx, http.MethodPost, "/build?"+query.Encode(), bytes.NewReader(body), "application/x-tar")
	logText := string(raw)
	if err != nil {
		return "", logText, err
	}
	if status != http.StatusOK {
		return "", logText, fmt.Errorf("docker status %d", status)
	}
	imageID, buildErr := parseBuild(raw)
	if buildErr != nil {
		return "", logText, buildErr
	}
	if imageID == "" {
		return "", logText, fmt.Errorf("docker build returned no image id")
	}
	return imageID, logText, nil
}

// ImageExists reports whether the engine still has the image.
func (d *Docker) ImageExists(ctx context.Context, imageID string) (bool, error) {
	status, _, err := d.do(ctx, http.MethodGet, "/images/"+url.PathEscape(imageID)+"/json", nil, "")
	if err != nil {
		return false, err
	}
	if status == http.StatusNotFound {
		return false, nil
	}
	if status != http.StatusOK {
		return false, fmt.Errorf("docker status %d", status)
	}
	return true, nil
}

// Inspect reads the fields a replacement container must keep.
func (d *Docker) Inspect(ctx context.Context, container string) (Spec, error) {
	status, raw, err := d.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(container)+"/json", nil, "")
	if err != nil {
		return Spec{}, err
	}
	if status != http.StatusOK {
		return Spec{}, fmt.Errorf("docker status %d", status)
	}
	var body struct {
		Config struct {
			Env        []string `json:"Env"`
			Cmd        []string `json:"Cmd"`
			Entrypoint []string `json:"Entrypoint"`
			User       string   `json:"User"`
			WorkingDir string   `json:"WorkingDir"`
		} `json:"Config"`
		HostConfig struct {
			Binds         []string                 `json:"Binds"`
			NetworkMode   string                   `json:"NetworkMode"`
			PortBindings  map[string][]portBinding `json:"PortBindings"`
			RestartPolicy restartPolicy            `json:"RestartPolicy"`
		} `json:"HostConfig"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return Spec{}, err
	}
	ports, err := json.Marshal(body.HostConfig.PortBindings)
	if err != nil {
		return Spec{}, err
	}
	restart, err := json.Marshal(body.HostConfig.RestartPolicy)
	if err != nil {
		return Spec{}, err
	}
	return Spec{
		Env:           body.Config.Env,
		Cmd:           body.Config.Cmd,
		Entrypoint:    body.Config.Entrypoint,
		User:          body.Config.User,
		WorkingDir:    body.Config.WorkingDir,
		Binds:         body.HostConfig.Binds,
		NetworkMode:   body.HostConfig.NetworkMode,
		PortBindings:  ports,
		RestartPolicy: restart,
	}, nil
}

// Create records a container without starting it, so its ports do not collide
// with the container it will replace.
func (d *Docker) Create(ctx context.Context, name string, spec Spec, imageID string) error {
	ports := json.RawMessage(spec.PortBindings)
	if len(ports) == 0 {
		ports = json.RawMessage(`{}`)
	}
	restart := json.RawMessage(spec.RestartPolicy)
	if len(restart) == 0 {
		restart = json.RawMessage(`{"Name":"","MaximumRetryCount":0}`)
	}
	payload := map[string]any{
		"Image":      imageID,
		"Env":        spec.Env,
		"Cmd":        spec.Cmd,
		"Entrypoint": spec.Entrypoint,
		"User":       spec.User,
		"WorkingDir": spec.WorkingDir,
		"HostConfig": map[string]any{
			"Binds":         spec.Binds,
			"NetworkMode":   spec.NetworkMode,
			"PortBindings":  ports,
			"RestartPolicy": restart,
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	status, resp, err := d.do(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(name), bytes.NewReader(raw), "application/json")
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("docker status %d: %s", status, resp)
	}
	return nil
}

// Start runs a created container by name.
func (d *Docker) Start(ctx context.Context, container string) error {
	status, resp, err := d.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(container)+"/start", nil, "")
	if err != nil {
		return err
	}
	if status != http.StatusNoContent && status != http.StatusOK {
		return fmt.Errorf("docker status %d: %s", status, resp)
	}
	return nil
}

// StopRemove stops and deletes a container.
func (d *Docker) StopRemove(ctx context.Context, container string) error {
	status, resp, err := d.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(container)+"/stop", nil, "")
	if err != nil {
		return err
	}
	if status != http.StatusNoContent && status != http.StatusNotModified && status != http.StatusNotFound {
		return fmt.Errorf("docker status %d: %s", status, resp)
	}
	status, resp, err = d.do(ctx, http.MethodDelete, "/containers/"+url.PathEscape(container)+"?force=1", nil, "")
	if err != nil {
		return err
	}
	if status != http.StatusNoContent && status != http.StatusNotFound {
		return fmt.Errorf("docker status %d: %s", status, resp)
	}
	return nil
}

// Rename changes a container's name.
func (d *Docker) Rename(ctx context.Context, from, to string) error {
	status, resp, err := d.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(from)+"/rename?name="+url.QueryEscape(to), nil, "")
	if err != nil {
		return err
	}
	if status != http.StatusNoContent {
		return fmt.Errorf("docker status %d: %s", status, resp)
	}
	return nil
}

func (d *Docker) do(ctx context.Context, method, path string, body io.Reader, contentType string) (int, []byte, error) {
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
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return 0, nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return res.StatusCode, nil, err
	}
	return res.StatusCode, raw, nil
}

func parseBuild(raw []byte) (string, error) {
	var imageID string
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg struct {
			Error string `json:"error"`
			Aux   struct {
				ID string `json:"ID"`
			} `json:"aux"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if msg.Error != "" {
			return "", fmt.Errorf("%s", msg.Error)
		}
		if msg.Aux.ID != "" {
			imageID = msg.Aux.ID
		}
	}
	return imageID, scanner.Err()
}

func tarContext(dir string) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		name := filepath.ToSlash(rel)
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = name
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if info.IsDir() || !info.Mode().IsRegular() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(tw, file)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
