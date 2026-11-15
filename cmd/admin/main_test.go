package main

import "testing"

func TestOperatorPasswordUsesEnvironment(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", "secret")
	if got := operatorPassword(); got != "secret" {
		t.Fatalf("password = %q, want secret", got)
	}
}

func TestOperatorPasswordDefaultsWhenUnset(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", "")
	if got := operatorPassword(); got != "123456" {
		t.Fatalf("password = %q, want 123456", got)
	}
}
