package lifecycle

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type LifecycleClock interface {
	Now() time.Time
}

type TournamentLifecycleCommand struct {
	TournamentID     uuid.UUID
	ExpectedRevision int64
	NextState        domain.TournamentState
}

type TournamentLifecycleTransitionInput struct {
	TournamentID     uuid.UUID
	ExpectedRevision int64
	ExpectedState    domain.TournamentState
	NextState        domain.TournamentState
	PausedFromState  *domain.TournamentState
	TransitionedAt   time.Time
	StartedAt        *time.Time
	FinishedAt       *time.Time
}

// TournamentLifecycleRepository owns the atomic lifecycle compare-and-set for
// one tournament. Terminal transitions release that tournament's participant
// reservations in the same transaction.
type TournamentLifecycleRepository interface {
	GetTournament(ctx context.Context, id uuid.UUID) (*LifecycleTournamentRecord, error)
	TransitionTournament(
		ctx context.Context,
		in TournamentLifecycleTransitionInput,
	) (*LifecycleTournamentRecord, bool, error)
}
