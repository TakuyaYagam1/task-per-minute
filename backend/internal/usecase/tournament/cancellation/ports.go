package cancellation

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type CancellationClock interface {
	Now() time.Time
}

type TournamentCancellationCommand struct {
	TournamentID     uuid.UUID
	ExpectedRevision int64
	CommandID        uuid.UUID
	ActorID          uuid.UUID
	Reason           string
	Confirmed        bool
}

type TournamentCancellationInput struct {
	TournamentID     uuid.UUID
	ExpectedRevision int64
	ExpectedState    domain.TournamentState
	CommandID        uuid.UUID
	ActorID          uuid.UUID
	Reason           string
	CancelledAt      time.Time
}

type TournamentCancellationRecord struct {
	Tournament    CancellationTournamentRecord
	CommandID     uuid.UUID
	ActorID       uuid.UUID
	Reason        string
	AuditEventID  uuid.UUID
	OutboxEventID uuid.UUID
	CancelledAt   time.Time
	ChampionID    *uuid.UUID
}

// TournamentCancellationRepository owns the full cancellation transaction.
// The commit must retain prior result evidence, clear future-start authority,
// block later participant mutations, append audit and terminal outbox rows,
// release the active slot and reservations, and leave champion evidence empty.
type TournamentCancellationRepository interface {
	GetTournament(ctx context.Context, id uuid.UUID) (*CancellationTournamentRecord, error)
	GetTournamentCancellation(
		ctx context.Context,
		tournamentID uuid.UUID,
		commandID uuid.UUID,
	) (*TournamentCancellationRecord, error)
	CancelTournament(
		ctx context.Context,
		in TournamentCancellationInput,
	) (*TournamentCancellationRecord, bool, error)
}
