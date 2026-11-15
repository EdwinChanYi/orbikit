package nacos

import (
	"bytes"
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Overlay unmarshals the remote document onto dst.
// dst already holds the local configuration. A nil client leaves dst unchanged.
// A remote failure returns an error containing "using local config" and does not change dst.
func Overlay(ctx context.Context, c *Client, dataID string, dst any) (bool, error) {
	if c == nil {
		return false, nil
	}
	content, err := c.Get(ctx, dataID)
	if err != nil {
		return false, fmt.Errorf("using local config: %w", err)
	}
	content = os.ExpandEnv(content)
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(content), &node); err != nil {
		return false, fmt.Errorf("using local config: %w", err)
	}
	if err := yaml.Unmarshal([]byte(content), dst); err != nil {
		return false, fmt.Errorf("using local config: %w", err)
	}
	return true, nil
}

// Watch long-polls dataID and calls onChange with each new body.
// contentMD5 is the hex MD5 of the body currently applied.
func (c *Client) Watch(ctx context.Context, dataID, contentMD5 string, onChange func(content string)) {
	sum := contentMD5
	for {
		if ctx.Err() != nil {
			return
		}
		changed, err := c.poll(ctx, dataID, sum)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			continue
		}
		if !changed {
			continue
		}
		content, err := c.Get(ctx, dataID)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		onChange(content)
		sum = fmt.Sprintf("%x", md5.Sum([]byte(content)))
	}
}

func (c *Client) poll(ctx context.Context, dataID, sum string) (bool, error) {
	changed, status, err := c.pull(ctx, dataID, sum)
	if status != http.StatusForbidden {
		return changed, err
	}
	c.clearToken()
	changed, _, err = c.pull(ctx, dataID, sum)
	return changed, err
}

func (c *Client) pull(ctx context.Context, dataID, sum string) (bool, int, error) {
	if err := c.login(ctx); err != nil {
		return false, 0, err
	}
	form := url.Values{}
	form.Set("Listening-Configs", dataID+"\x02"+Group+"\x02"+sum+"\x01")
	query := url.Values{}
	c.mu.Lock()
	query.Set("accessToken", c.token)
	c.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/nacos/v1/cs/configs/listener?"+query.Encode(), strings.NewReader(form.Encode()))
	if err != nil {
		return false, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Long-Pulling-Timeout", "30000")
	client := &http.Client{Timeout: 35 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, resp.StatusCode, err
	}
	if resp.StatusCode != http.StatusOK {
		return false, resp.StatusCode, fmt.Errorf("nacos status %d", resp.StatusCode)
	}
	return len(bytes.TrimSpace(raw)) > 0, resp.StatusCode, nil
}
