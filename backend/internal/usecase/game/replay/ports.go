package replay

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	attemptusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/attempt"
	closeusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/close"
)

type ReplayClock interface {
	Now() time.Time
}

const replayReserveExhaustionAttempts = 2

var (
	ErrInvalidReplayReserveExhaustion  = errors.New("invalid replay reserve exhaustion")
	ErrReplayReserveExhaustionConflict = errors.New("replay reserve exhaustion conflict")
	ErrReplayReserveExhaustionReuse    = errors.New("replay reserve exhaustion command was reused")
)

type ReplayReserveExhaustionCommand struct {
	Scope                     ReplayReplacementScope
	CommandID                 uuid.UUID
	ExpectedClosureRevisionID domain.WaveRevisionID
	ExpectedActiveSnapshotID  uuid.UUID
}

type ReplayReserveExhaustionAuthority struct {
	Scope          ReplayReplacementScope
	Revision       int64
	FailedAttempt  attemptusecase.AttemptRecord
	OldWaveClosure closeusecase.Closure
	ReserveChain   ReplayReserveChain
	Current        *ReplayReserveExhaustion
}

type ReplayReserveExhaustion struct {
	Scope                     ReplayReplacementScope
	CommandID                 uuid.UUID
	ExpectedAuthorityRevision int64
	ClosureRevisionID         domain.WaveRevisionID
	FailedAttemptCommandID    uuid.UUID
	AssignmentAttemptID       uuid.UUID
	GameID                    uuid.UUID
	ActiveSnapshotID          uuid.UUID
	ReservePosition           int
	Category                  domain.Category
	PreviousSeries            seriesdomain.Execution
	Series                    seriesdomain.Execution
	OldWave                   domain.Wave
}

// ReplayReserveExhaustionRepository owns one transaction that revalidates the
// failed attempt, completed old Wave and fully consumed reserve chain before
// pausing the replay-required Series without creating a replacement Wave.
type ReplayReserveExhaustionRepository interface {
	LoadReplayReserveExhaustionAuthority(
		ctx context.Context,
		scope ReplayReplacementScope,
	) (ReplayReserveExhaustionAuthority, error)
	CommitReplayReserveExhaustion(
		ctx context.Context,
		record ReplayReserveExhaustion,
	) (*ReplayReserveExhaustion, bool, error)
}

type FailedAttemptTerminalizer interface {
	Terminalize(
		ctx context.Context,
		command attemptusecase.AttemptCommand,
	) (*attemptusecase.AttemptRecord, bool, error)
}

type OldWaveCloser interface {
	Close(
		ctx context.Context,
		command closeusecase.CloseCommand,
	) (*closeusecase.Closure, bool, error)
}

type ReplayReplacementPlanner interface {
	Replace(
		ctx context.Context,
		command ReplayReplacementCommand,
	) (*ReplayReplacement, bool, error)
}

const operatorReserveAttempts = 2

var (
	ErrInvalidOperatorReserve  = errors.New("invalid operator reserve")
	ErrOperatorReserveConflict = errors.New("operator reserve conflict")
	ErrOperatorReserveReuse    = errors.New("operator reserve command was reused")
)

type OperatorReserveCommand struct {
	Scope                       ReplayReplacementScope
	CommandID                   uuid.UUID
	ExpectedExhaustionCommandID uuid.UUID
	ExpectedRevisions           assignmentusecase.ReserveAssignmentSourceRevisions
	ProposedTaskID              uuid.UUID
	ProposedVersion             int
	ProposedSnapshotID          uuid.UUID
	Reserve                     assignmentusecase.ReserveAssignmentCommand
}

type OperatorReserveAuthority struct {
	Scope      ReplayReplacementScope
	Revision   int64
	Exhaustion ReplayReserveExhaustion
	Reserve    assignmentusecase.ReserveAssignmentAuthority
	Current    *OperatorReserve
}

type OperatorReserve struct {
	Scope                     ReplayReplacementScope
	CommandID                 uuid.UUID
	ExpectedAuthorityRevision int64
	Exhaustion                ReplayReserveExhaustion
	Reserve                   assignmentusecase.ReserveAssignmentRecord
	Series                    seriesdomain.Execution
}

// OperatorReserveRepository owns one transaction that locks the paused Series
// and reserve-exhaustion evidence, revalidates the current reserve candidate,
// commits its assignment, and resumes the Series atomically.
type OperatorReserveRepository interface {
	LoadOperatorReserveAuthority(
		ctx context.Context,
		scope ReplayReplacementScope,
	) (OperatorReserveAuthority, error)
	CommitOperatorReserve(
		ctx context.Context,
		record OperatorReserve,
	) (*OperatorReserve, bool, error)
}

const (
	replayReplacementAttempts      = 2
	baseReplayReserveSnapshots     = domain.AssignmentReserveCount + 1
	operatorReplayReserveSnapshots = baseReplayReserveSnapshots + 1
)

var (
	ErrInvalidReplayReplacement  = errors.New("invalid replay replacement")
	ErrReplayReplacementConflict = errors.New("replay replacement conflict")
	ErrReplayReplacementReuse    = errors.New("replay replacement command was reused")
	ErrReplayReservesExhausted   = errors.New("replay reserves exhausted")
)

type ReplayReplacementScope struct {
	TournamentID uuid.UUID
	OldWaveID    uuid.UUID
	SeriesID     uuid.UUID
	SlotID       uuid.UUID
	AssignmentID uuid.UUID
}

type ReplayReserveChain struct {
	AssignmentID uuid.UUID
	ActiveIndex  int
	Snapshots    []domain.AssignmentTaskSnapshot
}

type ReplayReplacementAuthority struct {
	Scope          ReplayReplacementScope
	Revision       int64
	FailedAttempt  attemptusecase.AttemptRecord
	OldWaveClosure closeusecase.Closure
	ReserveChain   ReplayReserveChain
	ParticipantIDs [2]uuid.UUID
	Current        *ReplayReplacement
}

type ReplayReplacementCommand struct {
	Scope                     ReplayReplacementScope
	CommandID                 uuid.UUID
	ExpectedClosureRevisionID domain.WaveRevisionID
	AssignmentAttemptID       uuid.UUID
	GameID                    uuid.UUID
	WaveID                    uuid.UUID
	WaveRevisionID            domain.WaveRevisionID
	ReadyWindowID             uuid.UUID
	ReadyWindowRevisionID     domain.ReadyWindowRevisionID
}

type ReplayReplacement struct {
	Scope                     ReplayReplacementScope
	CommandID                 uuid.UUID
	ExpectedAuthorityRevision int64
	ClosureRevisionID         domain.WaveRevisionID
	FromSnapshotID            uuid.UUID
	AssignmentAttemptID       uuid.UUID
	ReservePosition           int
	Snapshot                  domain.AssignmentTaskSnapshot
	Category                  domain.Category
	Slot                      domain.GameSlot
	Game                      domain.Game
	Wave                      domain.Wave
	OpenedAt                  time.Time
}

type NoSolveReplayCommand struct {
	Terminalize attemptusecase.AttemptCommand
	Close       closeusecase.CloseCommand
	Replace     ReplayReplacementCommand
}

type NoSolveReplayResult struct {
	FailedAttempt  *attemptusecase.AttemptRecord
	OldWaveClosure *closeusecase.Closure
	Replacement    *ReplayReplacement
	Exhausted      bool
}

// ReplayReplacementRepository revalidates committed failure and old-Wave
// evidence, advances one planned reserve, and writes the fresh execution
// identities atomically.
type ReplayReplacementRepository interface {
	LoadReplayReplacementAuthority(
		ctx context.Context,
		scope ReplayReplacementScope,
	) (ReplayReplacementAuthority, error)
	CommitReplayReplacement(
		ctx context.Context,
		replacement ReplayReplacement,
	) (*ReplayReplacement, bool, error)
}
