package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

type CatalogClock interface {
	Now() time.Time
}

type CatalogTournamentRecord struct {
	ID                uuid.UUID
	RosterID          uuid.UUID
	Preset            domain.TournamentPreset
	Name              string
	PublicID          string
	PlannedRosterSize int
	ContentRevision   int64
	State             domain.TournamentState
	PausedFromState   *domain.TournamentState
	Revision          int64
	RosterSize        int
	CreatedAt         time.Time
	UpdatedAt         time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
}

type CatalogRosterRecord struct {
	ID           uuid.UUID
	TournamentID uuid.UUID
}

type TournamentCreateCommand struct {
	TournamentID      uuid.UUID
	RosterID          uuid.UUID
	Name              string
	PublicID          string
	PlannedRosterSize int
	ContentRevision   int64
}

type TournamentListFilter struct {
	States []domain.TournamentState
}

type TournamentRepository interface {
	CreateTournamentDraft(
		ctx context.Context,
		command TournamentCreateCommand,
		createdAt time.Time,
	) (*CatalogTournamentRecord, *CatalogRosterRecord, error)
	GetTournament(ctx context.Context, id uuid.UUID) (*CatalogTournamentRecord, error)
	ListTournaments(ctx context.Context) ([]CatalogTournamentRecord, error)
}

type TournamentLister interface {
	ListTournaments(
		ctx context.Context,
		filter TournamentListFilter,
	) ([]CatalogTournamentRecord, error)
}

type TournamentCreateStore interface {
	Create(ctx context.Context, command CreateReceiptCommand) (usecase.TournamentResult, error)
}

type TournamentContentRecord struct {
	ContentRevision      int64
	PublicationID        uuid.UUID
	PublishedAt          time.Time
	NormalPoolRevisionID uuid.UUID
	GoldenPoolRevisionID uuid.UUID
}

type ContentReader interface {
	GetTournamentContent(ctx context.Context) (TournamentContentRecord, error)
}

var _ TournamentLister = (*TournamentUseCase)(nil)

type IDScope string

const (
	IDScopeTournament IDScope = "tournament"
	IDScopeRoster     IDScope = "roster"
)

type IDInput struct {
	Scope          IDScope
	IdempotencyKey uuid.UUID
}

type IDGenerator interface {
	Derive(in IDInput) (uuid.UUID, error)
}

type CreateReceiptCommand struct {
	ActorID           uuid.UUID
	IdempotencyKey    uuid.UUID
	PayloadDigest     [32]byte
	TournamentID      uuid.UUID
	RosterID          uuid.UUID
	Name              string
	PublicID          string
	PlannedRosterSize int
	ContentRevision   int64
	CreatedAt         time.Time
}

type Clock interface {
	Now() time.Time
}
