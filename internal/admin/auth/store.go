// Package auth stores the admin operator and login sessions.
package auth

import (
	"context"
	"errors"
	"time"
)

// ErrNoOperator is returned when no operator password hash is stored.
var ErrNoOperator = errors.New("no operator")

// Store persists one operator password hash and its login sessions.
type Store interface {
	EnsureOperator(ctx context.Context, passwordHash []byte) error
	PasswordHash(ctx context.Context) ([]byte, error)
	CreateSession(ctx context.Context, tokenHash []byte, expires time.Time) error
	SessionValid(ctx context.Context, tokenHash []byte, now time.Time) (bool, error)
	DeleteSession(ctx context.Context, tokenHash []byte) error
}

var (
	_ Store = (*Memory)(nil)
	_ Store = (*MySQL)(nil)
)
