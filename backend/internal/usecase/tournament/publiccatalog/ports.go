package publiccatalog

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

type PublicTournamentRecord struct {
	TournamentID      uuid.UUID
	PublicID          string
	Name              string
	OrderName         string
	Preset            domain.TournamentPreset
	State             domain.TournamentState
	Group             inbound.PublicTournamentCatalogGroup
	Stage             domain.TournamentState
	PlannedRosterSize int
	RosterSize        int
	CreatedAt         time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
	ScheduledAt       *time.Time
}

type PublicTournamentListPage struct {
	Items   []PublicTournamentRecord
	HasMore bool
}

type Repository interface {
	ListPublicTournaments(ctx context.Context, query inbound.PublicTournamentCatalogQuery) (PublicTournamentListPage, error)
	GetPublicTournamentByPublicID(ctx context.Context, publicID string) (PublicTournamentRecord, error)
}
