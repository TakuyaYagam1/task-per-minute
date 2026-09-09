package recovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type Clock interface {
	Now() time.Time
}

const (
	DefaultSweepBatchSize int32 = 128
	MaximumSweepBatchSize int32 = 512
)

var (
	ErrInvalidDeadline = errors.New("invalid recovery deadline")
	ErrInvalidCursor   = errors.New("invalid recovery cursor")
	ErrInvalidBatch    = errors.New("invalid recovery deadline batch")
)

// DeadlineKind identifies the durable state transition whose timer was lost
// when the process stopped.
type DeadlineKind string

const (
	DeadlineKindGame        DeadlineKind = "game"
	DeadlineKindReadyWindow DeadlineKind = "ready_window"
	DeadlineKindReconnect   DeadlineKind = "reconnect"
)

// DeadlineCursor is an exclusive, stable cursor over pending deadline keys.
// Its zero value addresses the start of a sweep.
type DeadlineCursor struct {
	Kind DeadlineKind
	ID   uuid.UUID
}

// PendingDeadline contains only public scheduling metadata. Private task
// material is deliberately excluded from recovery scans.
type PendingDeadline struct {
	Kind                  DeadlineKind
	ID                    uuid.UUID
	TournamentID          uuid.UUID
	RosterID              uuid.UUID
	WaveID                uuid.UUID
	SeriesID              uuid.UUID
	SlotID                uuid.UUID
	GameID                uuid.UUID
	PauseID               uuid.UUID
	ReadyWindowRevisionID uuid.UUID
	ParticipantID         uuid.UUID
	ExpectedRevision      int64
	DueAt                 time.Time
}

func (deadline PendingDeadline) Validate() error {
	deadline.DueAt = deadline.DueAt.Round(0).UTC()
	if deadline.ID == uuid.Nil || deadline.TournamentID == uuid.Nil ||
		deadline.RosterID == uuid.Nil || deadline.ExpectedRevision < 1 ||
		!domain.IsValidServerTime(deadline.DueAt) {
		return ErrInvalidDeadline
	}

	valid := false
	switch deadline.Kind {
	case DeadlineKindGame:
		valid = validGameDeadline(deadline)
	case DeadlineKindReadyWindow:
		valid = validReadyWindowDeadline(deadline)
	case DeadlineKindReconnect:
		valid = validReconnectDeadline(deadline)
	}
	if valid {
		return nil
	}
	return ErrInvalidDeadline
}

func validGameDeadline(deadline PendingDeadline) bool {
	return deadline.WaveID != uuid.Nil && deadline.SeriesID != uuid.Nil &&
		deadline.SlotID != uuid.Nil && deadline.GameID == deadline.ID &&
		deadline.PauseID == uuid.Nil && deadline.ReadyWindowRevisionID == uuid.Nil &&
		deadline.ParticipantID == uuid.Nil
}

func validReadyWindowDeadline(deadline PendingDeadline) bool {
	return deadline.WaveID != uuid.Nil && deadline.SeriesID == uuid.Nil &&
		deadline.SlotID == uuid.Nil && deadline.GameID == uuid.Nil &&
		deadline.PauseID == uuid.Nil && deadline.ReadyWindowRevisionID != uuid.Nil &&
		deadline.ParticipantID == uuid.Nil
}

func validReconnectDeadline(deadline PendingDeadline) bool {
	return deadline.WaveID != uuid.Nil && deadline.SeriesID != uuid.Nil &&
		deadline.SlotID != uuid.Nil && deadline.GameID != uuid.Nil &&
		deadline.PauseID != uuid.Nil && deadline.ReadyWindowRevisionID == uuid.Nil &&
		deadline.ParticipantID != uuid.Nil
}

func (deadline PendingDeadline) Cursor() DeadlineCursor {
	return DeadlineCursor{Kind: deadline.Kind, ID: deadline.ID}
}

func (cursor DeadlineCursor) Validate() error {
	if cursor.Kind == "" && cursor.ID == uuid.Nil {
		return nil
	}
	if cursor.ID == uuid.Nil || deadlineKindOrder(cursor.Kind) == 0 {
		return ErrInvalidCursor
	}
	return nil
}

func (cursor DeadlineCursor) after(previous DeadlineCursor) bool {
	if previous.Kind == "" {
		return true
	}
	currentOrder := deadlineKindOrder(cursor.Kind)
	previousOrder := deadlineKindOrder(previous.Kind)
	if currentOrder != previousOrder {
		return currentOrder > previousOrder
	}
	return cursor.ID.String() > previous.ID.String()
}

func deadlineKindOrder(kind DeadlineKind) int32 {
	switch kind {
	case DeadlineKindGame:
		return 1
	case DeadlineKindReadyWindow:
		return 2
	case DeadlineKindReconnect:
		return 3
	default:
		return 0
	}
}

// DeadlineSource reads authoritative pending deadline state in cursor order.
type DeadlineSource interface {
	ListPendingDeadlines(
		ctx context.Context,
		after DeadlineCursor,
		limit int32,
	) ([]PendingDeadline, error)
}

// DeadlineRearmer revalidates and registers one pending deadline. It returns
// false when the durable state changed or the same timer is already armed.
type DeadlineRearmer interface {
	RearmDeadline(ctx context.Context, deadline PendingDeadline) (bool, error)
}

// DeadlineArmSink is the process-local scheduling boundary used by a durable
// adapter after it has revalidated the row.
type DeadlineArmSink interface {
	ArmDeadline(ctx context.Context, deadline PendingDeadline) (bool, error)
}

// DeadlineHandler executes the authoritative compare-and-set transition when
// a registered deadline becomes due. A false result is a safe stale replay.
type DeadlineHandler interface {
	HandleDeadline(ctx context.Context, deadline PendingDeadline) (bool, error)
}

func deadlineError(operation string, err error) error {
	return fmt.Errorf("recovery deadline %s: %w", operation, err)
}
