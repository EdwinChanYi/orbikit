// Package moderation checks user-generated text before it is broadcast.
package moderation

import (
	"context"
	"fmt"
	"time"
)

// Result is the verdict for one piece of text.
type Result struct {
	// Allowed reports whether the message may be sent.
	Allowed bool
	// Text is the text to send when Allowed; it may differ from the input
	// when offending words were masked.
	Text string
	// Reason explains a rejection or a modification.
	Reason string
}

// Moderator checks chat text. A non-nil error means the check itself failed
// and the caller should reject the message.
type Moderator interface {
	CheckText(ctx context.Context, appID string, uid, roomID int64, text string) (Result, error)
}

const (
	TypeNone    = "none"
	TypeKeyword = "keyword"
	TypeWebhook = "webhook"
)

// Config selects and configures a Moderator.
type Config struct {
	// Type is one of none, keyword, webhook.
	Type    string        `yaml:"type"`
	Keyword KeywordConfig `yaml:"keyword"`
	Webhook WebhookConfig `yaml:"webhook"`
}

// KeywordConfig configures the keyword moderator.
type KeywordConfig struct {
	// File holds one word per line; blank lines and lines starting with # are ignored.
	File string `yaml:"file"`
	// Mode is reject (default) or replace.
	Mode string `yaml:"mode"`
	// Replacement masks each matched character in replace mode. Defaults to *.
	Replacement string `yaml:"replacement"`
	// CaseSensitive disables Unicode case folding when matching.
	CaseSensitive bool `yaml:"case_sensitive"`
}

// WebhookConfig configures the webhook moderator.
type WebhookConfig struct {
	URL     string            `yaml:"url"`
	Timeout time.Duration     `yaml:"timeout"`
	Headers map[string]string `yaml:"headers"`
	// OnError is allow or reject (default) and applies when the call fails.
	OnError string `yaml:"on_error"`
}

// New builds the Moderator described by cfg.
func New(cfg Config) (Moderator, error) {
	switch cfg.Type {
	case "", TypeNone:
		return None{}, nil
	case TypeKeyword:
		return NewKeywordFromFile(cfg.Keyword)
	case TypeWebhook:
		return NewWebhook(cfg.Webhook)
	default:
		return nil, fmt.Errorf("moderation: unknown type %q", cfg.Type)
	}
}

// None allows everything unchanged.
type None struct{}

func (None) CheckText(_ context.Context, _ string, _, _ int64, text string) (Result, error) {
	return Result{Allowed: true, Text: text}, nil
}
