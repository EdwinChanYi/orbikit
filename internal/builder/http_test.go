package builder

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EdwinChanYi/orbikit/internal/release"
)

type fakeInspector struct {
	info Info
	err  error
	got  string
}

func (f *fakeInspector) Inspect(_ context.Context, container string) (Info, error) {
	f.got = container
	return f.info, f.err
}

func TestHandlerRejectsNamesOutsideWhitelist(t *testing.T) {
	h := NewHandler(&fakeInspector{}, "orbikit", ReleaseEnv{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/containers/admin", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "服务名不在允许范围内") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestHandlerReturnsContainerInfo(t *testing.T) {
	fake := &fakeInspector{info: Info{Status: "running", Running: true, ImageID: "sha256:abc"}}
	h := NewHandler(fake, "orbikit", ReleaseEnv{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/containers/core", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Name    string `json:"name"`
		Status  string `json:"status"`
		Running bool   `json:"running"`
		ImageID string `json:"image_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Name != "core" || body.Status != "running" || !body.Running || body.ImageID != "sha256:abc" {
		t.Fatalf("body = %+v", body)
	}
	if fake.got != "orbikit-core-1" {
		t.Fatalf("inspected %q", fake.got)
	}
}

func TestHandlerReportsInspectFailure(t *testing.T) {
	h := NewHandler(&fakeInspector{err: context.DeadlineExceeded}, "orbikit", ReleaseEnv{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/containers/core", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "容器查询失败") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestDockerInspectReadsState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Image":"sha256:abc","State":{"Status":"running","Running":true}}`))
	}))
	defer srv.Close()

	info, err := (&Docker{Base: srv.URL, Client: srv.Client()}).Inspect(context.Background(), "orbikit-core-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != "running" || !info.Running || info.ImageID != "sha256:abc" {
		t.Fatalf("info = %+v", info)
	}
}

func TestDockerInspectMissingContainer(t *testing.T) {
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.URL.Path
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	info, err := (&Docker{Base: srv.URL, Client: srv.Client()}).Inspect(context.Background(), "orbikit-core-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != "missing" || info.Running || info.ImageID != "" {
		t.Fatalf("info = %+v", info)
	}
	if path := <-got; path != "/containers/orbikit-core-1/json" {
		t.Fatalf("path = %q", path)
	}
}

func TestDockerInspectRejectsUnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusInternalServerError)
	}))
	defer srv.Close()

	info, err := (&Docker{Base: srv.URL, Client: srv.Client()}).Inspect(context.Background(), "orbikit-core-1")
	if err == nil || info.Status == "missing" {
		t.Fatalf("info = %+v, err = %v", info, err)
	}
}

func TestRefsRequiresCredentialsAndDoesNotEchoTheToken(t *testing.T) {
	previous := listRemoteRefs
	t.Cleanup(func() { listRemoteRefs = previous })
	called := false
	listRemoteRefs = func(_ context.Context, username, token string) ([]release.RemoteRef, error) {
		called = true
		if username != "git" || token != "secret-token" {
			t.Fatalf("credentials = %s %s", username, token)
		}
		return []release.RemoteRef{{Name: "v0.0.2", Ref: "v0.0.2", Kind: "tag"}}, nil
	}
	closed := NewHandler(&fakeInspector{}, "orbikit", ReleaseEnv{}, nil)
	rec := httptest.NewRecorder()
	closed.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/releases/refs", nil))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "缺少 Git 凭证") || called {
		t.Fatalf("missing = %d called=%v %s", rec.Code, called, rec.Body.String())
	}
	h := NewHandler(&fakeInspector{}, "orbikit", ReleaseEnv{Username: "git", Token: "secret-token"}, nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/releases/refs", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ref":"v0.0.2"`) || strings.Contains(rec.Body.String(), "secret-token") {
		t.Fatalf("refs = %d %s", rec.Code, rec.Body.String())
	}
}

func TestReadyDoesNotEchoTheToken(t *testing.T) {
	h := NewHandler(&fakeInspector{}, "orbikit", ReleaseEnv{Username: "git", Token: "secret-token"}, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/releases/ready", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"configured":true`) || strings.Contains(rec.Body.String(), "secret-token") {
		t.Fatalf("ready = %d %s", rec.Code, rec.Body.String())
	}
	empty := NewHandler(&fakeInspector{}, "orbikit", ReleaseEnv{}, nil)
	rec = httptest.NewRecorder()
	empty.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/releases/ready", nil))
	if !strings.Contains(rec.Body.String(), `"configured":false`) {
		t.Fatalf("empty = %s", rec.Body.String())
	}
}

func TestStartLaunchesTheOneShotAndReturns(t *testing.T) {
	var created, requestURI string
	var started bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/containers/create":
			body, _ := io.ReadAll(r.Body)
			created = string(body)
			requestURI = r.URL.RequestURI()
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"Id":"abc"}`))
		case r.URL.Path == "/containers/abc/start":
			started = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dock := &Docker{Base: srv.URL, Client: srv.Client(), Socket: "/var/run/docker.sock"}
	h := NewHandler(&fakeInspector{}, "orbikit", ReleaseEnv{Username: "git", Token: "secret-token", AdminDSN: "dsn", Image: "orbikit-builder", Network: "orbikit_default"}, dock)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/releases/7/start", nil))
	if rec.Code != http.StatusNoContent || !started {
		t.Fatalf("status = %d started=%v body=%s", rec.Code, started, rec.Body.String())
	}
	if !strings.Contains(requestURI, "name=orbikit-release-7") || !strings.Contains(created, `"Image":"orbikit-builder"`) {
		t.Fatalf("uri = %s create = %s", requestURI, created)
	}
	if !strings.Contains(created, `"-release","7"`) || !strings.Contains(created, `"AutoRemove":true`) || !strings.Contains(created, "secret-token") {
		t.Fatalf("create = %s", created)
	}
	if strings.Contains(rec.Body.String(), "secret-token") {
		t.Fatal("response leaked the token")
	}
}
