package postgres

import (
	"context"
	"fmt"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (r *ResultPostgres) Settle(
	ctx context.Context,
	in ResultSettlementInput,
) (*ResultCommitRecord, bool, error) {
	if r == nil || r.tx == nil || !validResultSettlementInput(in) {
		return nil, false, domain.ErrValidation
	}

	var record *ResultCommitRecord
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		if err := lockTournamentResultScope(txCtx, querier, in.Scope.TournamentID, in.Scope.RosterID); err != nil {
			return resultLookupError("Settle - lock result scope", err)
		}
		if _, err := querier.LockProjectionRevisionSet(txCtx, sqlc.LockProjectionRevisionSetParams{
			TournamentID: in.Scope.TournamentID,
			RosterID:     in.Scope.RosterID,
		}); err != nil {
			return resultLookupError("Settle - lock projection revisions", err)
		}
		attempt, err := querier.LockResultAttempt(txCtx, resultAttemptParams(in.Scope))
		if err != nil {
			return resultLookupError("Settle - lock attempt", err)
		}

		if domain.GameState(attempt.State).IsTerminal() {
			current, currentErr := querier.GetCurrentResultCommitForAttempt(
				txCtx,
				sqlc.GetCurrentResultCommitForAttemptParams{
					AttemptID: in.Scope.AttemptID, TournamentID: in.Scope.TournamentID,
					RosterID: in.Scope.RosterID, SeriesID: in.Scope.SeriesID,
				},
			)
			if currentErr != nil {
				return resultLookupError("Settle - load current commit", currentErr)
			}
			record, currentErr = loadResultCommit(txCtx, querier, current)
			return currentErr
		}
		if attempt.Revision != in.ExpectedAttemptRevision || attempt.State != string(in.ExpectedAttemptState) {
			return domain.ErrConflict
		}

		series, err := querier.LockResultSeries(txCtx, sqlc.LockResultSeriesParams{
			SeriesID: in.Scope.SeriesID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		})
		if err != nil {
			return resultLookupError("Settle - lock series", err)
		}
		if err := validateSettlementAgainstRows(in, attempt, series); err != nil {
			return err
		}
		source, err := querier.LockResultSourceProjection(txCtx, sqlc.LockResultSourceProjectionParams{
			TournamentID: in.Scope.TournamentID,
			RosterID:     in.Scope.RosterID,
		})
		if err != nil {
			return resultLookupError("Settle - lock source projection", err)
		}
		resultSequence, err := querier.AllocateResultEventSequence(txCtx, resultEventSequenceParams(in.Scope))
		if err != nil {
			return mapRepositoryWriteError("ResultPostgres - Settle - allocate event sequence", err)
		}

		if err := createResultEvidence(txCtx, querier, in, attempt, series, source, resultSequence); err != nil {
			return err
		}
		if in.ProjectionPublication == resultProjectionPublicationImmediate {
			if err := publishResultProjection(txCtx, r.tx, in, source); err != nil {
				return err
			}
		}
		commit, err := querier.GetResultCommitByID(txCtx, in.IDs.CommitID)
		if err != nil {
			return fmt.Errorf("ResultPostgres - Settle - reload commit: %w", err)
		}
		record, err = loadResultCommit(txCtx, querier, commit)
		if err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return record, changed, nil
}
