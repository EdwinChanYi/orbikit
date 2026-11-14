package nacosapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EdwinChanYi/orbikit/internal/admin/auth"
	"github.com/EdwinChanYi/orbikit/pkg/nacos"
)

func TestDisabledConfigStaysClosed(t *testing.T) {
	h, cookie := withSession(t, NewHandler(nil))
	rec := get(t, h, cookie, "/api/config/source")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"source":"local"`) || !strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Fatalf("source = %d %s", rec.Code, rec.Body.String())
	}
	rec = get(t, h, cookie, "/api/config/core")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "Nacos 未打开") {
		t.Fatalf("core = %d %s", rec.Code, rec.Body.String())
	}
}

func TestConfigRequiresSession(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/config/source", nil)
	rec := httptest.NewRecorder()
	auth.RequireSession(auth.NewMemory(), nil, NewHandler(nil)).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "登录失败") {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestPublishGetRollbackAndRejectsUnknown(t *testing.T) {
	var mu sync.Mutex
	current := "log:\n  level: info\n"
	nacosSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nacos/v1/auth/login" {
			_, _ = io.WriteString(w, `{"accessToken":"tok","tokenTtl":18000}`)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/nacos/v1/cs/configs":
			_, _ = io.WriteString(w, current)
		case r.Method == http.MethodPost && r.URL.Path == "/nacos/v1/cs/configs":
			_ = r.ParseForm()
			current = r.Form.Get("content")
			_, _ = io.WriteString(w, "true")
		case r.URL.Path == "/nacos/v1/cs/history":
			_, _ = io.WriteString(w, `{"pageItems":[{"id":"9","content":"log:\n  level: old\n","lastModifiedTime":"2026-11-15 00:00:00"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer nacosSrv.Close()

	h, cookie := withSession(t, NewHandler(nacos.New(nacosSrv.URL, "nacos", "nacos")))
	req := httptest.NewRequest(http.MethodPut, "/api/config/core", strings.NewReader(`{"content":"log:\n  level: debug\n"}`))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("put = %d %s", rec.Code, rec.Body.String())
	}
	rec = get(t, h, cookie, "/api/config/core")
	if !strings.Contains(rec.Body.String(), "debug") {
		t.Fatalf("get = %s", rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/api/config/core/rollback", strings.NewReader(`{"id":"9"}`))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("rollback = %d %s", rec.Code, rec.Body.String())
	}
	mu.Lock()
	got := current
	mu.Unlock()
	if got != "log:\n  level: old\n" {
		t.Fatalf("rolled back to %q", got)
	}
	rec = get(t, h, cookie, "/api/config/admin")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "服务名不在允许范围内") {
		t.Fatalf("admin = %d %s", rec.Code, rec.Body.String())
	}
}

func withSession(t *testing.T, next http.Handler) (http.Handler, *http.Cookie) {
	t.Helper()
	store := auth.NewMemory()
	raw := []byte("0123456789abcdef0123456789abcdef")
	sum := sha256.Sum256(raw)
	if err := store.CreateSession(context.Background(), sum[:], time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: auth.CookieName, Value: hex.EncodeToString(raw)}
	return auth.RequireSession(store, nil, next), cookie
}

func get(t *testing.T, h http.Handler, cookie *http.Cookie, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
