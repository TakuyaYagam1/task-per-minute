package snapshot

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

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

func lifecycleRequiredTime(valid bool, value time.Time) (time.Time, bool) {
	if !valid {
		return time.Time{}, false
	}
	result := value.UTC()
	return result, domain.IsValidServerTime(result)
}

func lifecycleOptionalTime(valid bool, value time.Time) (*time.Time, bool) {
	if !valid {
		return nil, true
	}
	result := value.UTC()
	if !domain.IsValidServerTime(result) {
		return nil, false
	}
	return &result, true
}

func lifecycleTournamentState(value *string) *domain.TournamentState {
	if value == nil {
		return nil
	}
	state := domain.TournamentState(*value)
	return &state
}
