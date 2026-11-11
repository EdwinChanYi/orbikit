package randstr

import (
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	for _, alphabet := range []string{Alnum, LowerAlnum} {
		s := String(128, alphabet)
		if len(s) != 128 {
			t.Fatalf("len = %d, want 128", len(s))
		}
		for _, r := range s {
			if !strings.ContainsRune(alphabet, r) {
				t.Fatalf("unexpected rune %q for alphabet %q", r, alphabet)
			}
		}
	}
	if String(32, Alnum) == String(32, Alnum) {
		t.Fatal("two random strings should differ")
	}
	if String(0, Alnum) != "" || String(5, "") != "" {
		t.Fatal("degenerate input should yield empty string")
	}
}
