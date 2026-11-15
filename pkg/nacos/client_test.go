package nacos

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDataIDRejectsUnknownService(t *testing.T) {
	if _, err := DataID("mysql"); err == nil || !strings.Contains(err.Error(), "服务名不在允许范围内") {
		t.Fatalf("err = %v", err)
	}
	for _, service := range []string{"admin", "builder"} {
		id, err := DataID(service)
		if err != nil || id != service+".yaml" {
			t.Fatalf("%s id = %q, err = %v", service, id, err)
		}
	}
	id, err := DataID("core")
	if err != nil || id != "core.yaml" {
		t.Fatalf("id = %q, err = %v", id, err)
	}
}

func TestGetPublishHistoryBetaListeners(t *testing.T) {
	var published, betaIPs string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nacos/v1/auth/login" {
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse login: %v", err)
			}
			if r.Form.Get("username") != "nacos" || r.Form.Get("password") != "nacos" {
				t.Errorf("login form = %v", r.Form)
			}
			_, _ = w.Write([]byte(`{"accessToken":"tok","tokenTtl":18000}`))
			return
		}
		if r.URL.Query().Get("accessToken") != "tok" {
			t.Errorf("token query = %q", r.URL.RawQuery)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/nacos/v1/cs/configs" && r.URL.Query().Get("beta") == "":
			if r.URL.Query().Get("dataId") != "core.yaml" || r.URL.Query().Get("group") != "orbikit" {
				t.Errorf("get query = %s", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, "log:\n  level: debug\n")
		case r.Method == http.MethodPost && r.URL.Path == "/nacos/v1/cs/configs" && r.FormValue("beta") == "":
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse publish: %v", err)
			}
			if r.Form.Get("dataId") != "core.yaml" || r.Form.Get("group") != Group || r.Form.Get("type") != "yaml" || r.Form.Get("content") != "log:" {
				t.Errorf("publish form = %v", r.Form)
			}
			published = r.Form.Get("content")
			_, _ = io.WriteString(w, "true")
		case r.Method == http.MethodGet && r.URL.Path == "/nacos/v1/cs/history":
			q := r.URL.Query()
			if q.Get("search") != "accurate" || q.Get("dataId") != "core.yaml" || q.Get("group") != Group || q.Get("pageNo") != "1" || q.Get("pageSize") != "20" {
				t.Errorf("history query = %s", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"totalCount":1,"pageItems":[{"id":"9","content":"log:\n  level: info\n","lastModifiedTime":"2026-11-15 00:00:00"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/nacos/v1/cs/configs" && r.FormValue("beta") == "true":
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse beta: %v", err)
			}
			betaIPs = r.Form.Get("betaIps")
			_, _ = io.WriteString(w, "true")
		case r.Method == http.MethodGet && r.URL.Path == "/nacos/v1/cs/configs" && r.URL.Query().Get("beta") == "true":
			_, _ = io.WriteString(w, `{"content":"gray","betaIps":"10.0.0.8,10.0.0.9"}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/nacos/v1/cs/configs":
			if r.URL.Query().Get("beta") != "true" || r.URL.Query().Get("dataId") != "core.yaml" {
				t.Errorf("delete query = %s", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, "true")
		case r.Method == http.MethodGet && r.URL.Path == "/nacos/v1/cs/configs/listener":
			if r.URL.Query().Get("dataId") != "core.yaml" || r.URL.Query().Get("group") != Group {
				t.Errorf("listener query = %s", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `[{"ip":"10.0.0.3"}]`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.String())
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "nacos", "nacos")
	ctx := context.Background()
	got, err := c.Get(ctx, "core.yaml")
	if err != nil || got != "log:\n  level: debug\n" {
		t.Fatalf("get = %q, err = %v", got, err)
	}
	if err := c.Publish(ctx, "core.yaml", "log:"); err != nil || published != "log:" {
		t.Fatalf("publish err = %v, content = %q", err, published)
	}
	revs, err := c.History(ctx, "core.yaml")
	if err != nil || len(revs) != 1 || revs[0].ID != "9" || revs[0].Content != "log:\n  level: info\n" || revs[0].Modified != "2026-11-15 00:00:00" {
		t.Fatalf("history = %+v, err = %v", revs, err)
	}
	if err := c.PublishBeta(ctx, "core.yaml", "gray", []string{"10.0.0.8", "10.0.0.9"}); err != nil || betaIPs != "10.0.0.8,10.0.0.9" {
		t.Fatalf("beta err = %v, ips = %q", err, betaIPs)
	}
	content, ips, err := c.Beta(ctx, "core.yaml")
	if err != nil || content != "gray" || len(ips) != 2 || ips[0] != "10.0.0.8" || ips[1] != "10.0.0.9" {
		t.Fatalf("beta get = %q %+v, err = %v", content, ips, err)
	}
	if err := c.DeleteBeta(ctx, "core.yaml"); err != nil {
		t.Fatal(err)
	}
	listeners, err := c.Listeners(ctx, "core.yaml")
	if err != nil || len(listeners) != 1 || listeners[0].IP != "10.0.0.3" {
		t.Fatalf("listeners = %+v, err = %v", listeners, err)
	}
}

func TestHistoryLoadsBodyByNid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nacos/v1/auth/login" {
			_, _ = io.WriteString(w, `{"accessToken":"tok","tokenTtl":18000}`)
			return
		}
		if r.URL.Query().Get("nid") == "1" {
			_, _ = io.WriteString(w, `{"id":"1","content":"log:\n  level: info\n"}`)
			return
		}
		_, _ = io.WriteString(w, `{"pageItems":[{"id":"1","content":null,"lastModifiedTime":"2026-11-15 00:00:00"}]}`)
	}))
	defer srv.Close()
	revs, err := New(srv.URL, "nacos", "nacos").History(context.Background(), "core.yaml")
	if err != nil || len(revs) != 1 || revs[0].ID != "1" || revs[0].Content != "log:\n  level: info\n" {
		t.Fatalf("history = %+v, err = %v", revs, err)
	}
}

func TestListenersReadsNacosStatusMap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nacos/v1/auth/login" {
			_, _ = io.WriteString(w, `{"accessToken":"tok","tokenTtl":18000}`)
			return
		}
		_, _ = io.WriteString(w, `{"collectStatus":200,"lisentersGroupkeyStatus":{"10.0.0.8":"abc"}}`)
	}))
	defer srv.Close()
	listeners, err := New(srv.URL, "nacos", "nacos").Listeners(context.Background(), "core.yaml")
	if err != nil || len(listeners) != 1 || listeners[0].IP != "10.0.0.8" {
		t.Fatalf("listeners = %+v, err = %v", listeners, err)
	}
}

func TestLoginRefreshesExpiredToken(t *testing.T) {
	var logins int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nacos/v1/auth/login" {
			logins++
			_, _ = io.WriteString(w, `{"accessToken":"tok","tokenTtl":1}`)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()
	c := New(srv.URL, "nacos", "nacos")
	if _, err := c.Get(context.Background(), "core.yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), "core.yaml"); err != nil {
		t.Fatal(err)
	}
	if logins != 2 {
		t.Fatalf("logins = %d", logins)
	}
}

func TestGetReportsMissingAndStatus(t *testing.T) {
	status := http.StatusNotFound
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nacos/v1/auth/login" {
			_, _ = w.Write([]byte(`{"accessToken":"tok","tokenTtl":18000}`))
			return
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	c := New(srv.URL, "nacos", "nacos")
	_, err := c.Get(context.Background(), "core.yaml")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
	status = http.StatusInternalServerError
	_, err = c.Get(context.Background(), "core.yaml")
	if err == nil || !strings.Contains(err.Error(), "nacos status 500") {
		t.Fatalf("err = %v", err)
	}
}

func TestPublishBetaRejectsBadIP(t *testing.T) {
	c := New("http://127.0.0.1:1", "nacos", "nacos")
	err := c.PublishBeta(context.Background(), "core.yaml", "gray", nil)
	if err == nil || !strings.Contains(err.Error(), "无效的灰度 IP") {
		t.Fatalf("empty err = %v", err)
	}
	err = c.PublishBeta(context.Background(), "core.yaml", "gray", []string{"not-an-ip"})
	if err == nil || !strings.Contains(err.Error(), "无效的灰度 IP") {
		t.Fatalf("bad err = %v", err)
	}
}
