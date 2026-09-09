package postgres

import (
	"context"
	"fmt"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func (r *ResultPostgres) Current(
	ctx context.Context,
	scope ResultScope,
) (*ResultCommitRecord, error) {
	if r == nil || r.tx == nil || !validResultScope(scope) {
		return nil, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	commit, err := querier.GetCurrentResultCommitForAttempt(
		ctx,
		sqlc.GetCurrentResultCommitForAttemptParams{
			AttemptID: scope.AttemptID, TournamentID: scope.TournamentID,
			RosterID: scope.RosterID, SeriesID: scope.SeriesID,
		},
	)
	if err != nil {
		return nil, resultLookupError("Current", err)
	}
	return loadResultCommit(ctx, querier, commit)
}

func (r *ResultPostgres) History(
	ctx context.Context,
	scope ResultScope,
) ([]ResultHistoryRecord, error) {
	if r == nil || r.tx == nil || !validResultScope(scope) {
		return nil, domain.ErrValidation
	}
	rows, err := r.tx.Querier(ctx).ListResultHistory(ctx, sqlc.ListResultHistoryParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		SeriesID: scope.SeriesID, AttemptID: scope.AttemptID,
	})
	if err != nil {
		return nil, fmt.Errorf("ResultPostgres - History: %w", err)
	}
	history := make([]ResultHistoryRecord, 0, len(rows))
	for _, row := range rows {
		history = append(history, resultHistoryRecord(row))
	}
	return history, nil
}
