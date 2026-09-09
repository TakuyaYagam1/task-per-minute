package cancellation

import (
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var CancellationErrTournamentNotFound = errors.New("tournament not found")

type CancellationTournamentRecord struct {
	ID              uuid.UUID
	State           domain.TournamentState
	PausedFromState *domain.TournamentState
	Revision        int64
	UpdatedAt       time.Time
	FinishedAt      *time.Time
}

func cancellationValidateTournamentRecordPointer(record *CancellationTournamentRecord, tournamentID uuid.UUID) error {
	if record == nil || record.ID != tournamentID {
		return domain.ErrInternal
	}
	return cancellationValidateTournamentRecord(*record)
}

func cancellationValidateTournamentRecord(record CancellationTournamentRecord) error {
	if record.ID == uuid.Nil || record.Revision < 1 || !cancellationValidServerTime(record.UpdatedAt) {
		return domain.ErrInternal
	}
	if err := (domain.Tournament{
		State: record.State, PausedFromState: record.PausedFromState,
	}).Validate(); err != nil {
		return domain.ErrInternal
	}
	if record.FinishedAt != nil && !cancellationValidServerTime(*record.FinishedAt) {
		return domain.ErrInternal
	}
	return nil
}

func cancellationCloneTournamentRecord(record CancellationTournamentRecord) *CancellationTournamentRecord {
	cloned := record
	if record.PausedFromState != nil {
		state := *record.PausedFromState
		cloned.PausedFromState = &state
	}
	if record.FinishedAt != nil {
		value := *record.FinishedAt
		cloned.FinishedAt = &value
	}
	return &cloned
}

func cancellationValidServerTime(value time.Time) bool {
	return domain.IsValidServerTime(value)
}
