package builder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	h := NewHandler(&fakeInspector{}, "orbikit")
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
	h := NewHandler(fake, "orbikit")
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
	h := NewHandler(&fakeInspector{err: context.DeadlineExceeded}, "orbikit")
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
