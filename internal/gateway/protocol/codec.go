package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"

	"github.com/xxtea/xxtea-go/xxtea"
)

// Codec encodes packet bodies. AUTH bodies use a bootstrap scheme because no
// session key exists yet; every later body uses the mask key that the AUTH
// response hands to the client.
type Codec interface {
	Name() string
	// OpenAuth extracts the serialized AuthReq from an AUTH body and returns a
	// function that encodes the matching AuthResp. sealResp may be non-nil
	// alongside an error so the failure can still be reported.
	OpenAuth(body []byte) (authReq []byte, sealResp func([]byte) []byte, err error)
	// Seal encodes a body with the session mask key.
	Seal(maskKey string, plain []byte) []byte
	// Open decodes a body sealed with the session mask key.
	Open(maskKey string, body []byte) ([]byte, error)
}

const (
	CodecXXTEA = "xxtea"
	CodecPlain = "plain"
)

// ErrDecrypt means a body could not be decrypted with the expected key.
var ErrDecrypt = errors.New("protocol: decrypt failed")

// NewCodec returns the codec registered under name.
func NewCodec(name string) (Codec, error) {
	switch name {
	case CodecXXTEA:
		return XXTEA{}, nil
	case CodecPlain:
		return Plain{}, nil
	default:
		return nil, fmt.Errorf("protocol: unknown codec %q", name)
	}
}

// MaskNumLen is the size of the big-endian maskNum prefix of an xxtea AUTH body.
const MaskNumLen = 8

// XXTEA is the codec used by legacy clients.
//
// AUTH request body: 8-byte big-endian maskNum followed by
// xxtea(AuthReq, decimal(^maskNum)). The AUTH response is
// xxtea(AuthResp, decimal(maskNum)). Other bodies are xxtea(pb, maskKey).
type XXTEA struct{}

func (XXTEA) Name() string { return CodecXXTEA }

func (XXTEA) OpenAuth(body []byte) ([]byte, func([]byte) []byte, error) {
	if len(body) < MaskNumLen {
		return nil, nil, fmt.Errorf("protocol: auth body shorter than %d bytes", MaskNumLen)
	}
	maskNum := binary.BigEndian.Uint64(body[:MaskNumLen])
	reqKey := []byte(strconv.FormatUint(^maskNum, 10))
	respKey := []byte(strconv.FormatUint(maskNum, 10))

	seal := func(resp []byte) []byte { return xxtea.Encrypt(resp, respKey) }
	var req []byte
	if enc := body[MaskNumLen:]; len(enc) > 0 {
		if req = xxtea.Decrypt(enc, reqKey); req == nil {
			return nil, seal, ErrDecrypt
		}
	}
	return req, seal, nil
}

func (XXTEA) Seal(maskKey string, plain []byte) []byte {
	return xxtea.Encrypt(plain, []byte(maskKey))
}

func (XXTEA) Open(maskKey string, body []byte) ([]byte, error) {
	if len(body) == 0 {
		return nil, nil
	}
	out := xxtea.Decrypt(body, []byte(maskKey))
	if out == nil {
		return nil, ErrDecrypt
	}
	return out, nil
}

// SealAuthRequest builds an xxtea AUTH body; it is the client-side
// counterpart of OpenAuth.
func (XXTEA) SealAuthRequest(maskNum uint64, authReq []byte) []byte {
	out := make([]byte, MaskNumLen, MaskNumLen+len(authReq)+8)
	binary.BigEndian.PutUint64(out, maskNum)
	return append(out, xxtea.Encrypt(authReq, []byte(strconv.FormatUint(^maskNum, 10)))...)
}

// OpenAuthResponse decodes an xxtea AUTH response on the client side.
func (XXTEA) OpenAuthResponse(maskNum uint64, body []byte) ([]byte, error) {
	if len(body) == 0 {
		return nil, nil
	}
	out := xxtea.Decrypt(body, []byte(strconv.FormatUint(maskNum, 10)))
	if out == nil {
		return nil, ErrDecrypt
	}
	return out, nil
}

// Plain leaves bodies untouched. It must only be used under transport-level
// encryption such as wss.
type Plain struct{}

func (Plain) Name() string { return CodecPlain }

func (Plain) OpenAuth(body []byte) ([]byte, func([]byte) []byte, error) {
	return body, func(resp []byte) []byte { return resp }, nil
}

func (Plain) Seal(_ string, plain []byte) []byte { return plain }

func (Plain) Open(_ string, body []byte) ([]byte, error) { return body, nil }
