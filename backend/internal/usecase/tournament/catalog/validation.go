package catalog

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func validOperatorIdentity(identity usecase.OperatorIdentity) bool {
	return identity.ActorID != uuid.Nil
}

func validListCommand(command usecase.TournamentListCommand) bool {
	if command.State != "" && !command.State.IsValid() {
		return false
	}
	if command.PageSize < 0 || command.PageSize > usecase.MaxTournamentPageSize {
		return false
	}
	return command.After == nil ||
		(command.After.TournamentID != uuid.Nil && validServerTime(command.After.CreatedAt))
}

func validCreateCommand(command usecase.TournamentCreateCommand) bool {
	return command.IdempotencyKey != uuid.Nil && command.ExpectedRevision == 0 &&
		command.Preset.IsValid() && (domain.TournamentMetadata{Name: command.Name, PublicID: command.PublicID,
		PlannedRosterSize: command.PlannedRosterSize, ContentRevision: command.ContentRevision}).Validate(command.Preset) == nil
}

func validateCreateResult(
	result usecase.TournamentResult,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	command usecase.TournamentCreateCommand,
) error {
	view := result.Tournament
	if !result.Changed || view.ID != tournamentID || view.RosterID != rosterID || !matchesCreateMetadata(view, command) ||
		view.State != domain.TournamentStateDraft || view.PausedFromState != nil || view.Revision != 1 ||
		view.RosterSize != 0 || view.StartedAt != nil || view.FinishedAt != nil ||
		!view.UpdatedAt.Equal(view.CreatedAt) {
		return domain.ErrInternal
	}
	record := CatalogTournamentRecord{
		ID: view.ID, RosterID: view.RosterID, Preset: view.Preset, Name: view.Name, PublicID: view.PublicID,
		PlannedRosterSize: view.PlannedRosterSize, ContentRevision: view.ContentRevision, State: view.State,
		PausedFromState: view.PausedFromState, Revision: view.Revision, RosterSize: view.RosterSize,
		CreatedAt: view.CreatedAt, UpdatedAt: view.UpdatedAt, StartedAt: view.StartedAt, FinishedAt: view.FinishedAt,
	}
	return validateTournamentRecord(record)
}

func matchesCreateMetadata(view usecase.TournamentView, command usecase.TournamentCreateCommand) bool {
	return view.Preset == command.Preset && view.Name == command.Name && view.PublicID == command.PublicID &&
		view.PlannedRosterSize == command.PlannedRosterSize && view.ContentRevision == command.ContentRevision
}

func validateTournamentRecord(record CatalogTournamentRecord) error {
	if record.ID == uuid.Nil || record.RosterID == uuid.Nil || !record.Preset.IsValid() ||
		record.Revision < 1 || record.RosterSize < 0 || record.RosterSize > domain.TournamentMaxParticipants ||
		!validServerTime(record.CreatedAt) || !validServerTime(record.UpdatedAt) ||
		record.UpdatedAt.Before(record.CreatedAt) {
		return domain.ErrInternal
	}
	if err := (domain.TournamentMetadata{Name: record.Name, PublicID: record.PublicID,
		PlannedRosterSize: record.PlannedRosterSize, ContentRevision: record.ContentRevision}).Validate(record.Preset); err != nil {
		return domain.ErrInternal
	}
	if err := (domain.Tournament{
		State: record.State, PausedFromState: record.PausedFromState,
	}).Validate(); err != nil {
		return domain.ErrInternal
	}
	for _, timestamp := range []*time.Time{record.StartedAt, record.FinishedAt} {
		if timestamp != nil && !validServerTime(*timestamp) {
			return domain.ErrInternal
		}
	}
	return nil
}

func validServerTime(value time.Time) bool {
	return domain.IsValidServerTime(value)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneState(value *domain.TournamentState) *domain.TournamentState {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneTournamentResult(result usecase.TournamentResult) usecase.TournamentResult {
	result.Tournament.PausedFromState = cloneState(result.Tournament.PausedFromState)
	result.Tournament.StartedAt = cloneTime(result.Tournament.StartedAt)
	result.Tournament.FinishedAt = cloneTime(result.Tournament.FinishedAt)
	return result
}
