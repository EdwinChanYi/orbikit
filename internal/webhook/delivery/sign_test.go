package delivery

import (
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCheckSumKnownVector(t *testing.T) {
	// Computed independently: SHA1("mysecret" + "abcNONCE+/" + "1700000000" + body), lowercase hex.
	body := []byte(`{"app_id":"app1","msg_type":1,"text_content":"hi"}`)
	got := CheckSum("mysecret", "abcNONCE+/", "1700000000", body)
	const want = "5baceeaa467712c150694a6dea8474785dd6ace6"
	if got != want {
		t.Fatalf("CheckSum = %s, want %s", got, want)
	}
}

func TestCheckSumMatchesConcatenationRule(t *testing.T) {
	cases := [][4]string{
		{"", "", "", ""},
		{"s", "n", "1", "{}"},
		{"secret-中文", strings.Repeat("x", 64), "1999999999", `{"custom_content":"AAEC"}`},
	}
	for _, c := range cases {
		want := fmt.Sprintf("%x", sha1.Sum([]byte(c[0]+c[1]+c[2]+c[3])))
		if got := CheckSum(c[0], c[1], c[2], []byte(c[3])); got != want {
			t.Errorf("CheckSum(%q) = %s, want %s", c, got, want)
		}
	}
}

func TestNewNonce(t *testing.T) {
	n := NewNonce()
	if len(n) != 64 {
		t.Fatalf("nonce length = %d, want 64", len(n))
	}
	for _, r := range n {
		if !strings.ContainsRune(nonceAlphabet, r) {
			t.Fatalf("nonce contains %q outside [0-9a-zA-Z+/]", r)
		}
	}
	if n == NewNonce() {
		t.Fatal("nonces must differ")
	}
}

func TestSignedRequestHeaders(t *testing.T) {
	body := []byte(`{"app_id":"app1"}`)
	req, err := newSignedRequest(context.Background(), "http://example.com/cb", "app1", "sec", body, "NONCE", "1700000000")
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %s", req.Method)
	}
	want := map[string]string{
		"Content-Type": "application/json",
		"App-Id":       "app1",
		"Nonce":        "NONCE",
		"Cur-Time":     "1700000000",
		"Check-Sum":    CheckSum("sec", "NONCE", "1700000000", body),
	}
	for k, v := range want {
		if got := req.Header.Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
	got, _ := io.ReadAll(req.Body)
	if string(got) != string(body) {
		t.Fatalf("body = %s", got)
	}
}

// TestReceiverCanVerify plays the receiver: it recomputes the checksum from
// the raw body and headers exactly as the contract documents.
func TestReceiverCanVerify(t *testing.T) {
	const secret = "app-secret"
	now := time.Unix(1700000123, 0)
	verified := make(chan bool, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		curTime := r.Header.Get("cur-time")
		sum := fmt.Sprintf("%x", sha1.Sum([]byte(secret+r.Header.Get("nonce")+curTime+string(raw))))
		ts, err := strconv.ParseInt(curTime, 10, 64)
		verified <- err == nil && ts == now.Unix() && sum == r.Header.Get("check-sum") &&
			r.Header.Get("app-id") == "app1" && len(r.Header.Get("nonce")) == 64
	}))
	defer srv.Close()

	req, err := NewSignedRequest(context.Background(), srv.URL, "app1", secret, []byte(`{"a":1}`), now)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if !<-verified {
		t.Fatal("receiver could not verify the signature")
	}
}
