package auth

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestMemoryOperatorAndSession(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	hash := []byte("hash")
	if err := s.EnsureOperator(ctx, hash); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureOperator(ctx, []byte("other")); err != nil {
		t.Fatal(err)
	}
	got, err := s.PasswordHash(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, hash) {
		t.Fatalf("hash changed to %s", got)
	}
	exp := time.Now().Add(time.Hour)
	token := []byte("tok")
	if err := s.CreateSession(ctx, token, exp); err != nil {
		t.Fatal(err)
	}
	ok, err := s.SessionValid(ctx, token, time.Now())
	if err != nil || !ok {
		t.Fatalf("valid = %v, %v", ok, err)
	}
	ok, err = s.SessionValid(ctx, token, exp.Add(time.Second))
	if err != nil || ok {
		t.Fatalf("expired valid = %v, %v", ok, err)
	}
	if err := s.DeleteSession(ctx, token); err != nil {
		t.Fatal(err)
	}
	ok, err = s.SessionValid(ctx, token, time.Now())
	if err != nil || ok {
		t.Fatalf("deleted valid = %v, %v", ok, err)
	}
}
