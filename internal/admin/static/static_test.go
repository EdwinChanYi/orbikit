package static

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIndexExists(t *testing.T) {
	h := Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if len(body) == 0 {
		t.Fatal("empty index")
	}
}
