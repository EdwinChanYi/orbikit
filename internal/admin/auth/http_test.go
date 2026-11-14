package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestLoginRejectsWrongPasswordWithSameBody(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemory()
	if err := store.EnsureOperator(context.Background(), hash); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(store, []byte("secret"), time.Now)
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"password":"nope"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != `{"error":"登录失败"}` {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if rec.Result().Cookies() != nil && len(rec.Result().Cookies()) > 0 {
		t.Fatal("failed login set a cookie")
	}
}

func TestLoginThenMe(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemory()
	if err := store.EnsureOperator(context.Background(), hash); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(store, []byte("secret"), func() time.Time { return time.Unix(0, 0) })
	login := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"password":"secret"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, login)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login status = %d, body %s", rec.Code, rec.Body.String())
	}
	cookie := rec.Result().Cookies()
	if len(cookie) != 1 || cookie[0].Name != CookieName || !cookie[0].HttpOnly {
		t.Fatalf("cookie = %+v", cookie)
	}
	me := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	me.AddCookie(cookie[0])
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, me)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"username":"admin"}` {
		t.Fatalf("me = %d %s", rec.Code, rec.Body.String())
	}
}

// errDeleteStore delegates to Memory and fails only DeleteSession.
type errDeleteStore struct {
	Store
}

func (errDeleteStore) DeleteSession(context.Context, []byte) error {
	return errors.New("delete session")
}

func TestLogoutKeepsCookieWhenDeleteFails(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	mem := NewMemory()
	if err := mem.EnsureOperator(context.Background(), hash); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(errDeleteStore{Store: mem}, []byte("secret"), func() time.Time { return time.Unix(0, 0) })
	login := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"password":"secret"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, login)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login status = %d, body %s", rec.Code, rec.Body.String())
	}
	cookie := rec.Result().Cookies()
	if len(cookie) != 1 || cookie[0].Value == "" {
		t.Fatalf("cookie = %+v", cookie)
	}
	logout := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	logout.AddCookie(cookie[0])
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, logout)
	if rec.Code != http.StatusUnauthorized || strings.TrimSpace(rec.Body.String()) != `{"error":"登录失败"}` {
		t.Fatalf("logout = %d %s", rec.Code, rec.Body.String())
	}
	if cleared := rec.Result().Cookies(); len(cleared) != 0 {
		t.Fatalf("delete failure cleared cookie: %+v", cleared)
	}
}

func TestRequireSession(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store := NewMemory()
	var called bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := RequireSession(store, func() time.Time { return now }, next)

	t.Run("no cookie", func(t *testing.T) {
		called = false
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
		assertUnauthorized(t, rec)
		if called {
			t.Fatal("next was called")
		}
	})

	t.Run("malformed cookie", func(t *testing.T) {
		called = false
		req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
		req.AddCookie(&http.Cookie{Name: CookieName, Value: "not-a-session"})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assertUnauthorized(t, rec)
		if called {
			t.Fatal("next was called")
		}
	})

	t.Run("expired session", func(t *testing.T) {
		called = false
		req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
		req.AddCookie(sessionCookie(t, store, now.Add(-time.Second)))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assertUnauthorized(t, rec)
		if called {
			t.Fatal("next was called")
		}
	})

	t.Run("store error", func(t *testing.T) {
		broken := RequireSession(errValidStore{Store: store}, func() time.Time { return now }, next)
		req := httptest.NewRequest(http.MethodGet, "/api/cluster", nil)
		req.AddCookie(sessionCookie(t, store, now.Add(time.Hour)))
		rec := httptest.NewRecorder()
		broken.ServeHTTP(rec, req)
		assertUnauthorized(t, rec)
	})

	t.Run("valid session", func(t *testing.T) {
		called = false
		req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
		req.AddCookie(sessionCookie(t, store, now.Add(time.Hour)))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if !called {
			t.Fatal("next was not called")
		}
	})

	t.Run("nil clock uses the current time", func(t *testing.T) {
		called = false
		live := RequireSession(store, nil, next)
		req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
		req.AddCookie(sessionCookie(t, store, time.Now().Add(time.Hour)))
		rec := httptest.NewRecorder()
		live.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !called {
			t.Fatalf("status = %d called %v", rec.Code, called)
		}
	})
}

// errValidStore delegates to Store and fails only SessionValid.
type errValidStore struct {
	Store
}

func (errValidStore) SessionValid(context.Context, []byte, time.Time) (bool, error) {
	return false, errors.New("session lookup")
}

func sessionCookie(t *testing.T, store Store, expires time.Time) *http.Cookie {
	t.Helper()
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(expires.UnixNano() + int64(i))
	}
	sum := sha256.Sum256(raw)
	if err := store.CreateSession(context.Background(), sum[:], expires); err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: CookieName, Value: hex.EncodeToString(raw)}
}

func assertUnauthorized(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != `{"error":"登录失败"}` {
		t.Fatalf("body = %s", rec.Body.String())
	}
}
