package catalog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var CatalogErrTournamentNotFound = errors.New("tournament not found")

type TournamentUseCase struct {
	repository TournamentRepository
	clock      CatalogClock
}

func NewTournamentUseCase(repository TournamentRepository, clock CatalogClock) *TournamentUseCase {
	return &TournamentUseCase{repository: repository, clock: clock}
}

func (u *TournamentUseCase) CreateTournament(
	ctx context.Context,
	command TournamentCreateCommand,
) (*CatalogTournamentRecord, bool, error) {
	if !u.isAvailable() || !validTournamentCreateCommand(command) {
		return nil, false, domain.ErrValidation
	}

	existing, err := u.repository.GetTournament(ctx, command.TournamentID)
	if err == nil {
		return reconcileTournamentCreate(existing, command)
	}
	if !errors.Is(err, CatalogErrTournamentNotFound) {
		return nil, false, fmt.Errorf("TournamentUseCase - CreateTournament - TournamentRepository.Get: %w", err)
	}

	createdAt := u.clock.Now()
	if !catalogValidServerTime(createdAt) {
		return nil, false, domain.ErrValidation
	}
	created, roster, err := u.repository.CreateTournamentDraft(ctx, command.TournamentID, command.RosterID, createdAt)
	if err != nil {
		existing, getErr := u.repository.GetTournament(ctx, command.TournamentID)
		if getErr == nil {
			return reconcileTournamentCreate(existing, command)
		}
		return nil, false, fmt.Errorf("TournamentUseCase - CreateTournament - TournamentRepository.Create: %w", err)
	}
	if created == nil || roster == nil || roster.ID != command.RosterID || roster.TournamentID != command.TournamentID {
		return nil, false, domain.ErrInternal
	}
	created.RosterID = roster.ID
	created.RosterSize = 0
	if err := catalogValidateTournamentRecord(*created); err != nil {
		return nil, false, err
	}
	return catalogCloneTournamentRecord(*created), true, nil
}

func (u *TournamentUseCase) isAvailable() bool {
	return u != nil && u.repository != nil && u.clock != nil
}

func validTournamentCreateCommand(command TournamentCreateCommand) bool {
	return command.TournamentID != uuid.Nil && command.RosterID != uuid.Nil
}

func (u *TournamentUseCase) ListTournaments(
	ctx context.Context,
	filter TournamentListFilter,
) ([]CatalogTournamentRecord, error) {
	if u == nil || u.repository == nil {
		return nil, domain.ErrValidation
	}
	states := make(map[domain.TournamentState]struct{}, len(filter.States))
	for _, state := range filter.States {
		if !state.IsValid() {
			return nil, domain.ErrValidation
		}
		states[state] = struct{}{}
	}

	records, err := u.repository.ListTournaments(ctx)
	if err != nil {
		return nil, fmt.Errorf("TournamentUseCase - ListTournaments - TournamentRepository.List: %w", err)
	}
	filtered := make([]CatalogTournamentRecord, 0, len(records))
	for _, record := range records {
		if err := catalogValidateTournamentRecord(record); err != nil {
			return nil, err
		}
		if len(states) > 0 {
			if _, included := states[record.State]; !included {
				continue
			}
		}
		filtered = append(filtered, *catalogCloneTournamentRecord(record))
	}
	return filtered, nil
}

func reconcileTournamentCreate(
	existing *CatalogTournamentRecord,
	command TournamentCreateCommand,
) (*CatalogTournamentRecord, bool, error) {
	if existing == nil || existing.ID != command.TournamentID || existing.RosterID != command.RosterID ||
		existing.Preset != domain.TournamentPresetV1 {
		return nil, false, domain.ErrConflict
	}
	if err := catalogValidateTournamentRecord(*existing); err != nil {
		return nil, false, err
	}
	return catalogCloneTournamentRecord(*existing), false, nil
}

func catalogValidateTournamentRecord(record CatalogTournamentRecord) error {
	if record.ID == uuid.Nil || record.RosterID == uuid.Nil || record.Preset != domain.TournamentPresetV1 ||
		record.Revision < 1 || record.RosterSize < 0 || record.RosterSize > domain.TournamentMaxParticipants ||
		!catalogValidServerTime(record.CreatedAt) || !catalogValidServerTime(record.UpdatedAt) ||
		record.UpdatedAt.Before(record.CreatedAt) {
		return domain.ErrInternal
	}
	if err := (domain.Tournament{
		State: record.State, PausedFromState: record.PausedFromState,
	}).Validate(); err != nil {
		return domain.ErrInternal
	}
	for _, timestamp := range []*time.Time{record.StartedAt, record.FinishedAt} {
		if timestamp != nil && !catalogValidServerTime(*timestamp) {
			return domain.ErrInternal
		}
	}
	return nil
}

func catalogCloneTournamentRecord(record CatalogTournamentRecord) *CatalogTournamentRecord {
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

func catalogValidServerTime(value time.Time) bool {
	return domain.IsValidServerTime(value)
}
