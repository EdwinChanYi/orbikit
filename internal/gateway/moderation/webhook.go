package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/pkg/log"
)

const (
	OnErrorAllow  = "allow"
	OnErrorReject = "reject"

	defaultWebhookTimeout = 3 * time.Second
	maxWebhookResponse    = 64 * 1024
)

// WebhookRequest is the JSON body POSTed to the webhook URL.
type WebhookRequest struct {
	AppID  string `json:"app_id"`
	UID    int64  `json:"uid"`
	RoomID int64  `json:"room_id"`
	Text   string `json:"text"`
}

// WebhookResponse is the JSON body the webhook must answer with (HTTP 2xx).
// An empty Text on an allowed message keeps the original text.
type WebhookResponse struct {
	Allowed bool   `json:"allowed"`
	Text    string `json:"text"`
	Reason  string `json:"reason"`
}

// Webhook delegates the decision to an HTTP endpoint owned by the integrator.
type Webhook struct {
	url        string
	headers    map[string]string
	allowOnErr bool
	client     *http.Client
}

// NewWebhook validates cfg and returns a Webhook moderator.
func NewWebhook(cfg WebhookConfig) (*Webhook, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("moderation: invalid webhook.url %q", cfg.URL)
	}
	w := &Webhook{url: cfg.URL, headers: cfg.Headers}
	switch cfg.OnError {
	case "", OnErrorReject:
	case OnErrorAllow:
		w.allowOnErr = true
	default:
		return nil, fmt.Errorf("moderation: unknown webhook.on_error %q", cfg.OnError)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultWebhookTimeout
	}
	w.client = &http.Client{Timeout: timeout}
	return w, nil
}

func (w *Webhook) CheckText(ctx context.Context, appID string, uid, roomID int64, text string) (Result, error) {
	res, err := w.call(ctx, &WebhookRequest{AppID: appID, UID: uid, RoomID: roomID, Text: text})
	if err != nil {
		if w.allowOnErr {
			log.L().Warn("moderation webhook failed, allowing message",
				zap.String("app_id", appID), zap.Int64("uid", uid), zap.Error(err))
			return Result{Allowed: true, Text: text}, nil
		}
		return Result{}, err
	}
	if !res.Allowed {
		return Result{Allowed: false, Reason: res.Reason}, nil
	}
	out := res.Text
	if out == "" {
		out = text
	}
	return Result{Allowed: true, Text: out, Reason: res.Reason}, nil
}

func (w *Webhook) call(ctx context.Context, in *WebhookRequest) (*WebhookResponse, error) {
	body, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range w.headers {
		req.Header.Set(k, v)
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("moderation webhook: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxWebhookResponse))
	if err != nil {
		return nil, fmt.Errorf("moderation webhook: read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("moderation webhook: status %d", resp.StatusCode)
	}
	var out WebhookResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("moderation webhook: decode response: %w", err)
	}
	return &out, nil
}
