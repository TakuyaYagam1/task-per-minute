package postgres

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

func validTerminalRepository(repository *PlayoffTerminalPostgres) bool {
	return repository != nil && repository.tx != nil && repository.drafts != nil && repository.assignments != nil
}

func validTerminalDraftCommand(command playoff.TerminalDraftCommand) bool {
	return command.TournamentID != uuid.Nil && command.SeriesID != uuid.Nil &&
		command.DraftID != uuid.Nil && command.CommandID != uuid.Nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func validFinalDraftPlan(plan playoff.FinalDraftPlan) bool {
	return plan.StageCommandID != uuid.Nil && plan.RosterID != uuid.Nil && plan.IDs.Valid() &&
		plan.Series.ID == plan.IDs.FinalSeriesID && plan.Series.TournamentID != uuid.Nil &&
		plan.Series.Format == domain.SeriesFormatBO3 && plan.Series.State == domain.SeriesStatePlanned &&
		plan.Series.Validate() == nil && plan.Category.Validate() == nil &&
		plan.Category.ID == plan.IDs.CategoryRevisionID && plan.Category.SeriesID == plan.Series.ID &&
		plan.Category.TournamentID == plan.Series.TournamentID && plan.Category.RosterID == plan.RosterID &&
		plan.Draft.Validate() == nil && plan.Draft.ID == plan.IDs.DraftID &&
		plan.Draft.SeriesID == plan.Series.ID && plan.Draft.RevisionID == plan.IDs.DraftInitialRevisionID &&
		plan.Draft.State == draftusecase.ExecutionStateActive && len(plan.Advancement) == 2 &&
		domain.IsValidServerTime(plan.CreatedAt)
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func (repository *PlayoffTerminalPostgres) semifinalStageAuthority(
	ctx context.Context,
	rows []sqlc.LockPostseasonSemifinalAuthorityRow,
) (*playoff.SemifinalStageAuthority, error) {
	if len(rows) != 2 {
		return nil, domain.ErrConflict
	}
	first := rows[0]
	if first.CommandID == uuid.Nil || first.TournamentID == uuid.Nil || first.RosterID == uuid.Nil ||
		first.BracketNodeID == uuid.Nil || first.PublishedProjectionRevisionID == uuid.Nil ||
		first.PublishedProjectionRevision < 1 {
		return nil, domain.ErrConflict
	}
	configuration, _, err := loadTournamentPreflightContent(ctx, repository.tx.Querier(ctx), first.TournamentID)
	if err != nil {
		return nil, err
	}
	semifinals := make([]playoff.SemifinalMatch, 0, len(rows))
	series := make([]domain.Series, 0, len(rows))
	var recordedAt time.Time
	for index, row := range rows {
		if row.CommandID != first.CommandID || row.TournamentID != first.TournamentID ||
			row.RosterID != first.RosterID || row.BracketNodeID != first.BracketNodeID ||
			row.Position != int16(index+1) || row.SeriesID == uuid.Nil ||
			row.FirstParticipantID == uuid.Nil || row.SecondParticipantID == uuid.Nil ||
			row.FirstParticipantID == row.SecondParticipantID || row.Format != string(domain.SeriesFormatBO1) {
			return nil, domain.ErrConflict
		}
		planned := domain.Series{
			ID: row.SeriesID, TournamentID: row.TournamentID,
			FirstParticipantID: row.FirstParticipantID, SecondParticipantID: row.SecondParticipantID,
			Format: domain.SeriesFormatBO1, State: domain.SeriesStateLocked,
		}
		if planned.Validate() != nil {
			return nil, domain.ErrConflict
		}
		observed, err := terminalSemifinalSeries(row)
		if err != nil {
			return nil, err
		}
		completedAt, err := semifinalCompletionTime(row)
		if err != nil {
			return nil, err
		}
		if completedAt.After(recordedAt) {
			recordedAt = completedAt
		}
		semifinals = append(semifinals, playoff.SemifinalMatch{
			Position: index + 1, Series: planned,
			WinnerPath: playoff.SemifinalWinnerToFinal,
			LoserPath:  playoff.SemifinalLoserEliminated,
		})
		series = append(series, observed)
	}
	authority := &playoff.SemifinalStageAuthority{
		StageCommandID: first.CommandID,
		RosterID:       first.RosterID,
		Bracket: playoff.SemifinalAdvancementAuthority{
			TournamentID:      first.TournamentID,
			BracketRevisionID: domain.DerivedRevisionID(first.BracketNodeID),
			Semifinals:        semifinals,
		},
		Series:        series,
		Configuration: configuration,
		RecordedAt:    recordedAt,
	}
	if authority.Bracket.Validate() != nil || authority.Configuration.Validate() != nil {
		return nil, domain.ErrConflict
	}
	return authority, nil
}

func semifinalCompletionTime(row sqlc.LockPostseasonSemifinalAuthorityRow) (time.Time, error) {
	if row.CompletedResultRevisionID == uuid.Nil || row.CompletedResultRevisionID != row.ResultHeadRevisionID ||
		!row.CurrentResultRevisionID.Valid || row.CompletedResultRevisionID != row.CurrentResultRevisionID.UUID {
		return time.Time{}, domain.ErrConflict
	}
	return requiredTerminalTime(row.CompletedAt)
}

func terminalSemifinalSeries(row sqlc.LockPostseasonSemifinalAuthorityRow) (domain.Series, error) {
	if row.State != string(domain.SeriesStateCompleted) || !row.CurrentScoreRevisionID.Valid ||
		!row.CurrentResultRevisionID.Valid || row.ScoreHeadRevisionID == uuid.Nil ||
		row.ResultHeadRevisionID == uuid.Nil || row.ScoreHeadRevision < 1 ||
		row.ResultHeadRevision < 1 ||
		row.CurrentScoreRevisionID.UUID != row.ScoreHeadRevisionID ||
		row.CurrentResultRevisionID.UUID != row.ResultHeadRevisionID {
		return domain.Series{}, domain.ErrConflict
	}
	series := domain.Series{
		ID: row.SeriesID, TournamentID: row.TournamentID,
		FirstParticipantID: row.FirstParticipantID, SecondParticipantID: row.SecondParticipantID,
		Format: domain.SeriesFormat(row.Format), State: domain.SeriesState(row.State),
		Score: domain.SeriesScore{
			FirstParticipantWins:  int(row.FirstParticipantWins),
			SecondParticipantWins: int(row.SecondParticipantWins),
		},
	}
	if row.WinnerID.Valid {
		winnerID := row.WinnerID.UUID
		series.WinnerID = &winnerID
	}
	if row.CurrentScoreRevisionID.Valid {
		revisionID := domain.SeriesScoreRevisionID(row.CurrentScoreRevisionID.UUID)
		series.CurrentScoreRevisionID = &revisionID
	}
	if row.CurrentResultRevisionID.Valid {
		revisionID := domain.OfficialResultRevisionID(row.CurrentResultRevisionID.UUID)
		series.CurrentResultRevisionID = &revisionID
	}
	if series.Validate() != nil {
		return domain.Series{}, domain.ErrConflict
	}
	return series, nil
}

func semifinalResults(
	series []domain.Series,
	authority playoff.SemifinalAdvancementAuthority,
) []playoff.SemifinalAdvancementResult {
	_, _, err := playoff.AdvanceSemifinalEvidence(playoff.SemifinalAdvancement{}, authority, series)
	if err != nil {
		return nil
	}
	result := make([]playoff.SemifinalAdvancementResult, 0, len(series))
	for _, item := range series {
		if item.State != domain.SeriesStateCompleted || item.WinnerID == nil ||
			item.CurrentScoreRevisionID == nil || item.CurrentResultRevisionID == nil {
			continue
		}
		for _, match := range authority.Semifinals {
			if match.Series.ID != item.ID {
				continue
			}
			loserID := item.FirstParticipantID
			if *item.WinnerID == loserID {
				loserID = item.SecondParticipantID
			}
			result = append(result, playoff.SemifinalAdvancementResult{
				Position: match.Position, SeriesID: item.ID, WinnerID: *item.WinnerID,
				LoserID: loserID, ScoreRevisionID: *item.CurrentScoreRevisionID,
				ResultRevisionID: *item.CurrentResultRevisionID,
			})
		}
	}
	return result
}

func sameFinalAdvancements(first, second []playoff.SemifinalAdvancementResult) bool {
	return len(first) == 2 && len(second) == 2 && reflect.DeepEqual(first, second)
}

func (repository *PlayoffTerminalPostgres) lockFinalStage(
	ctx context.Context,
	tournamentID uuid.UUID,
	seriesID uuid.UUID,
) (sqlc.LockPostseasonFinalStageRow, bool, error) {
	row, err := repository.tx.Querier(ctx).LockPostseasonFinalStage(
		ctx,
		sqlc.LockPostseasonFinalStageParams{TournamentID: tournamentID, SeriesID: seriesID},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.LockPostseasonFinalStageRow{}, false, nil
	}
	if err != nil {
		return sqlc.LockPostseasonFinalStageRow{}, false, err
	}
	return row, true, nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func finalDraftStageMatchesPlan(stage sqlc.LockPostseasonFinalStageRow, plan playoff.FinalDraftPlan) bool {
	return stage.CommandID == plan.StageCommandID && stage.TournamentID == plan.Series.TournamentID &&
		stage.RosterID == plan.RosterID && stage.FinalSeriesID == plan.Series.ID &&
		stage.CategoryRevisionID == plan.Category.ID && stage.DraftID == plan.Draft.ID &&
		stage.DraftInitialRevisionID == plan.Draft.RevisionID &&
		stage.FirstParticipantID == plan.Series.FirstParticipantID &&
		stage.SecondParticipantID == plan.Series.SecondParticipantID &&
		stage.SeriesState == string(domain.SeriesStatePlanned) && stage.SeriesRevision == 1 &&
		stage.CurrentScoreRevisionID.Valid && stage.CurrentScoreRevisionID.UUID == plan.IDs.InitialScoreRevisionID.UUID() &&
		!stage.CurrentResultRevisionID.Valid &&
		stage.CurrentDraftRevisionID == plan.Draft.RevisionID && stage.CurrentDraftRevision == plan.Draft.Revision &&
		stage.CurrentDraftState == string(draftusecase.ExecutionStateActive)
}

func (repository *PlayoffTerminalPostgres) createFinalDraft(
	ctx context.Context,
	plan playoff.FinalDraftPlan,
) error {
	if plan.Draft.AbsoluteDeadline == nil {
		return domain.ErrConflict
	}
	poolID, err := repository.tx.Querier(ctx).LockPostseasonFinalNormalPool(ctx, sqlc.LockPostseasonFinalNormalPoolParams{
		TournamentID:    plan.Series.TournamentID,
		ContentRevision: plan.Category.SourceContentRevision,
		CategoryPoolID:  plan.Category.CategoryPool.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	if err != nil {
		return err
	}
	_, err = repository.drafts.Create(ctx, DraftCreateInput{
		ID:                  plan.Draft.ID,
		SeriesID:            plan.Series.ID,
		RosterID:            plan.RosterID,
		CategoryRevisionID:  plan.Category.ID,
		CategoryRevision:    plan.Category.Revision,
		SourcePoolRevision:  poolID,
		FirstParticipantID:  plan.Draft.FirstParticipantID,
		SecondParticipantID: plan.Draft.SecondParticipantID,
		Format:              plan.Draft.Format,
		Pool:                append([]domain.Category(nil), plan.Draft.Pool...),
		InitialRevisionID:   plan.Draft.RevisionID,
		CommandID:           plan.Draft.CommandID,
		ServiceEpoch:        plan.Draft.ServiceEpoch,
		AbsoluteDeadline:    *plan.Draft.AbsoluteDeadline,
		DecisionEvidence:    plan.Draft.FirstActorDecision,
		CreatedAt:           plan.CreatedAt,
	})
	return err
}

func finalStageIDsMatch(stage sqlc.LockPostseasonFinalStageRow, ids playoff.FinalStageIDs) bool {
	return ids.Valid() && stage.FinalSeriesID == ids.FinalSeriesID &&
		stage.CategoryRevisionID == ids.CategoryRevisionID && stage.DraftID == ids.DraftID &&
		stage.DraftInitialRevisionID == ids.DraftInitialRevisionID
}

func (repository *PlayoffTerminalPostgres) finalDraftExecution(
	ctx context.Context,
	stage sqlc.LockPostseasonFinalStageRow,
) (draftusecase.Execution, error) {
	aggregate, err := repository.drafts.Get(ctx, stage.DraftID)
	if err != nil {
		return draftusecase.Execution{}, err
	}
	execution, err := participantDraftExecution(aggregate, stage.CurrentDraftRevision)
	if err != nil || execution == nil || execution.Validate() != nil ||
		execution.ID != stage.DraftID || execution.SeriesID != stage.FinalSeriesID ||
		execution.RevisionID != stage.CurrentDraftRevisionID ||
		execution.State != draftusecase.ExecutionState(stage.CurrentDraftState) {
		return draftusecase.Execution{}, domain.ErrConflict
	}
	return *execution, nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func (repository *PlayoffTerminalPostgres) finalSemifinalAuthority(
	ctx context.Context,
	stage sqlc.LockPostseasonFinalStageRow,
) (playoff.SemifinalAdvancementAuthority, []playoff.SemifinalAdvancementResult, error) {
	rows, err := repository.tx.Querier(ctx).LockPostseasonFinalAdvancements(
		ctx,
		sqlc.LockPostseasonFinalAdvancementsParams{CommandID: stage.CommandID, TournamentID: stage.TournamentID},
	)
	if err != nil || len(rows) != 2 {
		if err != nil {
			return playoff.SemifinalAdvancementAuthority{}, nil, err
		}
		return playoff.SemifinalAdvancementAuthority{}, nil, domain.ErrConflict
	}
	semifinals, err := repository.tx.Querier(ctx).LockPostseasonSemifinalAuthority(
		ctx,
		sqlc.LockPostseasonSemifinalAuthorityParams{
			TournamentID: stage.TournamentID,
			SeriesID:     rows[0].SemifinalSeriesID,
		},
	)
	if err != nil {
		return playoff.SemifinalAdvancementAuthority{}, nil, err
	}
	loaded, err := repository.semifinalStageAuthority(ctx, semifinals)
	if err != nil || loaded == nil || loaded.StageCommandID != stage.CommandID || loaded.RosterID != stage.RosterID ||
		loaded.Bracket.BracketRevisionID != domain.DerivedRevisionID(stage.BracketNodeID) {
		if err != nil {
			return playoff.SemifinalAdvancementAuthority{}, nil, err
		}
		return playoff.SemifinalAdvancementAuthority{}, nil, domain.ErrConflict
	}
	advancement := make([]playoff.SemifinalAdvancementResult, 0, len(rows))
	for index, row := range rows {
		if row.Position != int16(index+1) || row.SemifinalSeriesID != loaded.Bracket.Semifinals[index].Series.ID ||
			row.WinnerID == uuid.Nil || row.LoserID == uuid.Nil || row.WinnerID == row.LoserID ||
			row.ScoreRevisionID == uuid.Nil || row.ResultRevisionID == uuid.Nil {
			return playoff.SemifinalAdvancementAuthority{}, nil, domain.ErrConflict
		}
		advancement = append(advancement, playoff.SemifinalAdvancementResult{
			Position:         int(row.Position),
			SeriesID:         row.SemifinalSeriesID,
			WinnerID:         row.WinnerID,
			LoserID:          row.LoserID,
			ScoreRevisionID:  domain.SeriesScoreRevisionID(row.ScoreRevisionID),
			ResultRevisionID: domain.OfficialResultRevisionID(row.ResultRevisionID),
		})
	}
	if !sameFinalAdvancements(advancement, semifinalResults(loaded.Series, loaded.Bracket)) {
		return playoff.SemifinalAdvancementAuthority{}, nil, domain.ErrConflict
	}
	return loaded.Bracket, advancement, nil
}

func requiredTerminalTime(value pgtype.Timestamptz) (time.Time, error) {
	if !value.Valid {
		return time.Time{}, domain.ErrConflict
	}
	result := value.Time.Round(0).UTC()
	if !domain.IsValidServerTime(result) {
		return time.Time{}, domain.ErrConflict
	}
	return result, nil
}

func terminalRepositoryError(operation string, err error) error {
	if errors.Is(err, domain.ErrValidation) || errors.Is(err, domain.ErrConflict) {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	return mapRepositoryWriteError("PlayoffTerminalPostgres - "+operation, err)
}
