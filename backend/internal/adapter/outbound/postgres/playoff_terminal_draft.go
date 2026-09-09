package postgres

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (repository *PlayoffTerminalPostgres) PersistFinalDraft(
	ctx context.Context,
	plan playoff.FinalDraftPlan,
) (bool, error) {
	if !validTerminalRepository(repository) || ctx == nil || !validFinalDraftPlan(plan) {
		return false, domain.ErrValidation
	}

	changed := false
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		querier := repository.tx.Querier(txCtx)
		semifinals, err := querier.LockPostseasonSemifinalAuthority(
			txCtx,
			sqlc.LockPostseasonSemifinalAuthorityParams{
				TournamentID: plan.Series.TournamentID,
				SeriesID:     plan.Advancement[0].SeriesID,
			},
		)
		if err != nil {
			return err
		}
		stage, err := repository.semifinalStageAuthority(txCtx, semifinals)
		if err != nil {
			return err
		}
		if stage == nil {
			return domain.ErrConflict
		}
		if stage.StageCommandID != plan.StageCommandID || stage.RosterID != plan.RosterID ||
			!sameFinalAdvancements(plan.Advancement, semifinalResults(stage.Series, stage.Bracket)) {
			return domain.ErrConflict
		}

		existing, found, err := repository.lockFinalStage(txCtx, plan.Series.TournamentID, plan.Series.ID)
		if err != nil {
			return err
		}
		if found {
			if !finalDraftStageMatchesPlan(existing, plan) {
				return domain.ErrConflict
			}
			return nil
		}
		if _, err = querier.CreateSeries(txCtx, sqlc.CreateSeriesParams{
			ID:                  plan.Series.ID,
			TournamentID:        plan.Series.TournamentID,
			RosterID:            plan.RosterID,
			FirstParticipantID:  plan.Series.FirstParticipantID,
			SecondParticipantID: plan.Series.SecondParticipantID,
			Format:              string(plan.Series.Format),
			CreatedAt:           tstz(plan.CreatedAt),
		}); err != nil {
			return err
		}
		if err = repository.createFinalDraft(txCtx, plan); err != nil {
			return err
		}
		if err = querier.CreateInitialSeriesScoreRevision(txCtx, sqlc.CreateInitialSeriesScoreRevisionParams{
			ID: plan.IDs.InitialScoreRevisionID.UUID(), TournamentID: plan.Series.TournamentID,
			RosterID: plan.RosterID, SeriesID: plan.Series.ID, CommandID: plan.StageCommandID,
			SourceProjectionRevisionID: semifinals[0].PublishedProjectionRevisionID,
			SourceProjectionRevision:   semifinals[0].PublishedProjectionRevision, CreatedAt: tstz(plan.CreatedAt),
		}); err != nil {
			return err
		}
		if _, err = querier.CreateInitialSeriesScoreHead(txCtx, sqlc.CreateInitialSeriesScoreHeadParams{
			SeriesID: plan.Series.ID, RosterID: plan.RosterID, InitialScoreRevisionID: plan.IDs.InitialScoreRevisionID.UUID(), UpdatedAt: tstz(plan.CreatedAt),
		}); err != nil {
			return err
		}
		for _, advancement := range plan.Advancement {
			if err = querier.CreatePostseasonFinalAdvancement(
				txCtx,
				sqlc.CreatePostseasonFinalAdvancementParams{
					CommandID:    plan.StageCommandID,
					TournamentID: plan.Series.TournamentID,
					RosterID:     plan.RosterID,
					//nolint:gosec // Domain validation bounds this value before the storage conversion.
					Position:          int16(advancement.Position),
					SemifinalSeriesID: advancement.SeriesID,
					WinnerID:          advancement.WinnerID,
					LoserID:           advancement.LoserID,
					ScoreRevisionID:   advancement.ScoreRevisionID.UUID(),
					ResultRevisionID:  advancement.ResultRevisionID.UUID(),
					CreatedAt:         tstz(plan.CreatedAt),
				},
			); err != nil {
				return err
			}
		}
		if err = querier.CreatePostseasonFinalStage(txCtx, sqlc.CreatePostseasonFinalStageParams{
			CommandID:              plan.StageCommandID,
			TournamentID:           plan.Series.TournamentID,
			RosterID:               plan.RosterID,
			FinalSeriesID:          plan.Series.ID,
			CategoryRevisionID:     plan.Category.ID,
			DraftID:                plan.Draft.ID,
			DraftInitialRevisionID: plan.Draft.RevisionID,
			FirstParticipantID:     plan.Series.FirstParticipantID,
			SecondParticipantID:    plan.Series.SecondParticipantID,
			CreatedAt:              tstz(plan.CreatedAt),
		}); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return false, terminalRepositoryError("PersistFinalDraft", err)
	}
	return changed, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (repository *PlayoffTerminalPostgres) LoadFinalDraft(
	ctx context.Context,
	command playoff.TerminalDraftCommand,
) (*playoff.FinalDraftAuthority, error) {
	if !validTerminalRepository(repository) || ctx == nil || !validTerminalDraftCommand(command) {
		return nil, domain.ErrValidation
	}

	var authority *playoff.FinalDraftAuthority
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		stage, found, err := repository.lockFinalStage(txCtx, command.TournamentID, command.SeriesID)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		if stage.DraftID != command.DraftID || stage.CurrentDraftState != string(draftusecase.ExecutionStateCompleted) ||
			stage.CurrentDraftRevisionID == uuid.Nil || stage.CurrentDraftRevision < 1 ||
			stage.SeriesState != string(domain.SeriesStatePlanned) {
			return nil
		}
		ids, err := playoff.FinalStageIdentity(stage.CommandID)
		if err != nil || !finalStageIDsMatch(stage, ids) {
			return domain.ErrConflict
		}
		if !stage.CurrentScoreRevisionID.Valid || stage.CurrentScoreRevisionID.UUID != ids.InitialScoreRevisionID.UUID() || stage.CurrentResultRevisionID.Valid {
			return domain.ErrConflict
		}
		if _, err := repository.tx.Querier(txCtx).LockPostseasonFinalGenesis(txCtx, sqlc.LockPostseasonFinalGenesisParams{
			SeriesID: stage.FinalSeriesID, TournamentID: stage.TournamentID, InitialScoreRevisionID: ids.InitialScoreRevisionID.UUID(),
		}); err != nil {
			return err
		}
		draft, err := repository.finalDraftExecution(txCtx, stage)
		if err != nil {
			return err
		}
		bracket, advancement, err := repository.finalSemifinalAuthority(txCtx, stage)
		if err != nil {
			return err
		}
		recordedAt, err := requiredTerminalTime(stage.CurrentDraftCreatedAt)
		if err != nil {
			return err
		}
		authority = &playoff.FinalDraftAuthority{
			StageCommandID:         stage.CommandID,
			RosterID:               stage.RosterID,
			Bracket:                bracket,
			Advancement:            advancement,
			ExpectedSeriesRevision: stage.SeriesRevision,
			Draft:                  draft,
			IDs:                    ids,
			RecordedAt:             recordedAt,
		}
		return nil
	})
	if err != nil {
		return nil, terminalRepositoryError("LoadFinalDraft", err)
	}
	return authority, nil
}
