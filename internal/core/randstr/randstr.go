// Package randstr generates cryptographically random strings used for app
// credentials and login tokens.
package randstr

import (
	"crypto/rand"
	"encoding/binary"
	"strings"
)

const (
	// Alnum is the default alphabet: digits, lower and upper case letters.
	Alnum = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	// LowerAlnum contains digits and lower case letters only.
	LowerAlnum = "0123456789abcdefghijklmnopqrstuvwxyz"
)

// String returns n characters drawn uniformly-ish from alphabet using crypto/rand.
func String(n int, alphabet string) string {
	if n <= 0 || alphabet == "" {
		return ""
	}
	letters := []rune(alphabet)
	l := uint32(len(letters))
	buf := make([]byte, 4*n)
	if _, err := rand.Read(buf); err != nil {
		panic("randstr: crypto/rand unavailable: " + err.Error())
	}
	var sb strings.Builder
	sb.Grow(n)
	for i := 0; i < n; i++ {
		sb.WriteRune(letters[binary.BigEndian.Uint32(buf[i*4:])%l])
	}
	return sb.String()
}
