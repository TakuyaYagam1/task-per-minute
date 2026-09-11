package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"
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
	TournamentID               uuid.UUID
	CommandID                  uuid.UUID
	ExpectedProjectionRevision int64
}

type GoldenStartCommand struct {
	TournamentID uuid.UUID
	AttemptID    uuid.UUID
	CommandID    uuid.UUID
}

type GoldenReadyCommand struct {
	TournamentID uuid.UUID
	PlayerID     uuid.UUID
	CommandID    uuid.UUID
	Ready        bool
}

type GoldenSubmissionCommand struct {
	TournamentID  uuid.UUID
	PlayerID      uuid.UUID
	CommandID     uuid.UUID
	SubmittedFlag string
}

type GoldenConnectionCommand struct {
	TournamentID uuid.UUID
	PlayerID     uuid.UUID
	CommandID    uuid.UUID
	Connected    bool
}

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
	AssignmentID     uuid.UUID
	SnapshotID       uuid.UUID
	TaskID           uuid.UUID
	Title            string
	Category         string
	Difficulty       string
	TimeLimitSeconds int
}

type GoldenParticipantView struct {
	TournamentID    uuid.UUID
	ParticipantID   uuid.UUID
	GroupID         uuid.UUID
	GroupRevisionID uuid.UUID
	AttemptID       uuid.UUID
	State           string
	Ready           bool
	Submitted       bool
	Position        *int
	StartedAt       *time.Time
	Deadline        *time.Time
	Task            *GoldenTaskView
}
