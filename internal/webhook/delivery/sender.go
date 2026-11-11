package delivery

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"time"
)

// SenderConfig controls HTTP delivery.
type SenderConfig struct {
	// Timeout bounds a single HTTP attempt.
	Timeout time.Duration `yaml:"timeout"`
	// MaxRetries is the number of retries after the first attempt.
	MaxRetries int `yaml:"max_retries"`
	// RetryBackoff is the delay before the first retry; it doubles each time.
	RetryBackoff time.Duration `yaml:"retry_backoff"`
	// MaxRetryBackoff caps the delay between retries.
	MaxRetryBackoff time.Duration `yaml:"max_retry_backoff"`
	// InsecureSkipVerify disables TLS certificate verification of callback URLs.
	InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
}

// StatusError reports a non-2xx response.
type StatusError struct {
	StatusCode int
	Body       string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("unexpected status %d: %s", e.StatusCode, e.Body)
}

// Sender posts signed webhook requests with bounded exponential-backoff retries.
type Sender struct {
	client *http.Client
	cfg    SenderConfig
	now    func() time.Time
}

const maxLoggedRespBody = 512

// NewSender returns a Sender.
func NewSender(cfg SenderConfig) *Sender {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 2 * time.Second
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = 200 * time.Millisecond
	}
	if cfg.MaxRetryBackoff < cfg.RetryBackoff {
		cfg.MaxRetryBackoff = cfg.RetryBackoff
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = 64
	if cfg.InsecureSkipVerify {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &Sender{
		client: &http.Client{Transport: tr, Timeout: cfg.Timeout},
		cfg:    cfg,
		now:    time.Now,
	}
}

// Post delivers body to url. Network errors, 429 and 5xx responses are
// retried up to MaxRetries times; every attempt is signed anew. It returns the
// last error when all attempts fail or ctx is done.
func (s *Sender) Post(ctx context.Context, url, appID, appSecret string, body []byte) error {
	backoff := s.cfg.RetryBackoff
	for attempt := 0; ; attempt++ {
		retryable, err := s.postOnce(ctx, url, appID, appSecret, body)
		if err == nil {
			return nil
		}
		if !retryable || attempt >= s.cfg.MaxRetries || ctx.Err() != nil {
			return fmt.Errorf("attempt %d: %w", attempt+1, err)
		}
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
			return fmt.Errorf("attempt %d: %w (aborted: %v)", attempt+1, err, ctx.Err())
		case <-t.C:
		}
		backoff = min(backoff*2, s.cfg.MaxRetryBackoff)
	}
}

func (s *Sender) postOnce(ctx context.Context, url, appID, appSecret string, body []byte) (retryable bool, err error) {
	req, err := NewSignedRequest(ctx, url, appID, appSecret, body, s.now())
	if err != nil {
		return false, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return true, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxLoggedRespBody))
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return false, nil
	}
	retryable = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
	return retryable, &StatusError{StatusCode: resp.StatusCode, Body: string(respBody)}
}
