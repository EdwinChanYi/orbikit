package nacos

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOverlayKeepsLocalFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nacos/v1/auth/login" {
			_, _ = io.WriteString(w, `{"accessToken":"tok","tokenTtl":18000}`)
			return
		}
		_, _ = io.WriteString(w, "log:\n  level: debug\n")
	}))
	defer srv.Close()

	dst := struct {
		Log  struct{ Level string `yaml:"level"` } `yaml:"log"`
		Addr string `yaml:"grpc_addr"`
	}{}
	dst.Log.Level = "info"
	dst.Addr = ":9001"
	used, err := Overlay(context.Background(), New(srv.URL, "nacos", "nacos"), "core.yaml", &dst)
	if err != nil || !used || dst.Log.Level != "debug" || dst.Addr != ":9001" {
		t.Fatalf("used = %v, dst = %+v, err = %v", used, dst, err)
	}
}

func TestOverlayExpandsEnv(t *testing.T) {
	t.Setenv("ORB_PORT", ":9001")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nacos/v1/auth/login" {
			_, _ = io.WriteString(w, `{"accessToken":"tok","tokenTtl":18000}`)
			return
		}
		_, _ = io.WriteString(w, "grpc_addr: \"${ORB_PORT}\"\n")
	}))
	defer srv.Close()

	dst := struct {
		Addr string `yaml:"grpc_addr"`
	}{}
	used, err := Overlay(context.Background(), New(srv.URL, "nacos", "nacos"), "core.yaml", &dst)
	if err != nil || !used || dst.Addr != ":9001" {
		t.Fatalf("used = %v, addr = %q, err = %v", used, dst.Addr, err)
	}
}

func TestOverlayKeepsLocalWhenRemoteFails(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/nacos/v1/auth/login" {
			_, _ = io.WriteString(w, `{"accessToken":"tok","tokenTtl":18000}`)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	dst := struct {
		Log struct{ Level string `yaml:"level"` } `yaml:"log"`
	}{}
	dst.Log.Level = "info"
	used, err := Overlay(context.Background(), New(srv.URL, "nacos", "nacos"), "core.yaml", &dst)
	if err == nil || !strings.Contains(err.Error(), "using local config") || used || dst.Log.Level != "info" {
		t.Fatalf("used = %v, level = %s, err = %v", used, dst.Log.Level, err)
	}
	if used, err := Overlay(context.Background(), nil, "core.yaml", &dst); used || err != nil || hits == 0 {
		t.Fatalf("nil client used = %v, err = %v, hits = %d", used, err, hits)
	}
}

func TestWatchUsesTheNewMD5(t *testing.T) {
	const next = "log:\n  level: warn\n"
	sum := fmt.Sprintf("%x", md5.Sum([]byte(next)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var second string
	var listeners int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nacos/v1/auth/login" {
			_, _ = io.WriteString(w, `{"accessToken":"tok","tokenTtl":18000}`)
			return
		}
		if r.URL.Path == "/nacos/v1/cs/configs/listener" {
			raw, _ := io.ReadAll(r.Body)
			mu.Lock()
			listeners++
			n := listeners
			mu.Unlock()
			if n == 1 {
				if r.Header.Get("Long-Pulling-Timeout") != "30000" {
					t.Errorf("timeout header = %q", r.Header.Get("Long-Pulling-Timeout"))
				}
				_, _ = io.WriteString(w, "core.yaml")
				return
			}
			mu.Lock()
			second = string(raw)
			mu.Unlock()
			cancel()
			return
		}
		_, _ = io.WriteString(w, next)
	}))
	defer srv.Close()

	var got []string
	done := make(chan struct{})
	go func() {
		New(srv.URL, "nacos", "nacos").Watch(ctx, "core.yaml", "old", func(content string) {
			got = append(got, content)
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("watch did not return")
	}
	if len(got) != 1 || got[0] != next {
		t.Fatalf("changes = %#v", got)
	}
	if !strings.Contains(second, sum) {
		t.Fatalf("second listen = %q, want md5 %s", second, sum)
	}
}
