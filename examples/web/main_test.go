package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJoinRejectsBlankNickname(t *testing.T) {
	d := &demo{rooms: map[int64]struct{}{}}
	req := httptest.NewRequest(http.MethodPost, "/api/join", strings.NewReader(`{"nickname":"   "}`))
	rec := httptest.NewRecorder()
	d.join(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
}
