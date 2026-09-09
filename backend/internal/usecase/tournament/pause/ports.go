package pause

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type PauseClock interface {
	Now() time.Time
}

type TournamentTechnicalPauseCommand struct {
	TournamentID     uuid.UUID
	ExpectedRevision int64
	CommandID        uuid.UUID
	PauseID          uuid.UUID
	ActorID          uuid.UUID
	Reason           string
	Confirmed        bool
}

type TournamentPauseAdmission struct {
	TournamentID     uuid.UUID
	GraphRevision    int64
	ExpectedChildren int
	ObservedChildren int
	ActiveGolden     bool
	Complete         bool
}

type TournamentTechnicalPauseInput struct {
	TournamentID     uuid.UUID
	ExpectedRevision int64
	ExpectedState    domain.TournamentState
	GraphRevision    int64
	CommandID        uuid.UUID
	PauseID          uuid.UUID
	ActorID          uuid.UUID
	Reason           string
	PausedAt         time.Time
}

type TournamentTechnicalPauseRecord struct {
	Tournament PauseTournamentRecord
	CommandID  uuid.UUID
	PauseID    uuid.UUID
	ActorID    uuid.UUID
	Reason     string
	PausedAt   time.Time
}

// TournamentPauseRepository must inspect and revalidate the child graph in the
// same transaction as the tournament transition. A snapshot is complete only
// when every expected child is represented at one graph revision.
type TournamentPauseRepository interface {
	GetTournament(ctx context.Context, id uuid.UUID) (*PauseTournamentRecord, error)
	GetTournamentTechnicalPause(
		ctx context.Context,
		tournamentID uuid.UUID,
		commandID uuid.UUID,
	) (*TournamentTechnicalPauseRecord, error)
	InspectTournamentPauseAdmission(
		ctx context.Context,
		tournamentID uuid.UUID,
	) (*TournamentPauseAdmission, error)
	EnterTournamentTechnicalPause(
		ctx context.Context,
		in TournamentTechnicalPauseInput,
	) (*TournamentTechnicalPauseRecord, bool, error)
}

type PauseTransactionManager interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}
