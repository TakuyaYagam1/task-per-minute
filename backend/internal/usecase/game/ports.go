package game

import (
	"context"
	"errors"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	attemptusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/attempt"
	reconnectusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
	"github.com/google/uuid"
)

type ExecutionClock interface {
	Now() time.Time
}

// AuthorityTimeSource supplies the database time used to prove durable
// execution authority. It is deliberately context-bound: an unavailable
// authoritative clock must prevent recovery mutations rather than falling
// back to the process wall clock.
type AuthorityTimeSource interface {
	AuthorityTime(ctx context.Context) (time.Time, error)
}

// EpochReplayRepository owns the atomic revalidation boundary. CommitEpochReplay
// must prove that the expected lease is still live, then persist the failed
// attempt records and the old-wave route in one transaction.
type EpochReplayRepository interface {
	FindEpochReplay(
		ctx context.Context,
		scope domain.FailedAttemptScope,
	) (*EpochReplayRecord, error)
	LoadEpochReplayAuthority(
		ctx context.Context,
		scope domain.FailedAttemptScope,
		rosterID uuid.UUID,
	) (EpochReplayAuthority, error)
	CommitEpochReplay(
		ctx context.Context,
		condition EpochReplayCommitCondition,
		record EpochReplayRecord,
	) (*EpochReplayRecord, bool, error)
}

type RecoveryCommand struct {
	TournamentID uuid.UUID
	Authority    authoritydomain.Identity
}

type RecoveryCandidate struct {
	Scope          domain.FailedAttemptScope
	RosterID       uuid.UUID
	AttemptNo      int
	State          domain.GameState
	SnapshotID     uuid.UUID
	Category       domain.Category
	BoundAuthority authoritydomain.Stamp
	Deadline       time.Time
	EpochReplay    *EpochReplayCommand
}

type DeadlineArm struct {
	Scope     domain.FailedAttemptScope
	RosterID  uuid.UUID
	AttemptNo int
	Authority authoritydomain.Stamp
	Deadline  time.Time
}

type RecoveryReport struct {
	Rearmed          int
	TechnicalReplays int
	Paused           int
	Changed          int
}

type RecoveryAuthorityReader interface {
	LoadAuthority(ctx context.Context, tournamentID uuid.UUID) (*authoritydomain.Lease, error)
}

type RecoverySource interface {
	ListActiveGames(
		ctx context.Context,
		tournamentID uuid.UUID,
		authority authoritydomain.Identity,
	) ([]RecoveryCandidate, error)
}

// DeadlineRearmer owns the deadline-arm linearization boundary. RearmDeadline
// must fence by tournament scope and authority stamp, prove the durable lease
// is live using authoritative time, and arm the persisted deadline atomically.
type DeadlineRearmer interface {
	RearmDeadline(ctx context.Context, arm DeadlineArm) error
}

type RecoveryEpochReplayer interface {
	ReplayEpoch(ctx context.Context, command EpochReplayCommand) (bool, error)
}

// RecoveryTournamentSource lists durable tournament work. It must not infer
// ownership from process-local state.
type RecoveryTournamentSource interface {
	ListRecoveryTournaments(ctx context.Context) ([]uuid.UUID, error)
}

// RecoveryAuthorityProvider distinguishes a live foreign lease from a local
// infrastructure failure. Foreign work is skipped without takeover.
type RecoveryAuthorityProvider interface {
	RecoveryAuthorityFor(
		ctx context.Context,
		tournamentID uuid.UUID,
	) (authority authoritydomain.Identity, owned bool, err error)
}
type ForfeitClock interface {
	Now() time.Time
}

type ForfeitRepository interface {
	LoadForfeitAuthority(ctx context.Context, scope Scope) (ForfeitAuthority, error)
	CommitForfeitResolution(
		ctx context.Context,
		resolution ForfeitResolution,
	) (*ForfeitResolution, bool, error)
}
type NoShowClock interface {
	Now() time.Time
}

type NoShowRepository interface {
	LoadNormalNoShowAuthority(
		ctx context.Context,
		scope domain.NormalNoShowScope,
	) (NoShowAuthority, error)
	CommitNormalNoShow(
		ctx context.Context,
		resolution NoShowResolution,
	) (*NoShowResolution, bool, error)
}
type AttemptClock = attemptusecase.AttemptClock
type AttemptRepository = attemptusecase.AttemptRepository
type ReconnectClock = reconnectusecase.ReconnectClock
type Observer = reconnectusecase.Observer
type ReconnectRepository = reconnectusecase.ReconnectRepository
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
	FailedAttempt  AttemptRecord
	OldWaveClosure Closure
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
		command AttemptCommand,
	) (*AttemptRecord, bool, error)
}

type OldWaveCloser interface {
	Close(
		ctx context.Context,
		command CloseCommand,
	) (*Closure, bool, error)
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
	FailedAttempt  AttemptRecord
	OldWaveClosure Closure
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
	Terminalize AttemptCommand
	Close       CloseCommand
	Replace     ReplayReplacementCommand
}

type NoSolveReplayResult struct {
	FailedAttempt  *AttemptRecord
	OldWaveClosure *Closure
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
type WaveClock interface {
	Now() time.Time
}

type CloseRepository interface {
	LoadCloseAuthority(ctx context.Context, scope CloseScope) (CloseAuthority, error)
	CommitClosure(ctx context.Context, closure Closure) (*Closure, bool, error)
}
