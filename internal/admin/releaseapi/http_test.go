package releaseapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/EdwinChanYi/orbikit/internal/admin/auth"
	"github.com/EdwinChanYi/orbikit/internal/release"
)

func TestReleaseRequiresSession(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/releases", strings.NewReader(`{"service":"core","ref":"v1"}`))
	rec := httptest.NewRecorder()
	auth.RequireSession(auth.NewMemory(), nil, NewHandler(release.NewMemory(), "http://builder", nil)).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "登录失败") {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestPublishValidation(t *testing.T) {
	h, cookie := handlerWith(t, release.NewMemory(), true)
	rec := post(t, h, cookie, "/api/releases", `{"service":"mysql","ref":"v1"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "服务名不在允许范围内") {
		t.Fatalf("service = %d %s", rec.Code, rec.Body.String())
	}
	rec = post(t, h, cookie, "/api/releases", `{"service":"core","ref":"abc1234"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "引用无效") {
		t.Fatalf("ref = %d %s", rec.Code, rec.Body.String())
	}
	rec = post(t, h, cookie, "/api/releases", `{"service":"core","ref":"feature/login"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "引用无效") {
		t.Fatalf("branch = %d %s", rec.Code, rec.Body.String())
	}
}

func TestMissingCredentialsDoesNotCreateATask(t *testing.T) {
	store := release.NewMemory()
	h, cookie := handlerWith(t, store, false)
	rec := post(t, h, cookie, "/api/releases", `{"service":"core","ref":"v1.2.3"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "缺少 Git 凭证") {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	if items, _ := store.List(context.Background(), "core", 20); len(items) != 0 {
		t.Fatalf("created %+v", items)
	}
}

func TestBuilderDownDoesNotCreateATask(t *testing.T) {
	store := release.NewMemory()
	authStore := auth.NewMemory()
	h := auth.RequireSession(authStore, nil, NewHandler(store, "http://127.0.0.1:1", nil))
	rec := post(t, h, session(t, authStore), "/api/releases", `{"service":"core","ref":"v1.2.3"}`)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "builder 不可达") {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	if items, _ := store.List(context.Background(), "core", 20); len(items) != 0 {
		t.Fatalf("created %+v", items)
	}
}

func TestPublishStartsAndASecondTaskConflicts(t *testing.T) {
	store := release.NewMemory()
	var started string
	builder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/releases/ready":
			_, _ = w.Write([]byte(`{"configured":true}`))
		default:
			started = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer builder.Close()
	authStore := auth.NewMemory()
	h := auth.RequireSession(authStore, nil, NewHandler(store, builder.URL, nil))
	cookie := session(t, authStore)
	rec := post(t, h, cookie, "/api/releases", `{"service":"admin","ref":"v1.2.3"}`)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"id":1`) || started != "/v1/releases/1/start" {
		t.Fatalf("publish = %d %s started=%s", rec.Code, rec.Body.String(), started)
	}
	rec = post(t, h, cookie, "/api/releases", `{"service":"core","ref":"v1.2.4"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "已有发布在进行") {
		t.Fatalf("second = %d %s", rec.Code, rec.Body.String())
	}
}

func TestStartFailureMarksTheTaskFailed(t *testing.T) {
	store := release.NewMemory()
	builder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/releases/ready" {
			_, _ = w.Write([]byte(`{"configured":true}`))
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer builder.Close()
	authStore := auth.NewMemory()
	h := auth.RequireSession(authStore, nil, NewHandler(store, builder.URL, time.Now))
	rec := post(t, h, session(t, authStore), "/api/releases", `{"service":"core","ref":"v1.2.3"}`)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), `"id":1`) {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	got, err := store.Get(context.Background(), 1)
	if err != nil || got.Status != release.StatusFailed || got.Error != "builder 不可达" {
		t.Fatalf("task = %+v %v", got, err)
	}
}

func TestRollbackAndRecreate(t *testing.T) {
	store := release.NewMemory()
	h, cookie := handlerWith(t, store, true)
	rec := post(t, h, cookie, "/api/releases/rollback", `{"service":"core"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "没有可回滚的镜像") {
		t.Fatalf("rollback = %d %s", rec.Code, rec.Body.String())
	}
	failed := store.Seed(release.Task{Service: "core", Status: release.StatusFailed, ImageID: ""})
	rec = post(t, h, cookie, "/api/releases/"+strconv.FormatUint(failed.ID, 10)+"/recreate", `{}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "没有可重建的镜像") {
		t.Fatalf("empty recreate = %d %s", rec.Code, rec.Body.String())
	}
	withImage := store.Seed(release.Task{Service: "core", Kind: release.KindPublish, Status: release.StatusFailed, ImageID: "sha256:abc", GitRef: "v1"})
	rec = post(t, h, cookie, "/api/releases/"+strconv.FormatUint(withImage.ID, 10)+"/recreate", `{}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("recreate = %d %s", rec.Code, rec.Body.String())
	}
	succeeded := store.Seed(release.Task{Service: "webhook", Status: release.StatusSucceeded, ImageID: "sha256:old"})
	_ = succeeded
	// The recreate above is still queued, so rollback must conflict.
	rec = post(t, h, cookie, "/api/releases/rollback", `{"service":"webhook"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("busy rollback = %d %s", rec.Code, rec.Body.String())
	}
}

func TestRefsProxiesTheRemoteList(t *testing.T) {
	h, cookie := handlerWith(t, release.NewMemory(), true)
	req := httptest.NewRequest(http.MethodGet, "/api/releases/refs", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ref":"v0.0.2"`) {
		t.Fatalf("refs = %d %s", rec.Code, rec.Body.String())
	}
}

func TestVersionsListsBuiltImages(t *testing.T) {
	h, cookie := handlerWith(t, release.NewMemory(), false)
	req := httptest.NewRequest(http.MethodGet, "/api/releases/versions?service=admin", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"tag":"task5"`) || !strings.Contains(rec.Body.String(), `"tag":"latest"`) {
		t.Fatalf("versions = %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/releases/versions?service=mysql", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "服务名不在允许范围内") {
		t.Fatalf("service = %d %s", rec.Code, rec.Body.String())
	}
}

func TestDeployUsesABuiltImageWithoutGitCredentials(t *testing.T) {
	store := release.NewMemory()
	h, cookie := handlerWith(t, store, false)
	rec := post(t, h, cookie, "/api/releases/deploy", `{"service":"admin","image_id":"sha256:nope"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "没有可重建的镜像") {
		t.Fatalf("missing = %d %s", rec.Code, rec.Body.String())
	}
	if items, _ := store.List(context.Background(), "admin", 20); len(items) != 0 {
		t.Fatalf("created %+v", items)
	}
	image := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	rec = post(t, h, cookie, "/api/releases/deploy", `{"service":"admin","image_id":"`+image+`"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("deploy = %d %s", rec.Code, rec.Body.String())
	}
	items, err := store.List(context.Background(), "admin", 20)
	if err != nil || len(items) != 1 || items[0].Kind != release.KindRecreate || items[0].ImageID != image || items[0].GitRef != "" {
		t.Fatalf("task = %+v err=%v", items, err)
	}
}

func TestListExpiresAndReportsRollback(t *testing.T) {
	store := release.NewMemory()
	store.Seed(release.Task{Service: "core", Status: release.StatusRunning, CreatedAt: time.Now().Add(-release.Timeout - time.Minute)})
	store.Seed(release.Task{Service: "core", Status: release.StatusSucceeded, ImageID: "sha256:abc", GitRef: "v1"})
	h, cookie := handlerWith(t, store, true)
	req := httptest.NewRequest(http.MethodGet, "/api/releases?service=core", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `"error":"超时"`) || !strings.Contains(body, `"rollback":true`) || !strings.Contains(body, `"active":false`) {
		t.Fatalf("list = %d %s", rec.Code, body)
	}
}

func handlerWith(t *testing.T, store *release.Memory, configured bool) (http.Handler, *http.Cookie) {
	t.Helper()
	builder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/releases/ready":
			if configured {
				_, _ = w.Write([]byte(`{"configured":true}`))
				return
			}
			_, _ = w.Write([]byte(`{"configured":false}`))
			return
		case "/v1/releases/refs":
			_, _ = w.Write([]byte(`{"refs":[{"name":"v0.0.2","ref":"v0.0.2","kind":"tag","summary":"","committed_at":""}]}`))
		case "/v1/releases/images":
			if r.URL.Query().Get("service") == "admin" {
				_, _ = w.Write([]byte(`{"versions":[{"tag":"latest","image_id":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","created_at":"2026-11-15T08:00:00Z"},{"tag":"task5","image_id":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","created_at":"2026-11-14T08:00:00Z"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"versions":[{"tag":"latest","image_id":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","created_at":"2026-11-15T08:00:00Z"}]}`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(builder.Close)
	authStore := auth.NewMemory()
	return auth.RequireSession(authStore, nil, NewHandler(store, builder.URL, time.Now)), session(t, authStore)
}

func session(t *testing.T, store auth.Store) *http.Cookie {
	t.Helper()
	raw := []byte("0123456789abcdef0123456789abcdef")
	sum := sha256.Sum256(raw)
	if err := store.CreateSession(context.Background(), sum[:], time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: auth.CookieName, Value: hex.EncodeToString(raw)}
}

func post(t *testing.T, h http.Handler, cookie *http.Cookie, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
