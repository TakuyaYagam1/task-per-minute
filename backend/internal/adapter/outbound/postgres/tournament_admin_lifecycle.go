package postgres

import (
	"time"

	"github.com/google/uuid"

	lifecyclepostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/lifecycle"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

type TournamentAdminLifecyclePostgres = lifecyclepostgres.TournamentAdminLifecyclePostgres

func NewTournamentAdminLifecyclePostgres(tx *TxManager) *TournamentAdminLifecyclePostgres {
	return lifecyclepostgres.NewTournamentAdminLifecyclePostgres(tx)
}

// validLifecycleTournamentView preserves the private root helper used by the
// unmoved tournament progression adapter.
func validLifecycleTournamentView(view usecase.TournamentView, tournamentID uuid.UUID) bool {
	if view.ID != tournamentID || view.RosterID == uuid.Nil || !view.Preset.IsValid() ||
		view.Revision < 1 || view.RosterSize < 0 || view.RosterSize > domain.TournamentMaxParticipants ||
		!domain.IsValidServerTime(view.CreatedAt) || !domain.IsValidServerTime(view.UpdatedAt) ||
		view.UpdatedAt.Before(view.CreatedAt) ||
		(domain.Tournament{State: view.State, PausedFromState: view.PausedFromState}).Validate() != nil {
		return false
	}
	return validLifecycleEventTime(view.StartedAt, view.CreatedAt, view.UpdatedAt) &&
		validLifecycleEventTime(view.FinishedAt, view.CreatedAt, view.UpdatedAt) &&
		(view.StartedAt == nil || view.FinishedAt == nil || !view.FinishedAt.Before(*view.StartedAt))
}

func validLifecycleEventTime(value *time.Time, createdAt time.Time, updatedAt time.Time) bool {
	return value == nil || domain.IsValidServerTime(*value) &&
		!value.Before(createdAt) && !value.After(updatedAt)
}
