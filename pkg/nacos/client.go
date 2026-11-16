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
	case "core", "chatroom", "gateway", "webhook", "admin", "builder":
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

	mu         sync.Mutex
	token      string
	tokenUntil time.Time
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

// publishVisibleTimeout is how long Publish waits until a following read
// returns the body just written. Nacos can keep serving the previous body
// for a short time after the write is accepted.
const publishVisibleTimeout = 3 * time.Second

// Publish writes the current config body and returns after a read sees it.
func (c *Client) Publish(ctx context.Context, dataID, content string) error {
	_, status, err := c.do(ctx, http.MethodPost, "/nacos/v1/cs/configs", nil, url.Values{
		"dataId":  {dataID},
		"group":   {Group},
		"type":    {"yaml"},
		"content": {content},
	})
	if err = statusError(status, err); err != nil {
		return err
	}
	return c.waitVisible(ctx, dataID, content)
}

func (c *Client) waitVisible(ctx context.Context, dataID, content string) error {
	deadline := time.NewTimer(publishVisibleTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var last error
	for {
		got, err := c.Get(ctx, dataID)
		if err == nil && got == content {
			return nil
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if last != nil {
				return last
			}
			return fmt.Errorf("nacos config %s not visible after publish", dataID)
		case <-ticker.C:
		}
	}
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
		content := item.Content
		if content == "" && item.ID != "" {
			var err error
			content, err = c.historyContent(ctx, dataID, item.ID)
			if err != nil {
				return nil, err
			}
		}
		out[i] = Revision{ID: item.ID, Content: content, Modified: item.LastModifiedTime}
	}
	return out, nil
}

func (c *Client) historyContent(ctx context.Context, dataID, id string) (string, error) {
	body, status, err := c.do(ctx, http.MethodGet, "/nacos/v1/cs/history", url.Values{
		"dataId": {dataID},
		"group":  {Group},
		"nid":    {id},
	}, nil)
	if err != nil || status != http.StatusOK {
		if err == nil {
			err = fmt.Errorf("nacos status %d", status)
		}
		return "", err
	}
	var decoded struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", err
	}
	return decoded.Content, nil
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
	content, betaIPs, err := decodeBeta(body)
	if err != nil {
		return "", nil, err
	}
	var ips []string
	for _, ip := range strings.Split(betaIPs, ",") {
		ip = strings.TrimSpace(ip)
		if ip != "" {
			ips = append(ips, ip)
		}
	}
	return content, ips, nil
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
	// Nacos 2.5 reads the gray targets from the betaIps header. A form field
	// named beta is ignored and the same request overwrites the official config.
	_, status, err := c.doHeader(ctx, http.MethodPost, "/nacos/v1/cs/configs", nil, url.Values{
		"dataId":  {dataID},
		"group":   {Group},
		"type":    {"yaml"},
		"content": {content},
	}, map[string]string{"betaIps": strings.Join(clean, ",")})
	return statusError(status, err)
}

// decodeBeta accepts the legacy flat document and the Nacos 2.5 wrapper.
// A null data field means there is no gray release.
func decodeBeta(body []byte) (string, string, error) {
	var wrapped struct {
		Code int `json:"code"`
		Data *struct {
			Content string `json:"content"`
			BetaIps string `json:"betaIps"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return "", "", err
	}
	if wrapped.Code != 0 || wrapped.Data != nil {
		if wrapped.Data == nil {
			return "", "", nil
		}
		return wrapped.Data.Content, wrapped.Data.BetaIps, nil
	}
	var flat struct {
		Content string `json:"content"`
		BetaIps string `json:"betaIps"`
	}
	if err := json.Unmarshal(body, &flat); err != nil {
		return "", "", err
	}
	return flat.Content, flat.BetaIps, nil
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
	if len(body) > 0 && body[0] == '[' {
		var out []Listener
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	var decoded struct {
		Status map[string]string `json:"lisentersGroupkeyStatus"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	out := make([]Listener, 0, len(decoded.Status))
	for ip := range decoded.Status {
		out = append(out, Listener{IP: ip})
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
	return c.doHeader(ctx, method, path, query, form, nil)
}

func (c *Client) doHeader(ctx context.Context, method, path string, query url.Values, form url.Values, header map[string]string) ([]byte, int, error) {
	body, status, err := c.roundTrip(ctx, method, path, query, form, header)
	if status != http.StatusForbidden {
		return body, status, err
	}
	c.clearToken()
	return c.roundTrip(ctx, method, path, query, form, header)
}

func (c *Client) roundTrip(ctx context.Context, method, path string, query url.Values, form url.Values, header map[string]string) ([]byte, int, error) {
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
	for key, value := range header {
		req.Header.Set(key, value)
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

func (c *Client) clearToken() {
	c.mu.Lock()
	c.token = ""
	c.tokenUntil = time.Time{}
	c.mu.Unlock()
}

func (c *Client) login(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenUntil) {
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
		TokenTTL    int    `json:"tokenTtl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return err
	}
	if decoded.AccessToken == "" {
		return fmt.Errorf("nacos login failed")
	}
	ttl := time.Duration(decoded.TokenTTL) * time.Second
	if ttl > time.Minute {
		ttl -= time.Minute
	} else {
		ttl = 0
	}
	c.token = decoded.AccessToken
	c.tokenUntil = time.Now().Add(ttl)
	return nil
}
