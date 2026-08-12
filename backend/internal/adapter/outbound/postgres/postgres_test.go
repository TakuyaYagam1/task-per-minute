package postgres

import (
	"context"
	"errors"
	"testing"
)

func TestNew_InvalidDSNReturnsError(t *testing.T) {
	_, err := New(context.Background(), Config{DSN: "::not-a-dsn::"})
	if err == nil {
		t.Fatal("expected error for malformed DSN")
	}
}

func TestNew_UnreachableHostReturnsError(t *testing.T) {
	cfg := Config{
		DSN:      "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=1",
		MaxConns: 5,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := New(ctx, cfg)
	if err == nil {
		t.Fatal("expected error when target is unreachable")
	}
}

func TestHealthCheck_NilPoolReturnsErrNilPool(t *testing.T) {
	err := HealthCheck(context.Background(), nil)
	if !errors.Is(err, ErrNilPool) {
		t.Fatalf("expected ErrNilPool, got %v", err)
	}
}
