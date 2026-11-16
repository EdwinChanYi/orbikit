package builder

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestImagesListsOnlyTheServiceTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[
			{"Id":"sha256:latest","RepoTags":["orbikit-admin:latest"],"Created":200},
			{"Id":"sha256:task5","RepoTags":["orbikit-admin:task5"],"Created":100},
			{"Id":"sha256:core","RepoTags":["orbikit-core:latest","postgres:16"],"Created":300},
			{"Id":"sha256:dangling","RepoTags":null,"Created":400}
		]`))
	}))
	defer srv.Close()

	versions, err := (&Docker{Base: srv.URL, Client: srv.Client()}).Images(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].Tag != "latest" || versions[0].ImageID != "sha256:latest" || versions[1].Tag != "task5" {
		t.Fatalf("versions = %+v", versions)
	}
}

func TestImageRouteRejectsOtherServices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"Id":"sha256:task5","RepoTags":["orbikit-admin:task5"],"Created":100}]`))
	}))
	defer srv.Close()
	dock := &Docker{Base: srv.URL, Client: srv.Client()}
	h := NewHandler(&fakeInspector{}, "orbikit", ReleaseEnv{}, dock)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/releases/images?service=mysql", nil))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "服务名不在允许范围内") {
		t.Fatalf("service = %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/releases/images?service=admin", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"tag":"task5"`) {
		t.Fatalf("images = %d %s", rec.Code, rec.Body.String())
	}

	closed := NewHandler(&fakeInspector{}, "orbikit", ReleaseEnv{}, nil)
	rec = httptest.NewRecorder()
	closed.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/releases/images?service=admin", nil))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "容器查询失败") {
		t.Fatalf("nil docker = %d %s", rec.Code, rec.Body.String())
	}
}
