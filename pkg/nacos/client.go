package nacos

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const Group = "orbikit"

// DataID returns the Nacos dataId for a platform service.
func DataID(service string) (string, error) {
	switch service {
	case "core", "chatroom", "gateway", "webhook":
		return service + ".yaml", nil
	default:
		return "", fmt.Errorf("服务名不在允许范围内")
	}
}

// Client calls the Nacos Open API.
type Client struct {
	baseURL  string
	username string
	password string
	http     *http.Client

	mu    sync.Mutex
	token string
}

// Revision is one published config body.
type Revision struct {
	ID       string
	Content  string
	Modified string
}

// Listener is one process watching a config.
type Listener struct {
	IP string `json:"ip"`
}

// New returns a client for baseURL. baseURL has no trailing slash.
func New(baseURL, username, password string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		http:     &http.Client{Timeout: 10 * time.Second},
	}
}

// Get reads the current config body.
func (c *Client) Get(ctx context.Context, dataID string) (string, error) {
	body, status, err := c.do(ctx, http.MethodGet, "/nacos/v1/cs/configs", url.Values{
		"dataId": {dataID},
		"group":  {Group},
	}, nil)
	if err != nil {
		return "", err
	}
	if status == http.StatusNotFound {
		return "", fmt.Errorf("nacos config %s not found", dataID)
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("nacos status %d", status)
	}
	return string(body), nil
}

// Publish writes the current config body.
func (c *Client) Publish(ctx context.Context, dataID, content string) error {
	_, status, err := c.do(ctx, http.MethodPost, "/nacos/v1/cs/configs", nil, url.Values{
		"dataId":  {dataID},
		"group":   {Group},
		"type":    {"yaml"},
		"content": {content},
	})
	return statusError(status, err)
}

// History lists recent published bodies.
func (c *Client) History(ctx context.Context, dataID string) ([]Revision, error) {
	body, status, err := c.do(ctx, http.MethodGet, "/nacos/v1/cs/history", url.Values{
		"search":   {"accurate"},
		"dataId":   {dataID},
		"group":    {Group},
		"pageNo":   {"1"},
		"pageSize": {"20"},
	}, nil)
	if err != nil || status != http.StatusOK {
		if err == nil {
			err = fmt.Errorf("nacos status %d", status)
		}
		return nil, err
	}
	var decoded struct {
		PageItems []struct {
			ID               string `json:"id"`
			Content          string `json:"content"`
			LastModifiedTime string `json:"lastModifiedTime"`
		} `json:"pageItems"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	out := make([]Revision, len(decoded.PageItems))
	for i, item := range decoded.PageItems {
		out[i] = Revision{ID: item.ID, Content: item.Content, Modified: item.LastModifiedTime}
	}
	return out, nil
}

// Beta reads the gray release.
func (c *Client) Beta(ctx context.Context, dataID string) (string, []string, error) {
	body, status, err := c.do(ctx, http.MethodGet, "/nacos/v1/cs/configs", url.Values{
		"beta":   {"true"},
		"dataId": {dataID},
		"group":  {Group},
	}, nil)
	if err != nil || status != http.StatusOK {
		if err == nil {
			err = fmt.Errorf("nacos status %d", status)
		}
		return "", nil, err
	}
	var decoded struct {
		Content string `json:"content"`
		BetaIps string `json:"betaIps"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", nil, err
	}
	var ips []string
	for _, ip := range strings.Split(decoded.BetaIps, ",") {
		ip = strings.TrimSpace(ip)
		if ip != "" {
			ips = append(ips, ip)
		}
	}
	return decoded.Content, ips, nil
}

// PublishBeta stores a gray release for the given IPs.
func (c *Client) PublishBeta(ctx context.Context, dataID, content string, ips []string) error {
	if len(ips) == 0 {
		return fmt.Errorf("无效的灰度 IP")
	}
	clean := make([]string, 0, len(ips))
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		if net.ParseIP(ip) == nil {
			return fmt.Errorf("无效的灰度 IP")
		}
		clean = append(clean, ip)
	}
	_, status, err := c.do(ctx, http.MethodPost, "/nacos/v1/cs/configs", nil, url.Values{
		"dataId":  {dataID},
		"group":   {Group},
		"type":    {"yaml"},
		"content": {content},
		"beta":    {"true"},
		"betaIps": {strings.Join(clean, ",")},
	})
	return statusError(status, err)
}

// DeleteBeta removes the gray release.
func (c *Client) DeleteBeta(ctx context.Context, dataID string) error {
	_, status, err := c.do(ctx, http.MethodDelete, "/nacos/v1/cs/configs", url.Values{
		"beta":   {"true"},
		"dataId": {dataID},
		"group":  {Group},
	}, nil)
	return statusError(status, err)
}

// Listeners lists processes watching dataID.
func (c *Client) Listeners(ctx context.Context, dataID string) ([]Listener, error) {
	body, status, err := c.do(ctx, http.MethodGet, "/nacos/v1/cs/configs/listener", url.Values{
		"dataId": {dataID},
		"group":  {Group},
	}, nil)
	if err != nil || status != http.StatusOK {
		if err == nil {
			err = fmt.Errorf("nacos status %d", status)
		}
		return nil, err
	}
	var out []Listener
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func statusError(status int, err error) error {
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("nacos status %d", status)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, form url.Values) ([]byte, int, error) {
	if err := c.login(ctx); err != nil {
		return nil, 0, err
	}
	if query == nil {
		query = url.Values{}
	}
	c.mu.Lock()
	query.Set("accessToken", c.token)
	c.mu.Unlock()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path+"?"+query.Encode(), body)
	if err != nil {
		return nil, 0, err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return raw, resp.StatusCode, nil
}

func (c *Client) login(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" {
		return nil
	}
	form := url.Values{}
	form.Set("username", c.username)
	form.Set("password", c.password)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/nacos/v1/auth/login", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var decoded struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return err
	}
	if decoded.AccessToken == "" {
		return fmt.Errorf("nacos login failed")
	}
	c.token = decoded.AccessToken
	return nil
}
