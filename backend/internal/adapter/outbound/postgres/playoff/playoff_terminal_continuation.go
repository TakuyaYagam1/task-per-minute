package playoff

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (repository *PlayoffTerminalPostgres) PersistFinalContinuation(
	ctx context.Context,
	plan playoff.FinalContinuationPlan,
) (bool, error) {
	if !validTerminalRepository(repository) || ctx == nil || !validFinalContinuationPlan(plan) {
		return false, domain.ErrValidation
	}

	changed := false
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		stage, found, err := repository.lockFinalStage(txCtx, plan.Wave.TournamentID, plan.SeriesID)
		if err != nil || !found {
			if err != nil {
				return err
			}
			return domain.ErrConflict
		}
		if stage.CommandID != plan.StageCommandID || stage.RosterID != plan.RosterID ||
			stage.TournamentID == uuid.Nil || stage.FinalSeriesID != plan.SeriesID ||
			stage.SeriesState != string(domain.SeriesStateActive) || !stage.ScoreHeadRevisionID.Valid ||
			stage.ScoreHeadRevisionID.UUID != plan.SourceScoreRevision.UUID() {
			return domain.ErrConflict
		}
		querier := repository.tx.Querier(txCtx)
		progressions, err := querier.LockPostseasonFinalProgressions(txCtx, sqlc.LockPostseasonFinalProgressionsParams{
			CommandID: stage.CommandID, TournamentID: stage.TournamentID,
		})
		if err != nil {
			return err
		}
		for _, progression := range progressions {
			if progression.SourceScoreRevisionID != plan.SourceScoreRevision.UUID() {
				continue
			}
			if !finalProgressionMatchesPlan(progression, plan, stage) {
				return domain.ErrConflict
			}
			return nil
		}
		head, err := querier.LockFinalProjectionScoreHead(txCtx, sqlc.LockFinalProjectionScoreHeadParams{
			SeriesID: plan.SeriesID, RosterID: plan.RosterID,
		})
		if err != nil || head.CurrentRevisionID != plan.SourceScoreRevision.UUID() ||
			stage.ScoreHeadRevision == nil || head.HeadRevision != *stage.ScoreHeadRevision || !head.ResultEventID.Valid {
			if err != nil {
				return err
			}
			return domain.ErrConflict
		}
		if err = repository.validateContinuationSource(txCtx, stage, plan); err != nil {
			return err
		}
		series := domain.Series{
			ID: stage.FinalSeriesID, TournamentID: stage.TournamentID,
			FirstParticipantID: stage.FirstParticipantID, SecondParticipantID: stage.SecondParticipantID,
			Format: domain.SeriesFormatBO3, State: domain.SeriesStateActive,
		}
		if err = repository.createFinalGameGraph(
			txCtx,
			querier,
			stage,
			series,
			plan.Slot,
			plan.Wave,
			plan.Binding,
			plan.CreatedAt,
			false,
		); err != nil {
			return err
		}
		if err = querier.CreatePostseasonFinalProgression(txCtx, sqlc.CreatePostseasonFinalProgressionParams{
			CommandID:                  stage.CommandID,
			TournamentID:               stage.TournamentID,
			RosterID:                   stage.RosterID,
			FinalSeriesID:              stage.FinalSeriesID,
			SourceScoreRevisionID:      plan.SourceScoreRevision.UUID(),
			SourceGameResultRevisionID: plan.SourceResultRevision.UUID(),
			//nolint:gosec // Domain validation bounds this value before the storage conversion.
			NextPosition:       int16(plan.Slot.Position),
			NextSlotID:         plan.Slot.ID,
			NextGameID:         plan.Next.GameID,
			NextWaveID:         plan.Wave.ID,
			NextWaveRevisionID: plan.Wave.RevisionID.UUID(),
			NextAssignmentID:   plan.Binding.AssignmentID,
			CreatedAt:          tstz(plan.CreatedAt),
		}); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return false, terminalRepositoryError("PersistFinalContinuation", err)
	}
	return changed, nil
}
