package status

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EdwinChanYi/orbikit/internal/admin/auth"
)

type stampReader struct{}

func (stampReader) Last(_ context.Context, service string) (*ReleaseStamp, error) {
	if service != "core" {
		return nil, nil
	}
	return &ReleaseStamp{Ref: "v1.2.3", FinishedAt: time.Date(2026, 11, 15, 15, 10, 0, 0, time.FixedZone("CST", 8*3600))}, nil
}

func TestOverviewShowsTheLatestRelease(t *testing.T) {
	builder := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer builder.Close()
	h := NewHandler(View{BuilderURL: builder.URL, Source: fakeSource{snap: Cluster{Status: "ok"}}, Releases: stampReader{}})
	store := auth.NewMemory()
	cookie := operatorCookie(t, store, time.Now().Add(time.Hour))
	mux := http.NewServeMux()
	mux.Handle("/api/overview", auth.RequireSession(store, nil, h))
	req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `"ref":"v1.2.3"`) || strings.Count(body, `"last_release":null`) != 3 {
		t.Fatalf("overview = %d %s", rec.Code, body)
	}
}

func TestStatusRoutes(t *testing.T) {
	builder := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	builderURL := builder.URL
	builder.Close()

	view := View{
		BuilderURL: builderURL,
		Source: fakeSource{snap: Cluster{
			Status: "fault",
			Error:  "dial tcp: connection refused",
		}},
	}
	statusHandler := NewHandler(view)
	store := auth.NewMemory()
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	mux := http.NewServeMux()
	mux.Handle("/api/overview", auth.RequireSession(store, clock, statusHandler))
	mux.Handle("/api/cluster", auth.RequireSession(store, clock, statusHandler))
	mux.Handle("/api/", auth.NewHandler(store, []byte("secret"), clock))

	t.Run("overview requires a session", func(t *testing.T) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if strings.TrimSpace(rec.Body.String()) != `{"error":"登录失败"}` {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})

	cookie := operatorCookie(t, store, now.Add(time.Hour))

	t.Run("overview stays available when the builder is down", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, `"name":"core"`) || !strings.Contains(body, `"last_release":null`) {
			t.Fatalf("body = %s", body)
		}
		if strings.Contains(body, `"last_release":""`) {
			t.Fatalf("last_release encoded as empty string: %s", body)
		}
		var overview Overview
		if err := json.Unmarshal(rec.Body.Bytes(), &overview); err != nil {
			t.Fatal(err)
		}
		if len(overview.Services) != 4 {
			t.Fatalf("rows = %d, body = %s", len(overview.Services), body)
		}
		for i, svc := range Services {
			row := overview.Services[i]
			if row.Name != svc.Name {
				t.Fatalf("row %d = %s, want %s", i, row.Name, svc.Name)
			}
			if row.Container != "unknown" {
				t.Fatalf("%s container = %s, want unknown", row.Name, row.Container)
			}
		}
	})

	t.Run("cluster fault is encoded with null instances", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/cluster", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, `"status":"fault"`) || !strings.Contains(body, `"instances":null`) {
			t.Fatalf("body = %s", body)
		}
		if !strings.Contains(body, `"error":"dial tcp: connection refused"`) {
			t.Fatalf("fault snapshot was rewritten: %s", body)
		}
		var cluster Cluster
		if err := json.Unmarshal(rec.Body.Bytes(), &cluster); err != nil {
			t.Fatal(err)
		}
		if cluster.Status != "fault" || cluster.Instances != nil {
			t.Fatalf("cluster = %+v", cluster)
		}
	})
}

type hangingSource struct{}

func (hangingSource) Cluster(ctx context.Context) (Cluster, error) {
	<-ctx.Done()
	return Interpret("", 0, nil, nil, ctx.Err()), nil
}

func TestClusterReadReturnsFaultWhenEtcdHangs(t *testing.T) {
	handler := NewHandler(View{Source: hangingSource{}})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/cluster", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var cluster Cluster
	if err := json.Unmarshal(rec.Body.Bytes(), &cluster); err != nil {
		t.Fatal(err)
	}
	if cluster.Status != "fault" || cluster.Instances != nil {
		t.Fatalf("cluster = %+v", cluster)
	}
}

func operatorCookie(t *testing.T, store auth.Store, expires time.Time) *http.Cookie {
	t.Helper()
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	sum := sha256.Sum256(raw)
	if err := store.CreateSession(context.Background(), sum[:], expires); err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: auth.CookieName, Value: hex.EncodeToString(raw)}
}
