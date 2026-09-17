package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
)

func TestNew_InvalidDSNReturnsError(t *testing.T) {
	_, err := db.New(context.Background(), db.Config{DSN: "::not-a-dsn::"})
	if err == nil {
		t.Fatal("expected error for malformed DSN")
	}
}

func TestNew_UnreachableHostReturnsError(t *testing.T) {
	cfg := db.Config{
		DSN:      "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=1",
		MaxConns: 5,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := db.New(ctx, cfg)
	if err == nil {
		t.Fatal("expected error when target is unreachable")
	}
}

func TestHealthCheck_NilPoolReturnsErrNilPool(t *testing.T) {
	err := db.HealthCheck(context.Background(), nil)
	if !errors.Is(err, db.ErrNilPool) {
		t.Fatalf("expected ErrNilPool, got %v", err)
	}
}
