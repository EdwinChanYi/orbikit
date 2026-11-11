package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strconv"
	"testing"

	"github.com/xxtea/xxtea-go/xxtea"
)

func TestNewCodec(t *testing.T) {
	for _, name := range []string{CodecXXTEA, CodecPlain} {
		c, err := NewCodec(name)
		if err != nil || c.Name() != name {
			t.Fatalf("NewCodec(%q) = %v, %v", name, c, err)
		}
	}
	if _, err := NewCodec("lz4"); err == nil {
		t.Fatal("expected error for unknown codec")
	}
}

// legacyAuthBody builds an AUTH body exactly like the legacy client SDK.
func legacyAuthBody(maskNum uint64, req []byte) []byte {
	key := strconv.FormatUint(^maskNum, 10)
	body := make([]byte, 8)
	binary.BigEndian.PutUint64(body, maskNum)
	return append(body, xxtea.Encrypt(req, []byte(key))...)
}

func TestXXTEAOpenAuthLegacyCompatible(t *testing.T) {
	const maskNum = uint64(100)
	req := []byte("serialized AuthReq")

	got, seal, err := XXTEA{}.OpenAuth(legacyAuthBody(maskNum, req))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, req) {
		t.Fatalf("OpenAuth() = %q, want %q", got, req)
	}

	resp := []byte("serialized AuthResp")
	sealed := seal(resp)
	// The legacy client decrypts the response with decimal(maskNum).
	if plain := xxtea.Decrypt(sealed, []byte("100")); !bytes.Equal(plain, resp) {
		t.Fatalf("legacy client decoded %q, want %q", plain, resp)
	}
}

func TestXXTEAClientHelpers(t *testing.T) {
	c := XXTEA{}
	const maskNum = uint64(0xDEADBEEFCAFEBABE)
	req := []byte{1, 2, 3, 4, 5}

	body := c.SealAuthRequest(maskNum, req)
	if !bytes.Equal(body, legacyAuthBody(maskNum, req)) {
		t.Fatal("SealAuthRequest differs from legacy encoding")
	}
	got, seal, err := c.OpenAuth(body)
	if err != nil || !bytes.Equal(got, req) {
		t.Fatalf("OpenAuth() = %v, %v", got, err)
	}
	resp, err := c.OpenAuthResponse(maskNum, seal([]byte("resp")))
	if err != nil || string(resp) != "resp" {
		t.Fatalf("OpenAuthResponse() = %q, %v", resp, err)
	}
}

func TestXXTEAOpenAuthErrors(t *testing.T) {
	if _, _, err := (XXTEA{}).OpenAuth([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected error for short body")
	}
	body := legacyAuthBody(1, []byte("payload-payload"))
	body[0] ^= 0xFF // the key no longer matches
	if _, _, err := (XXTEA{}).OpenAuth(body); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("err = %v, want ErrDecrypt", err)
	}
}

func TestXXTEAOpenAuthEmptyRequest(t *testing.T) {
	req, _, err := XXTEA{}.OpenAuth(make([]byte, 8))
	if err != nil || len(req) != 0 {
		t.Fatalf("OpenAuth(empty) = %v, %v", req, err)
	}
}

func TestXXTEASealOpen(t *testing.T) {
	c := XXTEA{}
	key := "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ01"
	plain := []byte("chatroom message body")

	sealed := c.Seal(key, plain)
	if bytes.Equal(sealed, plain) {
		t.Fatal("Seal did not transform body")
	}
	if want := xxtea.Encrypt(plain, []byte(key)); !bytes.Equal(sealed, want) {
		t.Fatal("Seal differs from plain xxtea")
	}
	got, err := c.Open(key, sealed)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("Open() = %q, %v", got, err)
	}
	if _, err := c.Open("another-key-entirely", sealed); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("Open with wrong key err = %v, want ErrDecrypt", err)
	}
	if got, err := c.Open(key, nil); err != nil || len(got) != 0 {
		t.Fatalf("Open(empty) = %v, %v", got, err)
	}
	if got := c.Seal(key, nil); len(got) != 0 {
		t.Fatalf("Seal(empty) = %v", got)
	}
}

func TestPlainCodec(t *testing.T) {
	c := Plain{}
	req := []byte("AuthReq protobuf")
	got, seal, err := c.OpenAuth(req)
	if err != nil || !bytes.Equal(got, req) {
		t.Fatalf("OpenAuth() = %q, %v", got, err)
	}
	if out := seal([]byte("resp")); string(out) != "resp" {
		t.Fatalf("seal() = %q", out)
	}
	if out := c.Seal("k", []byte("body")); string(out) != "body" {
		t.Fatalf("Seal() = %q", out)
	}
	if out, err := c.Open("k", []byte("body")); err != nil || string(out) != "body" {
		t.Fatalf("Open() = %q, %v", out, err)
	}
}
