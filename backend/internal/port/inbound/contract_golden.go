package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// GoldenUseCase is the production boundary for the individual Golden stage.
// Operator and participant identities are supplied by authenticated adapters.
type GoldenUseCase interface {
	Open(ctx context.Context, command GoldenOpenCommand) (GoldenOperatorView, error)
	Start(ctx context.Context, command GoldenStartCommand) (GoldenOperatorView, error)
	SetReady(ctx context.Context, command GoldenReadyCommand) (GoldenParticipantView, error)
	Submit(ctx context.Context, command GoldenSubmissionCommand) (GoldenParticipantView, error)
	OperatorView(ctx context.Context, query GoldenOperatorQuery) (GoldenOperatorView, error)
	ParticipantView(ctx context.Context, query GoldenParticipantQuery) (GoldenParticipantView, error)
	Recover(ctx context.Context, tournamentID uuid.UUID) error
}

// GoldenConnectionUseCase is the authenticated realtime lifecycle boundary.
// It is kept separate from GoldenUseCase so read-only snapshot adapters do not
// gain mutation authority accidentally.
type GoldenConnectionUseCase interface {
	SetConnected(ctx context.Context, command GoldenConnectionCommand) error
}

type GoldenOpenCommand struct {
	GoldenMutationScope

	TournamentID               uuid.UUID
	CommandID                  uuid.UUID
	ExpectedProjectionRevision int64
}

type GoldenStartCommand struct {
	GoldenMutationScope

	TournamentID uuid.UUID
	AttemptID    uuid.UUID
	CommandID    uuid.UUID
}

type GoldenReadyCommand struct {
	GoldenMutationScope

	TournamentID uuid.UUID
	PlayerID     uuid.UUID
	CommandID    uuid.UUID
	Ready        bool
}

type GoldenSubmissionCommand struct {
	GoldenMutationScope

	TournamentID  uuid.UUID
	PlayerID      uuid.UUID
	CommandID     uuid.UUID
	SubmittedFlag string
}

type GoldenConnectionCommand struct {
	GoldenMutationScope

	TournamentID uuid.UUID
	PlayerID     uuid.UUID
	CommandID    uuid.UUID
	Connected    bool
}

// GoldenMutationScope is populated by the authenticated transport adapter.
// Expected values are part of the command identity and are checked again by
// the transactional repository while it holds the authoritative runtime lock.
type GoldenMutationScope struct {
	ActorID                 uuid.UUID
	ExpectedRuntimeRevision int64
	ExpectedAttemptID       uuid.UUID
	ExpectedReadyWindowID   uuid.UUID
}

var (
	ErrGoldenCommandReuseConflict = errors.New("golden command identifier was reused")
	ErrGoldenAuthorityConflict    = errors.New("golden runtime authority is stale")
)

type GoldenCommandReuseConflictError struct {
	CommandID uuid.UUID
}

func (e *GoldenCommandReuseConflictError) Error() string {
	if e == nil || e.CommandID == uuid.Nil {
		return ErrGoldenCommandReuseConflict.Error()
	}
	return fmt.Sprintf("%s: %s", ErrGoldenCommandReuseConflict, e.CommandID)
}

func (e *GoldenCommandReuseConflictError) Unwrap() error { return domain.ErrConflict }

type GoldenAuthorityConflictError struct {
	ExpectedRevision int64
	CurrentRevision  int64
	ExpectedWindow   uuid.UUID
	CurrentWindow    uuid.UUID
	ExpectedAttempt  uuid.UUID
	CurrentAttempt   uuid.UUID
}

func (e *GoldenAuthorityConflictError) Error() string { return ErrGoldenAuthorityConflict.Error() }
func (e *GoldenAuthorityConflictError) Unwrap() error { return domain.ErrConflict }

type GoldenOperatorQuery struct {
	TournamentID uuid.UUID
	OperatorID   uuid.UUID
}

type GoldenParticipantQuery struct {
	TournamentID uuid.UUID
	PlayerID     uuid.UUID
}

type GoldenMemberView struct {
	ParticipantID uuid.UUID
	Ready         bool
	Submitted     bool
	Position      *int
}

type GoldenOperatorGroupView struct {
	GroupID         uuid.UUID
	GroupRevisionID uuid.UUID
	AttemptID       uuid.UUID
	State           string
	RuntimeRevision int64
	ReadyWindowID   uuid.UUID
	PositionFrom    int
	PositionTo      int
	StartedAt       *time.Time
	Deadline        *time.Time
	Members         []GoldenMemberView
}

type GoldenOperatorView struct {
	TournamentID uuid.UUID
	Groups       []GoldenOperatorGroupView
	ObservedAt   time.Time
}

type GoldenTaskView struct {
	AssignmentID        uuid.UUID
	SnapshotID          uuid.UUID
	TaskID              uuid.UUID
	Version             int
	Title               string
	Description         string
	Category            string
	Difficulty          string
	TimeLimitSeconds    int
	TaskURL             *string
	SourceFileAvailable bool
}

type GoldenParticipantView struct {
	TournamentID    uuid.UUID
	ParticipantID   uuid.UUID
	GroupID         uuid.UUID
	GroupRevisionID uuid.UUID
	AttemptID       uuid.UUID
	State           string
	RuntimeRevision int64
	ReadyWindowID   uuid.UUID
	Ready           bool
	Submitted       bool
	Position        *int
	StartedAt       *time.Time
	Deadline        *time.Time
	Task            *GoldenTaskView
}
