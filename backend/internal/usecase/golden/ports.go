package golden

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

type StateClock interface {
	Now() time.Time
}

const goldenStateAttempts = 2

var (
	ErrGoldenParticipationAuthorityConflict = errors.New("golden participation authority conflict")
	ErrGoldenParticipationConflict          = errors.New("golden participation commit conflict")
	ErrGoldenCommandReuse                   = errors.New("golden command identifier was reused")
	ErrGoldenRevisionOverflow               = errors.New("golden revision overflow")
	ErrGoldenParticipantExcluded            = errors.New("golden participant is excluded")
)

type GoldenReadyCommand struct {
	Scope              GoldenStateScope
	CommandID          uuid.UUID
	ActorParticipantID uuid.UUID
	ParticipantID      uuid.UUID
	AttemptID          uuid.UUID
	WindowID           uuid.UUID
	ExpectedState      GoldenStateExpectation
	ExpectedWindow     GoldenReadyWindowExpectation

	NextStateRevisionID     uuid.UUID
	NextWindowRevisionID    uuid.UUID
	NextReadinessRevisionID uuid.UUID
}

type GoldenDisconnectCommand struct {
	Scope          GoldenStateScope
	CommandID      uuid.UUID
	ParticipantID  uuid.UUID
	AttemptID      uuid.UUID
	WindowID       uuid.UUID
	ExpectedState  GoldenStateExpectation
	ExpectedWindow GoldenReadyWindowExpectation

	NextStateRevisionID     uuid.UUID
	NextWindowRevisionID    uuid.UUID
	NextReadinessRevisionID uuid.UUID
	NextPresenceRevisionID  uuid.UUID
}

// StateRepository owns the group-level compare-and-set.
type StateRepository interface {
	LoadGoldenState(ctx context.Context, scope GoldenStateScope) (GoldenState, error)
	CommitGoldenState(ctx context.Context, commit GoldenStateCommit) (*GoldenState, bool, error)
}
