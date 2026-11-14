package auth

import (
	"bytes"
	"context"
	"sync"
	"time"
)

// Memory is an in-process Store. EnsureOperator keeps the first hash.
type Memory struct {
	mu       sync.Mutex
	hash     []byte
	hasHash  bool
	sessions map[string]time.Time
}

// NewMemory returns an empty memory store.
func NewMemory() *Memory {
	return &Memory{sessions: map[string]time.Time{}}
}

// EnsureOperator stores passwordHash when no operator exists yet.
func (m *Memory) EnsureOperator(ctx context.Context, passwordHash []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hasHash {
		return nil
	}
	m.hash = bytes.Clone(passwordHash)
	m.hasHash = true
	return nil
}

// PasswordHash returns the stored operator hash.
func (m *Memory) PasswordHash(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.hasHash {
		return nil, ErrNoOperator
	}
	return bytes.Clone(m.hash), nil
}

// CreateSession records tokenHash until expires.
func (m *Memory) CreateSession(ctx context.Context, tokenHash []byte, expires time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[string(tokenHash)] = expires
	return nil
}

// SessionValid reports whether tokenHash exists and expires is not before now.
func (m *Memory) SessionValid(ctx context.Context, tokenHash []byte, now time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	exp, ok := m.sessions[string(tokenHash)]
	if !ok {
		return false, nil
	}
	return !now.After(exp), nil
}

// DeleteSession removes tokenHash. A missing session is not an error.
func (m *Memory) DeleteSession(ctx context.Context, tokenHash []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, string(tokenHash))
	return nil
}
