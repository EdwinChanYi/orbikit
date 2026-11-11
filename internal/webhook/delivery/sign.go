package delivery

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"strconv"
	"time"
)

// Request header names of a webhook call. They are part of the contract with
// receivers; net/http sends them in canonical form (App-Id, Cur-Time, ...).
const (
	HeaderContentType = "content-type"
	HeaderAppID       = "app-id"
	HeaderNonce       = "nonce"
	HeaderCurTime     = "cur-time"
	HeaderCheckSum    = "check-sum"
)

const (
	nonceAlphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ+/"
	nonceLength   = 64
)

// CheckSum returns lowercase hex(SHA1(appSecret + nonce + curTime + body)).
// SHA1 is fixed by the receiver contract.
func CheckSum(appSecret, nonce, curTime string, body []byte) string {
	h := sha1.New()
	h.Write([]byte(appSecret))
	h.Write([]byte(nonce))
	h.Write([]byte(curTime))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// NewNonce returns 64 random characters from [0-9a-zA-Z+/].
func NewNonce() string {
	b := make([]byte, nonceLength)
	if _, err := rand.Read(b); err != nil {
		panic("delivery: crypto/rand unavailable: " + err.Error())
	}
	for i := range b {
		b[i] = nonceAlphabet[int(b[i])%len(nonceAlphabet)]
	}
	return string(b)
}

// NewSignedRequest builds the POST request for one webhook call, signed with
// a fresh nonce and the current Unix time in seconds.
func NewSignedRequest(ctx context.Context, url, appID, appSecret string, body []byte, now time.Time) (*http.Request, error) {
	return newSignedRequest(ctx, url, appID, appSecret, body, NewNonce(), strconv.FormatInt(now.Unix(), 10))
}

func newSignedRequest(ctx context.Context, url, appID, appSecret string, body []byte, nonce, curTime string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set(HeaderContentType, "application/json")
	req.Header.Set(HeaderAppID, appID)
	req.Header.Set(HeaderNonce, nonce)
	req.Header.Set(HeaderCurTime, curTime)
	req.Header.Set(HeaderCheckSum, CheckSum(appSecret, nonce, curTime, body))
	return req, nil
}
