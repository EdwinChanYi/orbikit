package status

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type scriptedHealth struct {
	calls []string
	state map[string]string
	fail  map[string]error
}

func (s *scriptedHealth) Check(_ context.Context, addr string) (string, error) {
	s.calls = append(s.calls, addr)
	if err := s.fail[addr]; err != nil {
		return "", err
	}
	return s.state[addr], nil
}

func TestCollect(t *testing.T) {
	t.Run("core running and gateway unknown", func(t *testing.T) {
		var paths []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("method = %s", r.Method)
			}
			paths = append(paths, r.URL.Path)
			switch r.URL.Path {
			case "/v1/containers/core":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"name":"core","status":"running","running":true,"image_id":"sha256:abc"}`))
			case "/v1/containers/chatroom":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"name":"chatroom","status":"missing","running":false,"image_id":"sha256:miss"}`))
			case "/v1/containers/webhook":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"name":"webhook","status":"exited","running":false,"image_id":"sha256:old"}`))
			case "/v1/containers/gateway":
				http.Error(w, "boom", http.StatusInternalServerError)
			default:
				t.Errorf("path = %s", r.URL.Path)
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()

		health := &scriptedHealth{
			state: map[string]string{"core:9001": "serving"},
			fail:  map[string]error{"webhook:9004": context.DeadlineExceeded},
		}
		cluster := Cluster{
			Status: "ok",
			Instances: []Instance{{
				Service: "core",
				ID:      "abc",
				Addr:    "10.0.0.1:9001",
			}},
		}

		got := Collect(context.Background(), srv.URL, health, cluster)
		if len(got.Services) != len(Services) {
			t.Fatalf("rows = %d, want %d", len(got.Services), len(Services))
		}
		for i, svc := range Services {
			if got.Services[i].Name != svc.Name {
				t.Fatalf("row %d = %s, want %s", i, got.Services[i].Name, svc.Name)
			}
		}

		core := findRow(t, got, "core")
		if core.Container != "running" || core.GRPC != "serving" || core.ImageID != "sha256:abc" {
			t.Fatalf("core = %+v", core)
		}
		if len(core.Instances) != 1 || core.Instances[0].Addr != "10.0.0.1:9001" || core.Instances[0].ID != "abc" {
			t.Fatalf("instances = %+v", core.Instances)
		}
		if core.LastRelease != nil {
			t.Fatalf("last_release = %#v, want no release", core.LastRelease)
		}

		chatroom := findRow(t, got, "chatroom")
		if chatroom.Container != "missing" || chatroom.ImageID != "sha256:miss" {
			t.Fatalf("chatroom = %+v", chatroom)
		}
		if chatroom.Instances == nil || len(chatroom.Instances) != 0 {
			t.Fatalf("chatroom instances = %#v, want empty", chatroom.Instances)
		}
		if chatroom.GRPC != "not_serving" {
			t.Fatalf("chatroom grpc = %q", chatroom.GRPC)
		}

		webhook := findRow(t, got, "webhook")
		if webhook.Container != "exited" || webhook.GRPC != "unknown" {
			t.Fatalf("webhook = %+v", webhook)
		}

		gateway := findRow(t, got, "gateway")
		if gateway.Container != "unknown" || gateway.GRPC != "skipped" || gateway.ImageID != "" {
			t.Fatalf("gateway = %+v", gateway)
		}
		if gateway.Instances == nil {
			t.Fatal("gateway instances = nil, want empty")
		}
		for _, addr := range health.calls {
			if addr == "" {
				t.Fatalf("dialed empty grpc address: %v", health.calls)
			}
		}
		if !contains(health.calls, "core:9001") || contains(health.calls, "") {
			t.Fatalf("health calls = %v", health.calls)
		}

		assertLastReleaseNull(t, got)

		cluster.Status = "fault"
		faulted := Collect(context.Background(), srv.URL, health, cluster)
		for _, svc := range Services {
			row := findRow(t, faulted, svc.Name)
			if svc.Register {
				if row.Instances != nil {
					t.Fatalf("%s instances = %#v, want nil", svc.Name, row.Instances)
				}
				continue
			}
			if row.Instances == nil {
				t.Fatalf("%s instances = nil, want empty", svc.Name)
			}
		}
		if len(paths) == 0 || paths[0] != "/v1/containers/core" {
			t.Fatalf("paths = %v", paths)
		}
	})

	t.Run("running flag wins and other states map", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/v1/containers/core":
				_, _ = w.Write([]byte(`{"status":"missing","running":true,"image_id":"sha256:abc"}`))
			case "/v1/containers/chatroom":
				_, _ = w.Write([]byte(`{"status":"missing","running":false,"image_id":"sha256:miss"}`))
			case "/v1/containers/webhook":
				_, _ = w.Write([]byte(`{"status":"dead","running":false,"image_id":"sha256:dead"}`))
			default:
				http.Error(w, "nope", http.StatusBadGateway)
			}
		}))
		defer srv.Close()

		health := &scriptedHealth{state: map[string]string{
			"core:9001":     "serving",
			"chatroom:9002": "SERVING",
			"webhook:9004":  "not_serving",
		}}
		got := Collect(context.Background(), srv.URL, health, Cluster{Status: "ok"})
		if findRow(t, got, "core").Container != "running" {
			t.Fatalf("core container = %s", findRow(t, got, "core").Container)
		}
		if findRow(t, got, "chatroom").Container != "missing" || findRow(t, got, "chatroom").GRPC != "not_serving" {
			t.Fatalf("chatroom = %+v", findRow(t, got, "chatroom"))
		}
		if findRow(t, got, "webhook").Container != "exited" {
			t.Fatalf("webhook container = %s", findRow(t, got, "webhook").Container)
		}
		if findRow(t, got, "gateway").Container != "unknown" || findRow(t, got, "gateway").GRPC != "skipped" {
			t.Fatalf("gateway = %+v", findRow(t, got, "gateway"))
		}
		for _, addr := range health.calls {
			if addr == "" {
				t.Fatal("dialed skipped address")
			}
		}
	})

	t.Run("builder unreachable still returns every row", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := srv.URL
		srv.Close()

		health := &scriptedHealth{state: map[string]string{"core:9001": "serving"}}
		got := Collect(context.Background(), url, health, Cluster{Status: "degraded", Instances: []Instance{{
			Service: "core", ID: "abc", Addr: "10.0.0.1:9001",
		}}})
		if len(got.Services) != 4 {
			t.Fatalf("rows = %d", len(got.Services))
		}
		for _, row := range got.Services {
			if row.Container != "unknown" {
				t.Fatalf("%s container = %s", row.Name, row.Container)
			}
		}
		core := findRow(t, got, "core")
		if core.GRPC != "serving" || len(core.Instances) != 1 || core.Instances[0].Addr != "10.0.0.1:9001" {
			t.Fatalf("core = %+v", core)
		}
		assertLastReleaseNull(t, got)
	})
}

func findRow(t *testing.T, overview Overview, name string) ServiceRow {
	t.Helper()
	for _, row := range overview.Services {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("missing row %s", name)
	return ServiceRow{}
}

func assertLastReleaseNull(t *testing.T, overview Overview) {
	t.Helper()
	body, err := json.Marshal(overview)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, `"last_release":""`) {
		t.Fatalf("last_release encoded as empty string: %s", text)
	}
	if strings.Count(text, `"last_release":null`) != len(overview.Services) {
		t.Fatalf("json = %s", text)
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
