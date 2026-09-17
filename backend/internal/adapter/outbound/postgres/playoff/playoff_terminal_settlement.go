package playoff

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (repository *PlayoffTerminalPostgres) LoadFinalSettlement(
	ctx context.Context,
	command playoff.TerminalSeriesCommand,
) (*playoff.FinalSettlementAuthority, error) {
	if !validTerminalRepository(repository) || ctx == nil ||
		command.TournamentID == uuid.Nil || command.SeriesID == uuid.Nil {
		return nil, domain.ErrValidation
	}

	var authority *playoff.FinalSettlementAuthority
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		stage, found, err := repository.lockFinalStage(txCtx, command.TournamentID, command.SeriesID)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		initialization, initialized, err := repository.lockFinalInitialization(txCtx, stage)
		if err != nil {
			return err
		}
		if !initialized || initialization.FinalSeriesID != stage.FinalSeriesID ||
			initialization.DraftID != stage.DraftID || stage.CurrentDraftState != string(draftusecase.ExecutionStateCompleted) {
			return nil
		}
		ids, err := playoff.FinalStageIdentity(stage.CommandID)
		if err != nil || !finalStageIDsMatch(stage, ids) ||
			initialization.InitialScoreRevisionID != ids.InitialScoreRevisionID.UUID() {
			return domain.ErrConflict
		}
		bracket, advancement, err := repository.finalSemifinalAuthority(txCtx, stage)
		if err != nil {
			return err
		}
		draft, err := repository.finalDraftExecution(txCtx, stage)
		if err != nil || draft.State != draftusecase.ExecutionStateCompleted {
			if err != nil {
				return err
			}
			return domain.ErrConflict
		}
		aggregate, err := repository.tx.Querier(txCtx).LockFinalProjectionAggregate(txCtx,
			sqlc.LockFinalProjectionAggregateParams{
				TournamentID: stage.TournamentID,
				RosterID:     stage.RosterID,
				SeriesID:     stage.FinalSeriesID,
			})
		if err != nil {
			return err
		}
		if aggregate.SeriesFormat != string(domain.SeriesFormatBO3) ||
			aggregate.FirstParticipantID != stage.FirstParticipantID ||
			aggregate.SecondParticipantID != stage.SecondParticipantID ||
			aggregate.SeriesRevision != stage.SeriesRevision ||
			aggregate.CurrentScoreRevisionID.Valid != stage.CurrentScoreRevisionID.Valid ||
			aggregate.CurrentScoreRevisionID.Valid && aggregate.CurrentScoreRevisionID.UUID != stage.CurrentScoreRevisionID.UUID {
			return domain.ErrConflict
		}
		scoreHead, err := repository.tx.Querier(txCtx).LockFinalProjectionScoreHead(txCtx,
			sqlc.LockFinalProjectionScoreHeadParams{SeriesID: stage.FinalSeriesID, RosterID: stage.RosterID})
		if err != nil {
			return err
		}
		if !stage.CurrentScoreRevisionID.Valid || !stage.ScoreHeadRevisionID.Valid ||
			stage.ScoreHeadRevision == nil || *stage.ScoreHeadRevision < 1 ||
			!aggregate.CurrentScoreRevisionID.Valid ||
			scoreHead.CurrentRevisionID != stage.CurrentScoreRevisionID.UUID ||
			scoreHead.CurrentRevisionID != stage.ScoreHeadRevisionID.UUID ||
			scoreHead.CurrentRevisionID != aggregate.CurrentScoreRevisionID.UUID ||
			scoreHead.HeadRevision != *stage.ScoreHeadRevision {
			return domain.ErrConflict
		}
		series, scoreRevisions, err := repository.finalSeriesHistory(
			txCtx,
			stage,
			aggregate,
			ids.InitialScoreRevisionID,
		)
		if err != nil {
			return err
		}
		if !aggregate.CurrentScoreRevisionID.Valid || aggregate.CurrentScoreRevisionID.UUID == ids.InitialScoreRevisionID.UUID() {
			return nil
		}
		current, found := scoreRevisions[aggregate.CurrentScoreRevisionID.UUID]
		if !found {
			return domain.ErrConflict
		}
		bindings, err := repository.finalExistingBindings(txCtx, stage, ids, series)
		if err != nil {
			return err
		}
		history, progression, err := repository.finalProgressionAuthority(
			txCtx,
			stage,
			ids,
			draft,
			series,
			scoreRevisions,
			current,
		)
		if err != nil {
			return err
		}
		recordedAt := current.RecordedAt
		settlement := &playoff.FinalSettlementAuthority{
			StageCommandID: stage.CommandID,
			RosterID:       stage.RosterID,
			Bracket:        bracket,
			Advancement:    advancement,
			Draft:          draft,
			IDs:            ids,
			History:        history,
			Progression:    progression,
			Bindings:       bindings,
			RecordedAt:     recordedAt,
		}
		if aggregate.SeriesState == string(domain.SeriesStateCompleted) {
			publication, publicationErr := repository.finalPublication(
				txCtx,
				stage,
				aggregate,
				progression,
				bracket,
				advancement,
			)
			if publicationErr != nil {
				return fmt.Errorf("final publication authority: %w", publicationErr)
			}
			settlement.Publication = publication
		} else if aggregate.SeriesState != string(domain.SeriesStateActive) || progression.Progression.Next == nil {
			return domain.ErrConflict
		}
		authority = settlement
		return nil
	})
	if err != nil {
		return nil, terminalRepositoryError("LoadFinalSettlement", err)
	}
	return authority, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (repository *PlayoffTerminalPostgres) finalSeriesHistory(
	ctx context.Context,
	stage sqlc.LockPostseasonFinalStageRow,
	aggregate sqlc.LockFinalProjectionAggregateRow,
	initialScoreRevisionID domain.SeriesScoreRevisionID,
) (domain.Series, map[uuid.UUID]seriesdomain.ScoreRevision, error) {
	querier := repository.tx.Querier(ctx)
	graph, err := querier.ListRecoverySeriesGraph(ctx, sqlc.ListRecoverySeriesGraphParams{
		SeriesID: stage.FinalSeriesID, RosterID: stage.RosterID,
	})
	if err != nil || len(graph) == 0 {
		if err != nil {
			return domain.Series{}, nil, err
		}
		return domain.Series{}, nil, domain.ErrConflict
	}
	row := sqlc.Series{
		ID:                      stage.FinalSeriesID,
		TournamentID:            stage.TournamentID,
		RosterID:                stage.RosterID,
		FirstParticipantID:      aggregate.FirstParticipantID,
		SecondParticipantID:     aggregate.SecondParticipantID,
		Format:                  aggregate.SeriesFormat,
		State:                   aggregate.SeriesState,
		FirstParticipantWins:    aggregate.FirstParticipantWins,
		SecondParticipantWins:   aggregate.SecondParticipantWins,
		WinnerID:                aggregate.WinnerID,
		CurrentScoreRevisionID:  aggregate.CurrentScoreRevisionID,
		CurrentResultRevisionID: aggregate.CurrentResultRevisionID,
		Revision:                aggregate.SeriesRevision,
	}
	series, err := recoverySeries(row, graph)
	if err != nil {
		return domain.Series{}, nil, err
	}
	historyRows, err := querier.LockPostseasonFinalScoreHistory(ctx,
		sqlc.LockPostseasonFinalScoreHistoryParams{SeriesID: stage.FinalSeriesID, RosterID: stage.RosterID})
	if err != nil || len(historyRows) < 2 {
		if err != nil {
			return domain.Series{}, nil, err
		}
		return domain.Series{}, nil, domain.ErrConflict
	}
	revisions := make(map[uuid.UUID]seriesdomain.ScoreRevision, len(historyRows))
	var previous *domain.SeriesScoreRevisionID
	for index, item := range historyRows {
		if item.TournamentID != stage.TournamentID || item.RosterID != stage.RosterID ||
			item.SeriesID != stage.FinalSeriesID || item.RevisionNumber != int64(index+1) || item.ID == uuid.Nil {
			return domain.Series{}, nil, domain.ErrConflict
		}
		if index == 0 {
			if item.ID != initialScoreRevisionID.UUID() || item.ResultEventID.Valid ||
				item.PreviousRevisionID.Valid || item.Operation != "initialize" ||
				item.FirstParticipantWins != 0 || item.SecondParticipantWins != 0 {
				return domain.Series{}, nil, domain.ErrConflict
			}
			initialID := domain.SeriesScoreRevisionID(item.ID)
			previous = &initialID
			continue
		}
		if !item.PreviousRevisionID.Valid || previous == nil || item.PreviousRevisionID.UUID != previous.UUID() ||
			!item.ResultEventID.Valid || item.Operation != "append_attempt" {
			return domain.Series{}, nil, domain.ErrConflict
		}
		attempts, loadErr := querier.LockSeriesScoreRevisionAttempts(ctx, sqlc.LockSeriesScoreRevisionAttemptsParams{
			ScoreRevisionID: item.ID, TournamentID: stage.TournamentID,
			RosterID: stage.RosterID, SeriesID: stage.FinalSeriesID,
		})
		if loadErr != nil || len(attempts) != index {
			if loadErr != nil {
				return domain.Series{}, nil, loadErr
			}
			return domain.Series{}, nil, domain.ErrConflict
		}
		resultIDs := make([]domain.OfficialResultRevisionID, len(attempts))
		for attemptIndex, attempt := range attempts {
			if attempt.Position != int16(attemptIndex+1) || attempt.GameResultRevisionID == uuid.Nil ||
				attempt.GameAttemptID == uuid.Nil || attempt.ResultEventID == uuid.Nil {
				return domain.Series{}, nil, domain.ErrConflict
			}
			resultIDs[attemptIndex] = domain.OfficialResultRevisionID(attempt.GameResultRevisionID)
		}
		recordedAt, timeErr := requiredTerminalTime(item.CreatedAt)
		if timeErr != nil {
			return domain.Series{}, nil, timeErr
		}
		revision := seriesdomain.ScoreRevision{
			ID:                    domain.SeriesScoreRevisionID(item.ID),
			SeriesID:              stage.FinalSeriesID,
			FirstParticipantID:    stage.FirstParticipantID,
			SecondParticipantID:   stage.SecondParticipantID,
			PreviousRevisionID:    previous,
			Ordinal:               index + 1,
			Format:                domain.SeriesFormatBO3,
			ScoreBefore:           domain.SeriesScore{FirstParticipantWins: int(historyRows[index-1].FirstParticipantWins), SecondParticipantWins: int(historyRows[index-1].SecondParticipantWins)},
			ScoreAfter:            domain.SeriesScore{FirstParticipantWins: int(item.FirstParticipantWins), SecondParticipantWins: int(item.SecondParticipantWins)},
			GameResultRevisionIDs: resultIDs,
			RecordedAt:            recordedAt,
		}
		revisions[item.ID] = revision
		currentID := domain.SeriesScoreRevisionID(item.ID)
		previous = &currentID
	}
	if !aggregate.CurrentScoreRevisionID.Valid || previous == nil || previous.UUID() != aggregate.CurrentScoreRevisionID.UUID {
		return domain.Series{}, nil, domain.ErrConflict
	}
	return series, revisions, nil
}

// finalExistingBindings reads only materialized final assignments. The active
// committed exact-draft branch remains the source of all three canonical
// bindings, so Game 2 and Game 3 may legitimately be absent until their
// preceding settlement creates them.
//
//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (repository *PlayoffTerminalPostgres) finalExistingBindings(
	ctx context.Context,
	stage sqlc.LockPostseasonFinalStageRow,
	ids playoff.FinalStageIDs,
	series domain.Series,
) ([]playoff.FinalGameBinding, error) {
	querier := repository.tx.Querier(ctx)
	bindings := make([]playoff.FinalGameBinding, 0, 3)
	for position, gameID := range []uuid.UUID{ids.FirstGameID, ids.SecondGameID, ids.ThirdGameID} {
		assignmentID := ids.GameAssignmentID(position + 1)
		assignment, err := querier.LockAssignment(ctx, assignmentID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if assignment.ID != assignmentID || assignment.AttemptID != gameID || assignment.SeriesID != stage.FinalSeriesID ||
			assignment.RosterID != stage.RosterID || assignment.PlanID != ids.DraftAssignmentPlanID ||
			assignment.State != "active" || assignment.Revision < 1 {
			return nil, domain.ErrConflict
		}
		snapshot, err := querier.GetAssignmentTaskSnapshot(ctx, assignment.SnapshotID)
		if err != nil || snapshot.ReservationID != assignment.ReservationID || snapshot.TimeLimit < 1 ||
			len(snapshot.ContentDigest) != 32 {
			if err != nil {
				return nil, err
			}
			return nil, domain.ErrConflict
		}
		var digest [32]byte
		copy(digest[:], snapshot.ContentDigest)
		bindings = append(bindings, playoff.FinalGameBinding{
			GameID: gameID, AssignmentID: assignment.ID, AssignmentRevision: assignment.Revision,
			PlanID: assignment.PlanID, PlanRevisionID: ids.DraftAssignmentRevisionID,
			BranchID: assignment.BranchID, ReservationID: assignment.ReservationID,
			SnapshotID: assignment.SnapshotID, ContentDigest: digest,
			DeadlineSeconds: int(snapshot.TimeLimit),
		})
	}
	if err := validateFinalMaterializedBindingPrefix(ids, series, bindings); err != nil {
		return nil, err
	}
	return bindings, nil
}

func validateFinalMaterializedBindingPrefix(
	ids playoff.FinalStageIDs,
	series domain.Series,
	bindings []playoff.FinalGameBinding,
) error {
	if len(series.Slots) < 1 || len(series.Slots) > 3 || len(bindings) != len(series.Slots) {
		return domain.ErrConflict
	}
	slotIDs := [...]uuid.UUID{ids.FirstSlotID, ids.SecondSlotID, ids.ThirdSlotID}
	gameIDs := [...]uuid.UUID{ids.FirstGameID, ids.SecondGameID, ids.ThirdGameID}
	for index, slot := range series.Slots {
		position := index + 1
		expectedSlotID := slotIDs[index]
		expectedGameID := gameIDs[index]
		if slot.ID != expectedSlotID || slot.Position != position || len(slot.Attempts) != 1 ||
			slot.Attempts[0].ID != expectedGameID {
			return domain.ErrConflict
		}
		binding := bindings[index]
		if !validFinalGameBinding(binding) || binding.GameID != expectedGameID ||
			binding.AssignmentID != ids.GameAssignmentID(position) ||
			binding.PlanID != ids.DraftAssignmentPlanID ||
			binding.PlanRevisionID != ids.DraftAssignmentRevisionID {
			return domain.ErrConflict
		}
	}
	return nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func (repository *PlayoffTerminalPostgres) finalProgressionAuthority(
	ctx context.Context,
	stage sqlc.LockPostseasonFinalStageRow,
	ids playoff.FinalStageIDs,
	draft draftusecase.Execution,
	series domain.Series,
	revisions map[uuid.UUID]seriesdomain.ScoreRevision,
	current seriesdomain.ScoreRevision,
) ([]playoff.FinalProgressionCommand, playoff.FinalProgressionCommand, error) {
	querier := repository.tx.Querier(ctx)
	progressions, err := querier.LockPostseasonFinalProgressions(ctx, sqlc.LockPostseasonFinalProgressionsParams{
		CommandID: stage.CommandID, TournamentID: stage.TournamentID,
	})
	if err != nil {
		return nil, playoff.FinalProgressionCommand{}, err
	}
	byGame := finalGamesByResult(series)
	categories, err := draft.DomainDraft()
	if err != nil {
		return nil, playoff.FinalProgressionCommand{}, domain.ErrConflict
	}
	locked, err := draftusecase.BO3FinalGameCategories(categories)
	if err != nil {
		return nil, playoff.FinalProgressionCommand{}, domain.ErrConflict
	}
	history := make([]playoff.FinalProgressionCommand, 0, len(progressions))
	for index, item := range progressions {
		if item.CommandID != stage.CommandID || item.TournamentID != stage.TournamentID ||
			item.RosterID != stage.RosterID || item.FinalSeriesID != stage.FinalSeriesID ||
			item.NextPosition != int16(index+2) || item.NextPosition < 2 || item.NextPosition > 3 {
			return nil, playoff.FinalProgressionCommand{}, domain.ErrConflict
		}
		revision, found := revisions[item.SourceScoreRevisionID]
		game, gameFound := byGame[item.SourceGameResultRevisionID]
		if !found || !gameFound || revision.GameResultRevisionIDs[len(revision.GameResultRevisionIDs)-1].UUID() != item.SourceGameResultRevisionID {
			return nil, playoff.FinalProgressionCommand{}, domain.ErrConflict
		}
		category := locked[item.NextPosition-1]
		next := seriesdomain.NextGameWave{
			WaveID: item.NextWaveID, WaveRevisionID: domain.WaveRevisionID(item.NextWaveRevisionID),
			SlotID: item.NextSlotID, GameID: item.NextGameID, Category: category,
		}
		if revision.ID == current.ID {
			if revision.Ordinal != int(item.NextPosition) || game.ResultRevisionID == nil ||
				game.ResultRevisionID.UUID() != item.SourceGameResultRevisionID {
				return nil, playoff.FinalProgressionCommand{}, domain.ErrConflict
			}
			continue
		}
		if revision.Ordinal >= current.Ordinal {
			return nil, playoff.FinalProgressionCommand{}, domain.ErrConflict
		}
		history = append(history, playoff.FinalProgressionCommand{Progression: seriesdomain.ScoreProgressionCommand{
			Game: game, ScoreRevision: revision, Next: &next,
		}})
	}
	currentResult := current.GameResultRevisionIDs[len(current.GameResultRevisionIDs)-1]
	currentGame, found := byGame[currentResult.UUID()]
	if !found || currentGame.State != domain.GameStateCompleted || currentGame.ResultRevisionID == nil {
		return nil, playoff.FinalProgressionCommand{}, domain.ErrConflict
	}
	winner := current.ScoreAfter.Winner(series.FirstParticipantID, series.SecondParticipantID, domain.SeriesFormatBO3)
	if winner != nil {
		if !stage.CurrentResultRevisionID.Valid {
			return nil, playoff.FinalProgressionCommand{}, domain.ErrConflict
		}
		publicationIDs, identityErr := playoff.FinalPublicationIdentity(currentResult)
		if identityErr != nil {
			return nil, playoff.FinalProgressionCommand{}, identityErr
		}
		terminal := domain.OfficialResultRevisionID(stage.CurrentResultRevisionID.UUID)
		return history, playoff.FinalProgressionCommand{Progression: seriesdomain.ScoreProgressionCommand{
			Game: currentGame, ScoreRevision: current, TerminalResultRevisionID: &terminal,
		}, ChampionRevisionID: publicationIDs.ChampionRevisionID, RecordedAt: current.RecordedAt}, nil
	}
	if len(series.Slots) >= 3 {
		return nil, playoff.FinalProgressionCommand{}, domain.ErrConflict
	}
	position := len(series.Slots) + 1
	next, err := finalNextGame(ids, position, locked[position-1])
	if err != nil {
		return nil, playoff.FinalProgressionCommand{}, err
	}
	return history, playoff.FinalProgressionCommand{Progression: seriesdomain.ScoreProgressionCommand{
		Game: currentGame, ScoreRevision: current, Next: &next,
	}}, nil
}

func finalGamesByResult(series domain.Series) map[uuid.UUID]domain.Game {
	result := make(map[uuid.UUID]domain.Game, len(series.Slots))
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			if game.ResultRevisionID != nil {
				result[game.ResultRevisionID.UUID()] = game
			}
		}
	}
	return result
}

func finalNextGame(
	ids playoff.FinalStageIDs,
	position int,
	category domain.Category,
) (seriesdomain.NextGameWave, error) {
	if !category.IsValid() {
		return seriesdomain.NextGameWave{}, domain.ErrConflict
	}
	switch position {
	case 2:
		return seriesdomain.NextGameWave{WaveID: ids.SecondWaveID, WaveRevisionID: ids.SecondWaveRevisionID,
			SlotID: ids.SecondSlotID, GameID: ids.SecondGameID, Category: category}, nil
	case 3:
		return seriesdomain.NextGameWave{WaveID: ids.ThirdWaveID, WaveRevisionID: ids.ThirdWaveRevisionID,
			SlotID: ids.ThirdSlotID, GameID: ids.ThirdGameID, Category: category}, nil
	default:
		return seriesdomain.NextGameWave{}, domain.ErrConflict
	}
}
