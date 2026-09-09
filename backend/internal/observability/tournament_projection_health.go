package observability

import (
	"context"
	"errors"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrInvalidProjectionHealth = errors.New("invalid projection health snapshot")

// ProjectionHealthSnapshot is the bounded durable publication-lag view.
// ObservedAt is sampled by PostgreSQL so local clock skew cannot fabricate lag.
type ProjectionHealthSnapshot struct {
	PendingCount    int64
	OldestPendingAt *time.Time
	ObservedAt      time.Time
}

// Validate rejects ambiguous, future, and incomplete lag observations.
func (snapshot ProjectionHealthSnapshot) Validate() error {
	if snapshot.PendingCount < 0 || !domain.IsValidServerTime(snapshot.ObservedAt) {
		return ErrInvalidProjectionHealth
	}
	if snapshot.PendingCount == 0 {
		if snapshot.OldestPendingAt != nil {
			return ErrInvalidProjectionHealth
		}
		return nil
	}
	if snapshot.OldestPendingAt == nil ||
		!domain.IsValidServerTime(*snapshot.OldestPendingAt) ||
		snapshot.OldestPendingAt.After(snapshot.ObservedAt) {
		return ErrInvalidProjectionHealth
	}
	return nil
}

// ProjectionHealthSource returns one bounded PostgreSQL publication-lag sample.
type ProjectionHealthSource interface {
	ProjectionHealth(ctx context.Context) (ProjectionHealthSnapshot, error)
}

// ProjectionHealthSourceFunc adapts a function to ProjectionHealthSource.
type ProjectionHealthSourceFunc func(ctx context.Context) (ProjectionHealthSnapshot, error)

func (fn ProjectionHealthSourceFunc) ProjectionHealth(ctx context.Context) (ProjectionHealthSnapshot, error) {
	if fn == nil {
		return ProjectionHealthSnapshot{}, context.Canceled
	}
	return fn(ctx)
}
