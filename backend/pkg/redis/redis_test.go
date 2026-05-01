package redis

import (
	"context"
	"errors"
	"testing"
)

func TestNew_UnreachableAddrReturnsError(t *testing.T) {
	cfg := Config{Addr: "127.0.0.1:1"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := New(ctx, cfg)
	if err == nil {
		t.Fatal("expected error when target is unreachable")
	}
}

func TestHealthCheck_NilClientReturnsErrNilClient(t *testing.T) {
	err := HealthCheck(context.Background(), nil)
	if !errors.Is(err, ErrNilClient) {
		t.Fatalf("expected ErrNilClient, got %v", err)
	}
}
