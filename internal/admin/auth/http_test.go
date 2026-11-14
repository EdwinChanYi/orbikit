package auth

import (
	"context"
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
