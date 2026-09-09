package pause

import (
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const pauseMaxTournamentCommandReasonLength = 512

var ErrTournamentNotFound = errors.New("tournament not found")

type PauseTournamentRecord struct {
	ID              uuid.UUID
	State           domain.TournamentState
	PausedFromState *domain.TournamentState
	Revision        int64
	UpdatedAt       time.Time
}

func pauseValidateTournamentRecordPointer(record *PauseTournamentRecord, tournamentID uuid.UUID) error {
	if record == nil || record.ID != tournamentID {
		return domain.ErrInternal
	}
	return pauseValidateTournamentRecord(*record)
}

func pauseValidateTournamentRecord(record PauseTournamentRecord) error {
	if record.ID == uuid.Nil || record.Revision < 1 || !pauseValidServerTime(record.UpdatedAt) {
		return domain.ErrInternal
	}
	if err := (domain.Tournament{
		State: record.State, PausedFromState: record.PausedFromState,
	}).Validate(); err != nil {
		return domain.ErrInternal
	}
	return nil
}

func pauseCloneTournamentRecord(record PauseTournamentRecord) *PauseTournamentRecord {
	cloned := record
	if record.PausedFromState != nil {
		state := *record.PausedFromState
		cloned.PausedFromState = &state
	}
	return &cloned
}

func pauseValidServerTime(value time.Time) bool {
	return domain.IsValidServerTime(value)
}
