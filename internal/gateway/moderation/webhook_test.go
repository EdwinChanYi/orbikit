package moderation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWebhookVerdicts(t *testing.T) {
	var got WebhookRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var resp WebhookResponse
		switch got.Text {
		case "bad":
			resp = WebhookResponse{Allowed: false, Reason: "abuse"}
		case "mask me":
			resp = WebhookResponse{Allowed: true, Text: "**** me"}
		default:
			resp = WebhookResponse{Allowed: true}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	m, err := New(Config{Type: TypeWebhook, Webhook: WebhookConfig{
		URL:     srv.URL,
		Headers: map[string]string{"Authorization": "Bearer token"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	res, err := m.CheckText(ctx, "app1", 42, 7, "hello")
	if err != nil || !res.Allowed || res.Text != "hello" {
		t.Fatalf("allow = %+v, %v", res, err)
	}
	if got != (WebhookRequest{AppID: "app1", UID: 42, RoomID: 7, Text: "hello"}) {
		t.Fatalf("webhook received %+v", got)
	}

	res, err = m.CheckText(ctx, "app1", 42, 7, "bad")
	if err != nil || res.Allowed || res.Reason != "abuse" {
		t.Fatalf("reject = %+v, %v", res, err)
	}

	res, err = m.CheckText(ctx, "app1", 42, 7, "mask me")
	if err != nil || !res.Allowed || res.Text != "**** me" {
		t.Fatalf("replace = %+v, %v", res, err)
	}
}

func TestWebhookFailurePolicy(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer failing.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer slow.Close()
	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer garbage.Close()

	for _, url := range []string{failing.URL, slow.URL, garbage.URL} {
		reject, err := NewWebhook(WebhookConfig{URL: url, Timeout: 100 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reject.CheckText(context.Background(), "a", 1, 1, "x"); err == nil {
			t.Fatalf("%s: reject policy returned no error", url)
		}

		allow, err := NewWebhook(WebhookConfig{URL: url, Timeout: 100 * time.Millisecond, OnError: OnErrorAllow})
		if err != nil {
			t.Fatal(err)
		}
		res, err := allow.CheckText(context.Background(), "a", 1, 1, "x")
		if err != nil || !res.Allowed || res.Text != "x" {
			t.Fatalf("%s: allow policy = %+v, %v", url, res, err)
		}
	}
}

func TestWebhookConfigValidation(t *testing.T) {
	for _, cfg := range []WebhookConfig{
		{URL: ""},
		{URL: "ftp://example.com"},
		{URL: "http://example.com", OnError: "maybe"},
	} {
		if _, err := NewWebhook(cfg); err == nil {
			t.Fatalf("NewWebhook(%+v) succeeded", cfg)
		}
	}
}
