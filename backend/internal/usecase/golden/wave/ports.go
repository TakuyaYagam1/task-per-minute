package golden

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	goldenexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/execution"
	goldenplan "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/plan"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
)

type GoldenReadyWindow = goldenstate.GoldenReadyWindow
type GoldenReadyWindowExpectation = goldenstate.GoldenReadyWindowExpectation
type GoldenReadyWindowState = goldenstate.GoldenReadyWindowState
type GoldenState = goldenstate.GoldenState
type GoldenStateExpectation = goldenstate.GoldenStateExpectation
type GoldenStateScope = goldenstate.GoldenStateScope

const (
	GoldenReadyWindowOpen     = goldenstate.GoldenReadyWindowOpen
	GoldenReadyWindowExpired  = goldenstate.GoldenReadyWindowExpired
	GoldenReadyWindowConsumed = goldenstate.GoldenReadyWindowConsumed
)

type GoldenWaveCommandKind = goldenexecution.GoldenWaveCommandKind
type GoldenWaveMembershipBinding = goldenexecution.GoldenWaveMembershipBinding
type GoldenPrivateAssignment = goldenexecution.GoldenPrivateAssignment
type GoldenAttemptAssignment = goldenexecution.GoldenAttemptAssignment
type GoldenWaveExecutionExpectation = goldenexecution.GoldenWaveExecutionExpectation
type GoldenWaveCommandReceipt = goldenexecution.GoldenWaveCommandReceipt
type GoldenWaveCommandReplay = goldenexecution.GoldenWaveCommandReplay
type GoldenWaveExecution = goldenexecution.GoldenWaveExecution
type GoldenStartAuthority = goldenexecution.GoldenStartAuthority
type GoldenStartedAssignment = goldenexecution.GoldenStartedAssignment
type GoldenStartRecord = goldenexecution.GoldenStartRecord

const (
	GoldenWaveCommandOpened       = goldenexecution.GoldenWaveCommandOpened
	GoldenWaveCommandReady        = goldenexecution.GoldenWaveCommandReady
	GoldenWaveCommandDisconnected = goldenexecution.GoldenWaveCommandDisconnected
	GoldenWaveCommandReconnected  = goldenexecution.GoldenWaveCommandReconnected
	GoldenWaveCommandStarted      = goldenexecution.GoldenWaveCommandStarted
)

type Group = goldenplan.Group
type Edge = goldenplan.Edge
type ExactPlan = goldenplan.ExactPlan

type WaveClock interface {
	Now() time.Time
}

// WaveRepository owns the execution compare-and-set. The source Golden state
// is read-only through this port.
type WaveRepository interface {
	LoadGoldenState(ctx context.Context, scope GoldenStateScope) (GoldenState, error)
	FindGoldenWaveCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*GoldenWaveCommandReplay, error)
	LoadGoldenWaveExecution(ctx context.Context, scope GoldenStateScope) (*GoldenWaveExecution, error)
	CommitGoldenWaveExecution(ctx context.Context, commit GoldenWaveExecutionCommit) (*GoldenWaveExecution, bool, error)
}

const goldenWaveCommitAttempts = 2

var (
	ErrInvalidGoldenWaveExecution = goldenexecution.ErrInvalidGoldenWaveExecution
	ErrGoldenReadyWindowClosed    = errors.New("golden ready window is closed")

	ErrGoldenReadyWindowIneligible = errors.New("golden ready window is ineligible")
	ErrGoldenWaveAuthorityConflict = errors.New("golden Wave authority conflict")
	ErrGoldenWaveCommitConflict    = errors.New("golden Wave commit conflict")
	ErrGoldenWaveCommandReuse      = errors.New("golden Wave command identifier was reused")
	ErrGoldenWaveIdentityConflict  = errors.New("golden Wave identity is already reserved")
	ErrGoldenWaveRevisionOverflow  = errors.New("golden Wave revision overflow")
	ErrGoldenWaveExecutionNotFound = errors.New("golden Wave execution was not found")
	ErrGoldenWaveAuthorityNotLive  = errors.New("golden Wave execution authority is not live")
)

type GoldenPrivateAssignmentCommand struct {
	ParticipantID uuid.UUID
	AssignmentID  uuid.UUID
}

type GoldenWaveAuthorityCondition struct {
	Identity      authoritydomain.Identity
	LeaseRevision int64
	LeaseDigest   [sha256.Size]byte
}

func (c GoldenWaveAuthorityCondition) Validate() error {
	if c.Identity.Validate() != nil || c.LeaseRevision < 1 || c.LeaseDigest == [sha256.Size]byte{} {
		return goldenWaveError("invalid execution authority condition")
	}
	return nil
}

type GoldenWaveExecutionCommit struct {
	ExpectedState     GoldenStateExpectation
	ExpectedExecution *GoldenWaveExecutionExpectation
	Authority         *GoldenWaveAuthorityCondition
	NewIdentityIDs    []uuid.UUID
	Next              GoldenWaveExecution
}

type OpenGoldenReadyWindowCommand struct {
	Scope         GoldenStateScope
	CommandID     uuid.UUID
	ExpectedState GoldenStateExpectation

	AttemptID            uuid.UUID
	WaveID               uuid.UUID
	WaveRevisionID       domain.WaveRevisionID
	WindowID             uuid.UUID
	WaveWindowRevisionID domain.ReadyWindowRevisionID
	WindowRevisionID     uuid.UUID
	ReadinessRevisionID  uuid.UUID
	PresenceRevisionID   uuid.UUID
	MembershipID         uuid.UUID
	MembershipRevisionID uuid.UUID
	AssignmentID         uuid.UUID
	AssignmentRevisionID uuid.UUID
	ExecutionRevisionID  uuid.UUID
	PrivateAssignments   []GoldenPrivateAssignmentCommand
}
