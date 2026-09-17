package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// StateClock provides the authoritative server time for state transitions.
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
	ErrGoldenReadyWindowClosed              = errors.New("golden ready window is closed")

	ErrGoldenNoShowCutoff            = errors.New("golden no-show cutoff has not passed")
	ErrGoldenNoShowAuthorityConflict = errors.New("golden no-show authority conflict")
	ErrGoldenNoShowConflict          = errors.New("golden no-show commit conflict")

	ErrGoldenFallbackAuthorityConflict = errors.New("golden fallback authority conflict")
	ErrGoldenFallbackConflict          = errors.New("golden fallback commit conflict")
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

type GoldenNoShowCommand struct {
	Scope          GoldenStateScope
	CommandID      uuid.UUID
	AttemptID      uuid.UUID
	WindowID       uuid.UUID
	ExpectedState  GoldenStateExpectation
	ExpectedWindow GoldenReadyWindowExpectation

	NextStateRevisionID      uuid.UUID
	NextWindowRevisionID     uuid.UUID
	NextMembershipRevisionID uuid.UUID
}

type GoldenFallbackCommand struct {
	Scope               GoldenStateScope
	CommandID           uuid.UUID
	AllocationID        uuid.UUID
	ExpectedState       GoldenStateExpectation
	NextStateRevisionID uuid.UUID
}

// StateRepository owns the group-level compare-and-set.
type StateRepository interface {
	LoadGoldenState(ctx context.Context, scope GoldenStateScope) (GoldenState, error)
	CommitGoldenState(ctx context.Context, commit GoldenStateCommit) (*GoldenState, bool, error)
}
