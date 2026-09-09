package lifecycle

import (
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrTournamentNotFound = errors.New("tournament not found")

type LifecycleTournamentRecord struct {
	ID              uuid.UUID
	State           domain.TournamentState
	PausedFromState *domain.TournamentState
	Revision        int64
	UpdatedAt       time.Time
	StartedAt       *time.Time
	FinishedAt      *time.Time
}

func lifecycleValidateTournamentRecord(record LifecycleTournamentRecord) error {
	if record.ID == uuid.Nil || record.Revision < 1 || !lifecycleValidServerTime(record.UpdatedAt) {
		return domain.ErrInternal
	}
	if err := (domain.Tournament{
		State: record.State, PausedFromState: record.PausedFromState,
	}).Validate(); err != nil {
		return domain.ErrInternal
	}
	for _, timestamp := range []*time.Time{record.StartedAt, record.FinishedAt} {
		if timestamp != nil && !lifecycleValidServerTime(*timestamp) {
			return domain.ErrInternal
		}
	}
	return nil
}

func lifecycleCloneTournamentRecord(record LifecycleTournamentRecord) *LifecycleTournamentRecord {
	cloned := record
	if record.PausedFromState != nil {
		state := *record.PausedFromState
		cloned.PausedFromState = &state
	}
	if record.StartedAt != nil {
		value := *record.StartedAt
		cloned.StartedAt = &value
	}
	if record.FinishedAt != nil {
		value := *record.FinishedAt
		cloned.FinishedAt = &value
	}
	return &cloned
}

func lifecycleValidServerTime(value time.Time) bool {
	return domain.IsValidServerTime(value)
}
