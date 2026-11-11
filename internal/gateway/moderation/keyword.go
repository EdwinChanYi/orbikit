package moderation

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
)

const (
	ModeReject  = "reject"
	ModeReplace = "replace"
)

// Keyword matches text against a word list using a trie (DFA), so the cost of
// a check is linear in the text length times the longest word, independent of
// the number of words.
type Keyword struct {
	root          *trieNode
	replace       bool
	mask          []rune
	caseSensitive bool
}

type trieNode struct {
	next map[rune]*trieNode
	end  bool
}

// NewKeywordFromFile loads the word list named by cfg.File.
func NewKeywordFromFile(cfg KeywordConfig) (*Keyword, error) {
	if cfg.File == "" {
		return nil, fmt.Errorf("moderation: keyword.file is required")
	}
	f, err := os.Open(cfg.File)
	if err != nil {
		return nil, fmt.Errorf("moderation: open keyword file: %w", err)
	}
	defer f.Close()
	words, err := ReadWords(f)
	if err != nil {
		return nil, fmt.Errorf("moderation: read keyword file: %w", err)
	}
	return NewKeyword(cfg, words)
}

// ReadWords parses a word list: one word per line, blank lines and lines
// starting with # skipped, surrounding whitespace trimmed.
func ReadWords(r io.Reader) ([]string, error) {
	var words []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		words = append(words, line)
	}
	return words, sc.Err()
}

// NewKeyword builds a matcher from words.
func NewKeyword(cfg KeywordConfig, words []string) (*Keyword, error) {
	k := &Keyword{root: &trieNode{}, caseSensitive: cfg.CaseSensitive}
	switch cfg.Mode {
	case "", ModeReject:
	case ModeReplace:
		k.replace = true
	default:
		return nil, fmt.Errorf("moderation: unknown keyword mode %q", cfg.Mode)
	}
	k.mask = []rune(cfg.Replacement)
	if len(k.mask) == 0 {
		k.mask = []rune{'*'}
	}
	for _, w := range words {
		k.add(w)
	}
	return k, nil
}

func (k *Keyword) fold(r rune) rune {
	if k.caseSensitive {
		return r
	}
	return unicode.ToLower(r)
}

func (k *Keyword) add(word string) {
	if word == "" {
		return
	}
	n := k.root
	for _, r := range word {
		r = k.fold(r)
		child := n.next[r]
		if child == nil {
			if n.next == nil {
				n.next = make(map[rune]*trieNode)
			}
			child = &trieNode{}
			n.next[r] = child
		}
		n = child
	}
	n.end = true
}

// longestMatch returns the length of the longest word starting at text[i], or 0.
func (k *Keyword) longestMatch(text []rune, i int) int {
	n := k.root
	best := 0
	for j := i; j < len(text); j++ {
		n = n.next[k.fold(text[j])]
		if n == nil {
			break
		}
		if n.end {
			best = j - i + 1
		}
	}
	return best
}

// Contains reports whether text contains any word.
func (k *Keyword) Contains(text string) bool {
	runes := []rune(text)
	for i := range runes {
		if k.longestMatch(runes, i) > 0 {
			return true
		}
	}
	return false
}

// Mask replaces every matched character, preferring the longest match at each
// position, and reports whether anything matched.
func (k *Keyword) Mask(text string) (string, bool) {
	runes := []rune(text)
	hit := false
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(runes); {
		if n := k.longestMatch(runes, i); n > 0 {
			hit = true
			for m := 0; m < n; m++ {
				b.WriteRune(k.mask[m%len(k.mask)])
			}
			i += n
			continue
		}
		b.WriteRune(runes[i])
		i++
	}
	if !hit {
		return text, false
	}
	return b.String(), true
}

func (k *Keyword) CheckText(_ context.Context, _ string, _, _ int64, text string) (Result, error) {
	if k.replace {
		masked, hit := k.Mask(text)
		if !hit {
			return Result{Allowed: true, Text: text}, nil
		}
		return Result{Allowed: true, Text: masked, Reason: "keyword masked"}, nil
	}
	if k.Contains(text) {
		return Result{Allowed: false, Reason: "keyword hit"}, nil
	}
	return Result{Allowed: true, Text: text}, nil
}
