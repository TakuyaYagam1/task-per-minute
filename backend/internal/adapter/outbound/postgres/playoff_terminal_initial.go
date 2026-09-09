package postgres

import (
	"context"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (repository *PlayoffTerminalPostgres) PersistFinalInitial(
	ctx context.Context,
	plan playoff.FinalInitialPlan,
) (bool, error) {
	if !validTerminalRepository(repository) || ctx == nil || !validFinalInitialPlan(plan) {
		return false, domain.ErrValidation
	}

	changed := false
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		stage, found, err := repository.lockFinalStage(
			txCtx,
			plan.Execution.Series.TournamentID,
			plan.Execution.Series.ID,
		)
		if err != nil {
			return err
		}
		if !found || stage.CommandID != plan.StageCommandID || stage.RosterID != plan.RosterID {
			return domain.ErrConflict
		}
		querier := repository.tx.Querier(txCtx)
		initialization, initFound, err := repository.lockFinalInitialization(txCtx, stage)
		if err != nil {
			return err
		}
		if initFound {
			if !finalInitializationMatchesPlan(initialization, plan, stage) {
				return domain.ErrConflict
			}
			return nil
		}
		if stage.SeriesState != string(domain.SeriesStatePlanned) ||
			stage.SeriesRevision != plan.ExpectedSeriesRevision || !stage.CurrentScoreRevisionID.Valid ||
			stage.CurrentScoreRevisionID.UUID != plan.InitialScoreRevisionID.UUID() ||
			stage.CurrentResultRevisionID.Valid || stage.CurrentDraftState != string(draftusecase.ExecutionStateCompleted) {
			return domain.ErrConflict
		}
		if err = repository.validateFinalInitialDraft(txCtx, stage, plan); err != nil {
			return err
		}
		series := plan.Execution.Series
		if _, err = querier.LockPostseasonFinalGenesis(txCtx, sqlc.LockPostseasonFinalGenesisParams{
			SeriesID: series.ID, TournamentID: series.TournamentID, InitialScoreRevisionID: plan.InitialScoreRevisionID.UUID(),
		}); err != nil {
			return err
		}
		if err = repository.createFinalGameGraph(
			txCtx,
			querier,
			stage,
			series,
			series.Slots[0],
			plan.CurrentWave,
			plan.Binding,
			plan.ActivatedAt,
			true,
		); err != nil {
			return err
		}
		revision, err := querier.ActivatePostseasonFinalSeriesCAS(txCtx, sqlc.ActivatePostseasonFinalSeriesCASParams{
			InitialScoreRevisionID: nullableUUIDValue(plan.InitialScoreRevisionID.UUID()),
			ActivatedAt:            tstz(plan.ActivatedAt),
			SeriesID:               series.ID,
			TournamentID:           series.TournamentID,
			RosterID:               plan.RosterID,
			ExpectedSeriesRevision: plan.ExpectedSeriesRevision,
		})
		if err != nil || revision != plan.ExpectedSeriesRevision+1 {
			if err != nil {
				return err
			}
			return domain.ErrConflict
		}
		if err = querier.CreatePostseasonFinalInitialization(txCtx, sqlc.CreatePostseasonFinalInitializationParams{
			CommandID:                stage.CommandID,
			TournamentID:             stage.TournamentID,
			RosterID:                 stage.RosterID,
			FinalSeriesID:            stage.FinalSeriesID,
			DraftID:                  stage.DraftID,
			CompletedDraftRevisionID: stage.CurrentDraftRevisionID,
			InitialScoreRevisionID:   plan.InitialScoreRevisionID.UUID(),
			FirstSlotID:              series.Slots[0].ID,
			FirstGameID:              series.Slots[0].Attempts[0].ID,
			FirstWaveID:              plan.CurrentWave.ID,
			FirstWaveRevisionID:      plan.CurrentWave.RevisionID.UUID(),
			FirstAssignmentID:        plan.Binding.AssignmentID,
			CreatedAt:                tstz(plan.ActivatedAt),
		}); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return false, terminalRepositoryError("PersistFinalInitial", err)
	}
	return changed, nil
}
