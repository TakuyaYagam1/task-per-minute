package postgres

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

func tournamentProgressionReceipt(
	row sqlc.FindTournamentStageProgressionRow,
) (*tournamentprogression.Receipt, error) {
	if row.CommandID == uuid.Nil || row.TournamentID == uuid.Nil || row.RosterID == uuid.Nil ||
		row.SourceProjectionRevisionID == uuid.Nil || row.SourceProjectionRevision < 1 ||
		row.SourceTournamentRevision < 1 || row.ResultingTournamentRevision != row.SourceTournamentRevision+1 ||
		row.RosterSize < 0 || row.RosterSize > domain.TournamentMaxParticipants {
		return nil, domain.ErrConflict
	}
	nextState, validAction := progressionActionState(tournamentprogression.Action(row.Action))
	if !validAction || row.ResultingTournamentState != string(nextState) {
		return nil, domain.ErrConflict
	}
	createdAt, valid := progressionRequiredTime(row.TournamentCreatedAt.Valid, row.TournamentCreatedAt.Time)
	if !valid {
		return nil, domain.ErrConflict
	}
	updatedAt, valid := progressionRequiredTime(row.TournamentUpdatedAt.Valid, row.TournamentUpdatedAt.Time)
	if !valid || !row.ExecutedAt.Valid || !updatedAt.Equal(row.ExecutedAt.Time.UTC()) {
		return nil, domain.ErrConflict
	}
	startedAt, valid := progressionOptionalTime(row.TournamentStartedAt.Valid, row.TournamentStartedAt.Time)
	if !valid {
		return nil, domain.ErrConflict
	}
	finishedAt, valid := progressionOptionalTime(row.TournamentFinishedAt.Valid, row.TournamentFinishedAt.Time)
	if !valid {
		return nil, domain.ErrConflict
	}
	view := usecase.TournamentView{
		ID:              row.TournamentID,
		RosterID:        row.RosterID,
		Preset:          domain.TournamentPreset(row.Preset),
		State:           nextState,
		Revision:        row.ResultingTournamentRevision,
		RosterSize:      int(row.RosterSize),
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
		StartedAt:       startedAt,
		FinishedAt:      finishedAt,
		PausedFromState: nil,
	}
	if !validProgressionTournamentView(view, row.TournamentID) {
		return nil, domain.ErrConflict
	}
	return &tournamentprogression.Receipt{CommandID: row.CommandID, Result: view}, nil
}

func progressionActionState(action tournamentprogression.Action) (domain.TournamentState, bool) {
	switch action {
	case tournamentprogression.ActionStartGolden:
		return domain.TournamentStateGolden, true
	case tournamentprogression.ActionStartPlayoffs:
		return domain.TournamentStatePlayoffs, true
	default:
		return "", false
	}
}

func progressionRequiredTime(valid bool, value time.Time) (time.Time, bool) {
	if !valid {
		return time.Time{}, false
	}
	value = value.UTC()
	return value, domain.IsValidServerTime(value)
}

func progressionOptionalTime(valid bool, value time.Time) (*time.Time, bool) {
	if !valid {
		return nil, true
	}
	value = value.UTC()
	if !domain.IsValidServerTime(value) {
		return nil, false
	}
	return &value, true
}

func validProgressionTournamentView(view usecase.TournamentView, tournamentID uuid.UUID) bool {
	if view.ID != tournamentID || view.RosterID == uuid.Nil || !view.Preset.IsValid() || view.Revision < 1 ||
		view.RosterSize < 0 || view.RosterSize > domain.TournamentMaxParticipants ||
		!domain.IsValidServerTime(view.CreatedAt) || !domain.IsValidServerTime(view.UpdatedAt) ||
		view.UpdatedAt.Before(view.CreatedAt) ||
		(domain.Tournament{State: view.State, PausedFromState: view.PausedFromState}).Validate() != nil {
		return false
	}
	return validProgressionEventTime(view.StartedAt, view.CreatedAt, view.UpdatedAt) &&
		validProgressionEventTime(view.FinishedAt, view.CreatedAt, view.UpdatedAt) &&
		(view.StartedAt == nil || view.FinishedAt == nil || !view.FinishedAt.Before(*view.StartedAt))
}

func validProgressionEventTime(value *time.Time, createdAt, updatedAt time.Time) bool {
	return value == nil || domain.IsValidServerTime(*value) &&
		!value.Before(createdAt) && !value.After(updatedAt)
}
