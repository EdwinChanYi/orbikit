package moderation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustKeyword(t *testing.T, cfg KeywordConfig, words ...string) *Keyword {
	t.Helper()
	k, err := NewKeyword(cfg, words)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestKeywordMask(t *testing.T) {
	k := mustKeyword(t, KeywordConfig{Mode: ModeReplace}, "bad", "badword", "坏蛋", "AbC")
	cases := []struct {
		in, want string
		hit      bool
	}{
		{"hello world", "hello world", false},
		{"bad", "***", true},
		{"a badword here", "a ******* here", true},
		{"badwor", "***wor", true},
		{"你是坏蛋吗", "你是**吗", true},
		{"坏", "坏", false},
		{"xxABCxx", "xx***xx", true},
		{"badbad", "******", true},
		{"", "", false},
	}
	for _, c := range cases {
		got, hit := k.Mask(c.in)
		if got != c.want || hit != c.hit {
			t.Errorf("Mask(%q) = %q, %v; want %q, %v", c.in, got, hit, c.want, c.hit)
		}
	}
}

func TestKeywordOverlappingPrefixes(t *testing.T) {
	// "ab" is a prefix of "abcd"; matching must fall back to the shorter word
	// when the longer one does not complete.
	k := mustKeyword(t, KeywordConfig{}, "ab", "abcd", "cde")
	if got, _ := k.Mask("abcx"); got != "**cx" {
		t.Fatalf("Mask(abcx) = %q", got)
	}
	if got, _ := k.Mask("abcde"); got != "****e" {
		t.Fatalf("Mask(abcde) = %q", got)
	}
	if got, _ := k.Mask("xcde"); got != "x***" {
		t.Fatalf("Mask(xcde) = %q", got)
	}
}

func TestKeywordCaseSensitive(t *testing.T) {
	k := mustKeyword(t, KeywordConfig{CaseSensitive: true}, "Bad")
	if k.Contains("bad") {
		t.Fatal("case-sensitive matcher matched different case")
	}
	if !k.Contains("so Bad") {
		t.Fatal("case-sensitive matcher missed exact case")
	}
	ki := mustKeyword(t, KeywordConfig{}, "Bad")
	if !ki.Contains("BAD") || !ki.Contains("bad") {
		t.Fatal("case-insensitive matcher missed")
	}
}

func TestKeywordCustomReplacement(t *testing.T) {
	k := mustKeyword(t, KeywordConfig{Mode: ModeReplace, Replacement: "#"}, "spam")
	if got, _ := k.Mask("no spam"); got != "no ####" {
		t.Fatalf("Mask = %q", got)
	}
}

func TestKeywordCheckTextModes(t *testing.T) {
	ctx := context.Background()

	reject := mustKeyword(t, KeywordConfig{Mode: ModeReject}, "spam")
	res, err := reject.CheckText(ctx, "app", 1, 2, "buy spam now")
	if err != nil || res.Allowed || res.Reason == "" {
		t.Fatalf("reject mode = %+v, %v", res, err)
	}
	res, err = reject.CheckText(ctx, "app", 1, 2, "hello")
	if err != nil || !res.Allowed || res.Text != "hello" {
		t.Fatalf("reject mode clean = %+v, %v", res, err)
	}

	replace := mustKeyword(t, KeywordConfig{Mode: ModeReplace}, "spam")
	res, err = replace.CheckText(ctx, "app", 1, 2, "buy spam now")
	if err != nil || !res.Allowed || res.Text != "buy **** now" {
		t.Fatalf("replace mode = %+v, %v", res, err)
	}
}

func TestKeywordInvalidMode(t *testing.T) {
	if _, err := NewKeyword(KeywordConfig{Mode: "drop"}, nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestReadWords(t *testing.T) {
	words, err := ReadWords(strings.NewReader("# comment\n\n  foo  \nbar\r\n#baz\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(words, ",") != "foo,bar" {
		t.Fatalf("ReadWords = %q", words)
	}
}

func TestNewFromConfigKeywordFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "words.txt")
	if err := os.WriteFile(path, []byte("违禁词\nforbidden\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := New(Config{Type: TypeKeyword, Keyword: KeywordConfig{File: path, Mode: ModeReplace}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := m.CheckText(context.Background(), "a", 1, 1, "这是违禁词和Forbidden")
	if err != nil || res.Text != "这是***和*********" {
		t.Fatalf("CheckText = %+v, %v", res, err)
	}

	if _, err := New(Config{Type: TypeKeyword}); err == nil {
		t.Fatal("expected error for missing file")
	}
	if _, err := New(Config{Type: "vendor"}); err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestNone(t *testing.T) {
	m, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := m.CheckText(context.Background(), "a", 1, 1, "anything")
	if err != nil || !res.Allowed || res.Text != "anything" {
		t.Fatalf("None = %+v, %v", res, err)
	}
}

func BenchmarkKeywordMask(b *testing.B) {
	words := make([]string, 0, 10000)
	for i := 0; i < 10000; i++ {
		words = append(words, strings.Repeat(string(rune('a'+i%26)), 1+i%8)+string(rune('A'+i%26)))
	}
	k, _ := NewKeyword(KeywordConfig{Mode: ModeReplace}, words)
	text := strings.Repeat("the quick brown fox jumps over the lazy dog ", 10)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k.Mask(text)
	}
}
