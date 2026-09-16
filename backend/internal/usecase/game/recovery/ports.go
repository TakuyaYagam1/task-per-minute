package recovery

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
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
