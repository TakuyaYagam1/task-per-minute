package task

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

var (
	ErrInvalidConfig      = errors.New("private task availability: invalid configuration")
	ErrMonitorRunning     = errors.New("private task availability: monitor already running")
	ErrInvalidBacklog     = errors.New("private task availability: invalid durable backlog")
	ErrBacklogUnavailable = errors.New("private task availability: durable backlog unavailable")
)

// BacklogSnapshot is the sanitized durable private-task receipt availability gap.
// It contains no participant, task, assignment, or source-file data.
type BacklogSnapshot struct {
	PendingCount    int64
	OldestPendingAt *time.Time
}

func (snapshot BacklogSnapshot) Validate() error {
	if snapshot.PendingCount < 0 {
		return fmt.Errorf("%w: negative pending count", ErrInvalidBacklog)
	}
	if snapshot.PendingCount == 0 && snapshot.OldestPendingAt != nil {
		return fmt.Errorf("%w: empty backlog has oldest item", ErrInvalidBacklog)
	}
	if snapshot.PendingCount > 0 && (snapshot.OldestPendingAt == nil ||
		snapshot.OldestPendingAt.IsZero() || snapshot.OldestPendingAt.Location() != time.UTC) {
		return fmt.Errorf("%w: pending backlog has invalid oldest item", ErrInvalidBacklog)
	}
	return nil
}

// BacklogSource reads the authoritative receipt gap for all active private
// task graphs. A successful zero backlog proves WaveStart's task material is
// durably available to every active assigned participant; content never
// crosses this port.
type BacklogSource interface {
	TaskDeliveryBacklog(ctx context.Context) (BacklogSnapshot, error)
}

// BacklogSourceFunc adapts a narrow authoritative scan without carrying
// private delivery data through worker composition.
type BacklogSourceFunc func(context.Context) (BacklogSnapshot, error)

func (fn BacklogSourceFunc) TaskDeliveryBacklog(ctx context.Context) (BacklogSnapshot, error) {
	if fn == nil {
		return BacklogSnapshot{}, ErrBacklogUnavailable
	}
	return fn(ctx)
}

type HealthSnapshot struct {
	Started             bool
	Running             bool
	StartedAt           *time.Time
	LastAttemptAt       *time.Time
	LastSuccessAt       *time.Time
	LastFailureAt       *time.Time
	ConsecutiveFailures int
	Stale               bool
}

type HealthSource interface {
	Health(now time.Time) HealthSnapshot
}

// HealthSourceFunc adapts a sanitized local worker-health observation.
type HealthSourceFunc func(time.Time) HealthSnapshot

func (fn HealthSourceFunc) Health(now time.Time) HealthSnapshot {
	if fn == nil {
		return HealthSnapshot{Stale: true}
	}
	return fn(now)
}

func (snapshot HealthSnapshot) Validate() error {
	if snapshot.ConsecutiveFailures < 0 || snapshot.Running && !snapshot.Started ||
		snapshot.Started != (snapshot.StartedAt != nil) {
		return fmt.Errorf("%w: invalid health state", ErrInvalidConfig)
	}
	values := []*time.Time{snapshot.StartedAt, snapshot.LastAttemptAt, snapshot.LastSuccessAt, snapshot.LastFailureAt}
	for _, value := range values {
		if value != nil && (value.IsZero() || value.Location() != time.UTC) {
			return fmt.Errorf("%w: invalid health timestamp", ErrInvalidConfig)
		}
	}
	if snapshot.StartedAt != nil {
		for _, value := range values[1:] {
			if value != nil && value.Before(*snapshot.StartedAt) {
				return fmt.Errorf("%w: health precedes start", ErrInvalidConfig)
			}
		}
	}
	if snapshot.LastSuccessAt != nil &&
		(snapshot.LastAttemptAt == nil || snapshot.LastSuccessAt.After(*snapshot.LastAttemptAt)) {
		return fmt.Errorf("%w: success exceeds attempt", ErrInvalidConfig)
	}
	if snapshot.LastFailureAt != nil &&
		(snapshot.LastAttemptAt == nil || snapshot.LastFailureAt.After(*snapshot.LastAttemptAt)) {
		return fmt.Errorf("%w: failure exceeds attempt", ErrInvalidConfig)
	}
	return nil
}

type Repository interface {
	Create(ctx context.Context, input UpdateInput) (*domain.Task, error)
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Task, error)
	List(ctx context.Context) ([]*domain.Task, error)
	Update(ctx context.Context, id uuid.UUID, input UpdateInput) (*domain.Task, error)

	// Delete atomically rejects task heads that are already referenced by
	// published tournament content or assignment plans.
	Delete(ctx context.Context, id uuid.UUID) error
}

type Catalog interface {
	GetTask(ctx context.Context, id uuid.UUID) (*domain.Task, error)
	UpdateTask(ctx context.Context, id uuid.UUID, input UpdateInput) (*domain.Task, error)
}

type CleanupRunner interface {
	Run(ctx context.Context, timeout time.Duration, cleanup func(context.Context) error) error
}

type SourceFileCleanupFailure struct {
	Operation string
	TaskID    uuid.UUID
	ObjectKey string
	Err       error
}

type SourceFileCleanupObserver interface {
	ObserveSourceFileCleanup(ctx context.Context, failure SourceFileCleanupFailure)
}

type SourceFileStorage interface {
	Upload(ctx context.Context, key string, reader io.Reader, size int64) (string, error)
	PresignedGetURL(ctx context.Context, key string, ttl time.Duration) (string, error)
	Delete(ctx context.Context, key string) error
}
