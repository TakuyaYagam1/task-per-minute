package arena

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type Clock interface {
	Now() time.Time
}

type TransactionManager interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

type TournamentRepository interface {
	CreateTournamentDraft(
		ctx context.Context,
		tournamentID uuid.UUID,
		rosterID uuid.UUID,
		createdAt time.Time,
	) (*TournamentRecord, *RosterRecord, error)
	GetTournament(ctx context.Context, id uuid.UUID) (*TournamentRecord, error)
	ListTournaments(ctx context.Context) ([]TournamentRecord, error)
}

type AttendanceRepository interface {
	InviteParticipant(ctx context.Context, in ParticipantInput) (*ParticipantRecord, bool, error)
	ChangeAttendance(
		ctx context.Context,
		participantID uuid.UUID,
		expected domain.ArenaAttendanceState,
		next domain.ArenaAttendanceState,
		updatedAt time.Time,
	) (*ParticipantRecord, bool, error)
	ReplaceWithdrawnParticipant(
		ctx context.Context,
		in ParticipantReplacementInput,
	) (*ParticipantRecord, bool, error)
	ListRosterParticipants(ctx context.Context, rosterID uuid.UUID) ([]ParticipantRecord, error)
}

type RosterLockRepository interface {
	GetRosterSnapshot(ctx context.Context, id uuid.UUID) (*RosterRecord, error)
	LockRosterAndReserveExpected(
		ctx context.Context,
		rosterID uuid.UUID,
		expectedRevision int64,
		expectedPlayerIDs []uuid.UUID,
		lockedAt time.Time,
	) (*RosterRecord, bool, error)
	UnlockRosterAndReleaseExpected(
		ctx context.Context,
		rosterID uuid.UUID,
		expectedRevision int64,
		updatedAt time.Time,
	) (*RosterRecord, bool, error)
}

type GameRepository interface {
	Get(ctx context.Context, scope GameScope) (*domain.ArenaGame, error)
	Update(
		ctx context.Context,
		scope GameScope,
		expected domain.ArenaGameState,
		next domain.ArenaGame,
	) (*domain.ArenaGame, bool, error)
}

// TaskSnapshotter derives a snapshot without writing task or game state.
type TaskSnapshotter interface {
	Snapshot(ctx context.Context, scope GameScope, input TaskSnapshotInput) (domain.ArenaTaskSnapshot, error)
}

// FlagValidator checks a submitted flag without settling the game.
type FlagValidator interface {
	Validate(ctx context.Context, scope GameScope, input FlagValidationInput) (bool, error)
}

// SubmissionOrderer orders values without recording or settling them.
type SubmissionOrderer interface {
	Order(ctx context.Context, scope GameScope, submissions []Submission) ([]Submission, error)
}
