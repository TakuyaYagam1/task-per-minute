package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

func (r *TournamentProgressionPostgres) transitionStage(
	ctx context.Context,
	command tournamentprogression.TransitionCommand,
) (usecase.TournamentView, bool, error) {
	var result usecase.TournamentView
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		current, err := querier.GetTournamentSummary(txCtx, command.TournamentID)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrTournamentNotFound
		}
		if err != nil {
			return fmt.Errorf("TournamentProgressionPostgres - transition stage - load tournament: %w", err)
		}
		currentTournament := domain.Tournament{State: domain.TournamentState(current.State)}
		if current.Revision != command.ExpectedRevision || !currentTournament.State.IsValid() ||
			!currentTournament.CanTransitionTo(command.NextState) {
			return domain.ErrConflict
		}

		updatedAt := time.Now().UTC()
		if !domain.IsValidServerTime(updatedAt) || updatedAt.Before(current.UpdatedAt.Time.UTC()) {
			return domain.ErrConflict
		}
		_, err = querier.UpdateTournamentCAS(txCtx, sqlc.UpdateTournamentCASParams{
			ID:               command.TournamentID,
			ExpectedRevision: command.ExpectedRevision,
			ExpectedState:    current.State,
			NextState:        string(command.NextState),
			UpdatedAt:        tstz(updatedAt),
			StartedAt:        current.StartedAt,
			FinishedAt:       current.FinishedAt,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrConflict
		}
		if err != nil {
			return fmt.Errorf("TournamentProgressionPostgres - transition stage - CAS: %w", err)
		}
		updated, err := querier.GetTournamentSummary(txCtx, command.TournamentID)
		if err != nil {
			return fmt.Errorf("TournamentProgressionPostgres - transition stage - load result: %w", err)
		}
		mapped, err := tournamentProgressionTournamentView(updated)
		if err != nil {
			return err
		}
		if mapped.Revision != command.ExpectedRevision+1 || mapped.State != command.NextState {
			return domain.ErrConflict
		}
		result = mapped
		changed = true
		return nil
	})
	if err != nil {
		return usecase.TournamentView{}, false, err
	}
	return result, changed, nil
}

func tournamentProgressionTournamentView(
	row sqlc.GetTournamentSummaryRow,
) (usecase.TournamentView, error) {
	record, err := tournamentSummaryRecord(row)
	if err != nil {
		return usecase.TournamentView{}, err
	}
	view := usecase.TournamentView{
		ID: record.ID, RosterID: record.RosterID, Preset: record.Preset,
		Name: record.Name, PublicID: record.PublicID, PlannedRosterSize: record.PlannedRosterSize,
		ContentRevision: record.ContentRevision, State: record.State,
		Revision: record.Revision, RosterSize: record.RosterSize, CreatedAt: record.CreatedAt,
		UpdatedAt: record.UpdatedAt, StartedAt: record.StartedAt, FinishedAt: record.FinishedAt,
	}
	if record.PausedFromState != nil {
		state := *record.PausedFromState
		view.PausedFromState = &state
	}
	if !validLifecycleTournamentView(view, view.ID) {
		return usecase.TournamentView{}, domain.ErrConflict
	}
	return view, nil
}
