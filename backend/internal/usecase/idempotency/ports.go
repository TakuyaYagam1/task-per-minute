package idempotency

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

type BeginDisposition uint8

const (
	BeginAcquired BeginDisposition = iota + 1
	BeginInFlight
	BeginSucceeded
)

var ErrPayloadConflict = errors.New("idempotency: command key already belongs to another payload")

var ErrLeaseLost = errors.New("idempotency: command receipt lease is no longer owned")

type Command struct {
	Namespace     string
	ID            uuid.UUID
	PayloadDigest [32]byte
}

// LeaseToken is an opaque random owner token for one in-flight receipt
// acquisition. It fences delayed replicas after a receipt TTL expires.
type LeaseToken [16]byte

type BeginResult struct {
	Disposition BeginDisposition
	Lease       LeaseToken
}

type Store interface {
	Begin(ctx context.Context, command Command, lease LeaseToken) (BeginResult, error)
	MarkSucceeded(ctx context.Context, command Command, lease LeaseToken) error
	MarkFailed(ctx context.Context, command Command, lease LeaseToken) error
}
