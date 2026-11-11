package httpapi

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/xxtea/xxtea-go/xxtea"

	"github.com/EdwinChanYi/orbikit/internal/gateway/store"
)

type fakeUsers map[string]*store.UserInfo

func (f fakeUsers) LoadUserInfo(_ context.Context, token string) (*store.UserInfo, error) {
	if u, ok := f[token]; ok {
		return u, nil
	}
	return nil, store.ErrUnknownToken
}

var (
	testToken = strings.Repeat("t", tokenLen)
	testUser  = &store.UserInfo{AppID: "app", UID: 7, MaskKey: "0123456789abcdef0123456789abcdef"}
	testUUID  = func() (u [uuidLen]byte) { copy(u[:], strings.Repeat("u", uuidLen)); return }()
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	users := fakeUsers{testToken: testUser}
	for _, payload := range [][]byte{[]byte("hello"), nil} {
		body := Encode(0x5a, testToken, testUser.MaskKey, testUUID, payload)
		u, got, err := Decode(context.Background(), users, body)
		if err != nil {
			t.Fatalf("Decode(%q): %v", payload, err)
		}
		if u != testUser {
			t.Fatalf("user = %+v", u)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("payload = %q, want %q", got, payload)
		}
	}
}

func xorWrap(key byte, data []byte) []byte {
	out := []byte{key}
	for _, b := range data {
		out = append(out, b^key)
	}
	return out
}

func TestDecodeErrors(t *testing.T) {
	users := fakeUsers{testToken: testUser}
	valid := Encode(0x11, testToken, testUser.MaskKey, testUUID, []byte("x"))
	unknown := Encode(0x11, strings.Repeat("z", tokenLen), testUser.MaskKey, testUUID, []byte("x"))
	wrongKey := Encode(0x11, testToken, "another key", testUUID, []byte("x"))

	tests := []struct {
		name     string
		body     []byte
		reason   string
		wantUser bool
	}{
		{"empty", nil, "xor target not found", false},
		{"only xor key", []byte{1}, "xor target not found", false},
		{"no token", append([]byte{0}, make([]byte, tokenLen)...), "auth token not found", false},
		{"unknown token", unknown, "auth info not exist", false},
		{"wrong key", wrongKey, "xxtea decode error", true},
		{"truncated ciphertext", valid[:1+tokenLen+8], "xxtea decode error", true},
		{"short uuid", xorWrap(0x22, append([]byte(testToken), xxtea.Encrypt([]byte("short"), []byte(testUser.MaskKey))...)), "request uuid not found", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, _, err := Decode(context.Background(), users, tt.body)
			var de *DecodeError
			if !errors.As(err, &de) {
				t.Fatalf("err = %v, want DecodeError", err)
			}
			if de.Reason != tt.reason {
				t.Fatalf("reason = %q, want %q", de.Reason, tt.reason)
			}
			if (u != nil) != tt.wantUser {
				t.Fatalf("user = %v, want user %v", u, tt.wantUser)
			}
		})
	}
}
