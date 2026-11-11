package delivery

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testSender(maxRetries int) *Sender {
	return NewSender(SenderConfig{
		Timeout:         200 * time.Millisecond,
		MaxRetries:      maxRetries,
		RetryBackoff:    5 * time.Millisecond,
		MaxRetryBackoff: 20 * time.Millisecond,
	})
}

func statusServer(codes ...int) (*httptest.Server, *atomic.Int32) {
	var calls atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := int(calls.Add(1)) - 1
		code := codes[len(codes)-1]
		if n < len(codes) {
			code = codes[n]
		}
		w.WriteHeader(code)
	})), &calls
}

func TestSenderRetriesServerErrors(t *testing.T) {
	srv, calls := statusServer(500, 503, 200)
	defer srv.Close()
	if err := testSender(2).Post(context.Background(), srv.URL, "a", "s", []byte("{}")); err != nil {
		t.Fatalf("Post = %v, want success on third attempt", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
}

func TestSenderGivesUpAfterMaxRetries(t *testing.T) {
	srv, calls := statusServer(500)
	defer srv.Close()
	err := testSender(2).Post(context.Background(), srv.URL, "a", "s", []byte("{}"))
	var se *StatusError
	if !errors.As(err, &se) || se.StatusCode != 500 {
		t.Fatalf("Post = %v, want StatusError 500", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 1 + 2 retries", calls.Load())
	}
}

func TestSenderDoesNotRetryClientErrors(t *testing.T) {
	srv, calls := statusServer(400)
	defer srv.Close()
	if err := testSender(3).Post(context.Background(), srv.URL, "a", "s", []byte("{}")); err == nil {
		t.Fatal("400 must fail")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestSenderRetries429(t *testing.T) {
	srv, calls := statusServer(429, 204)
	defer srv.Close()
	if err := testSender(1).Post(context.Background(), srv.URL, "a", "s", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestSenderTimeoutIsRetriedThenFails(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer once.Do(func() { close(release) })

	s := NewSender(SenderConfig{Timeout: 30 * time.Millisecond, MaxRetries: 1, RetryBackoff: time.Millisecond})
	start := time.Now()
	if err := s.Post(context.Background(), srv.URL, "a", "s", []byte("{}")); err == nil {
		t.Fatal("hanging receiver must time out")
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("timeouts not enforced: %v", d)
	}
}

func TestSenderStopsOnContextCancel(t *testing.T) {
	srv, calls := statusServer(500)
	defer srv.Close()
	s := NewSender(SenderConfig{Timeout: time.Second, MaxRetries: 10, RetryBackoff: time.Second, MaxRetryBackoff: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := s.Post(ctx, srv.URL, "a", "s", []byte("{}")); err == nil {
		t.Fatal("cancelled post must fail")
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("backoff ignored cancellation: %v", d)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}
