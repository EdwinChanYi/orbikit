package nacos

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStartDisabledDoesNotDial(t *testing.T) {
	Start(context.Background(), "core", NacosConfig{Enabled: false, Addr: "http://127.0.0.1:1"}, nil, func(string) {
		t.Fatal("disabled nacos must not apply")
	})
}

func TestStartAppliesRemoteChange(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nacos/v1/auth/login" {
			_, _ = io.WriteString(w, `{"accessToken":"tok","tokenTtl":18000}`)
			return
		}
		if r.URL.Path == "/nacos/v1/cs/configs/listener" {
			_, _ = io.WriteString(w, "changed")
			return
		}
		_, _ = io.WriteString(w, "log:\n  level: debug\n")
	}))
	defer srv.Close()

	got := make(chan string, 1)
	go Start(ctx, "core", NacosConfig{Enabled: true, Addr: srv.URL, Username: "nacos", Password: "nacos"}, []byte("log:\n  level: info\n"), func(content string) {
		select {
		case got <- content:
		default:
		}
		cancel()
	})
	select {
	case content := <-got:
		if content != "log:\n  level: debug\n" {
			t.Fatalf("content = %q", content)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("apply was not called")
	}
}
