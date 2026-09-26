package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// PublicTournamentCatalogUseCase is the anonymous read boundary for the
// redacted tournament catalog. It is intentionally separate from the
// operator-owned tournament catalog contract.
type PublicTournamentCatalogUseCase interface {
	ListPublicTournaments(ctx context.Context, query PublicTournamentCatalogQuery) (PublicTournamentCatalogPage, error)
	GetPublicTournamentByPublicID(ctx context.Context, publicID string) (PublicTournamentCatalogView, error)
}

type PublicTournamentCatalogFilterGroup string

const (
	PublicTournamentCatalogFilterAll       PublicTournamentCatalogFilterGroup = "all"
	PublicTournamentCatalogFilterLive      PublicTournamentCatalogFilterGroup = "live"
	PublicTournamentCatalogFilterUpcoming  PublicTournamentCatalogFilterGroup = "upcoming"
	PublicTournamentCatalogFilterCompleted PublicTournamentCatalogFilterGroup = "completed"
)

func (g PublicTournamentCatalogFilterGroup) IsValid() bool {
	switch g {
	case PublicTournamentCatalogFilterAll,
		PublicTournamentCatalogFilterLive,
		PublicTournamentCatalogFilterUpcoming,
		PublicTournamentCatalogFilterCompleted:
		return true
	default:
		return false
	}
}

type PublicTournamentCatalogGroup string

const (
	PublicTournamentCatalogGroupLive      PublicTournamentCatalogGroup = "live"
	PublicTournamentCatalogGroupUpcoming  PublicTournamentCatalogGroup = "upcoming"
	PublicTournamentCatalogGroupCompleted PublicTournamentCatalogGroup = "completed"
)

func (g PublicTournamentCatalogGroup) IsValid() bool {
	switch g {
	case PublicTournamentCatalogGroupLive,
		PublicTournamentCatalogGroupUpcoming,
		PublicTournamentCatalogGroupCompleted:
		return true
	default:
		return false
	}
}

type PublicTournamentCatalogSort string

const (
	PublicTournamentCatalogSortActivity PublicTournamentCatalogSort = "activity"
	PublicTournamentCatalogSortName     PublicTournamentCatalogSort = "name"
	PublicTournamentCatalogSortNewest   PublicTournamentCatalogSort = "newest"
)

func (s PublicTournamentCatalogSort) IsValid() bool {
	switch s {
	case PublicTournamentCatalogSortActivity,
		PublicTournamentCatalogSortName,
		PublicTournamentCatalogSortNewest:
		return true
	default:
		return false
	}
}

type PublicTournamentCatalogQuery struct {
	Search string
	Group  PublicTournamentCatalogFilterGroup
	Sort   PublicTournamentCatalogSort
	Limit  int
	After  *PublicTournamentCatalogCursor
}

// PublicTournamentCatalogCursor contains the stable keyset position and the
// request binding that prevents a cursor from being reused with other filters.
type PublicTournamentCatalogCursor struct {
	Search       string
	Group        PublicTournamentCatalogFilterGroup
	Sort         PublicTournamentCatalogSort
	GroupRank    int
	Name         string
	CreatedAt    time.Time
	TournamentID uuid.UUID
}

type PublicTournamentCatalogPage struct {
	Items []PublicTournamentCatalogView
	Next  *PublicTournamentCatalogCursor
}

type PublicTournamentCatalogView struct {
	TournamentID      uuid.UUID
	PublicID          string
	Name              string
	Preset            domain.TournamentPreset
	State             domain.TournamentState
	Group             PublicTournamentCatalogGroup
	Stage             domain.TournamentState
	PlannedRosterSize int
	RosterSize        int
	CreatedAt         time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
	ScheduledAt       *time.Time
}
