package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func participantReadAssignment(
	ctx context.Context,
	querier *sqlc.Queries,
	query usecase.ParticipantSnapshotQuery,
) (*usecase.ParticipantAssignmentView, error) {
	row, err := querier.GetParticipantReadAssignment(ctx, sqlc.GetParticipantReadAssignmentParams{
		TournamentID: query.TournamentID,
		PlayerID:     query.PlayerID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("TournamentSnapshotPostgres - ParticipantSnapshot - assignment: %w", err)
	}
	waveID, err := requiredTournamentUUID(row.WaveID)
	if err != nil {
		return nil, tournamentSnapshotInvalidError("assignment wave")
	}
	if row.AssignmentID == uuid.Nil || row.AttemptID == uuid.Nil || row.SeriesID == uuid.Nil ||
		row.GameID == uuid.Nil || row.SnapshotID == uuid.Nil || row.TaskID == uuid.Nil ||
		strings.TrimSpace(row.Title) == "" || strings.TrimSpace(row.Category) == "" ||
		strings.TrimSpace(row.Difficulty) == "" || row.TimeLimitSeconds < 1 {
		return nil, tournamentSnapshotInvalidError("assignment")
	}
	return &usecase.ParticipantAssignmentView{
		AssignmentID: row.AssignmentID,
		AttemptID:    row.AttemptID,
		SeriesID:     row.SeriesID,
		GameID:       row.GameID,
		WaveID:       waveID,
		Task: usecase.ParticipantTaskView{
			SnapshotID:       row.SnapshotID,
			TaskID:           row.TaskID,
			Title:            row.Title,
			Category:         row.Category,
			Difficulty:       row.Difficulty,
			TimeLimitSeconds: int(row.TimeLimitSeconds),
		},
	}, nil
}

func participantReadOpponent(
	ctx context.Context,
	querier *sqlc.Queries,
	query usecase.ParticipantSnapshotQuery,
	assignment *usecase.ParticipantAssignmentView,
) (*usecase.ParticipantOpponentView, error) {
	seriesID := uuid.NullUUID{}
	if assignment != nil {
		seriesID = uuid.NullUUID{UUID: assignment.SeriesID, Valid: true}
	}
	row, err := querier.GetParticipantReadOpponent(ctx, sqlc.GetParticipantReadOpponentParams{
		TournamentID: query.TournamentID,
		PlayerID:     query.PlayerID,
		SeriesID:     seriesID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("TournamentSnapshotPostgres - ParticipantSnapshot - opponent: %w", err)
	}
	if row.PlayerID == uuid.Nil || row.PlayerID == query.PlayerID || row.SeriesID == uuid.Nil ||
		strings.TrimSpace(row.DisplayName) == "" || strings.TrimSpace(row.SeriesState) == "" || row.Score < 0 {
		return nil, tournamentSnapshotInvalidError("opponent")
	}
	return &usecase.ParticipantOpponentView{
		PlayerID:    row.PlayerID,
		DisplayName: row.DisplayName,
		SeriesID:    row.SeriesID,
		Ready:       row.Ready,
		SeriesState: row.SeriesState,
		Score:       int(row.Score),
	}, nil
}

func tournamentParticipantNames(rows []sqlc.ListTournamentReadParticipantsRow) map[uuid.UUID]string {
	names := make(map[uuid.UUID]string, len(rows))
	for _, row := range rows {
		names[row.ParticipantID] = row.DisplayName
	}
	return names
}

func tournamentScoreboard(
	payload []byte,
	names map[uuid.UUID]string,
) ([]usecase.PublicScoreboardEntryView, error) {
	var document standingsPayloadDocument
	if err := json.Unmarshal(payload, &document); err != nil || document.Entries == nil {
		return nil, tournamentSnapshotInvalidError("standings payload")
	}
	view := make([]usecase.PublicScoreboardEntryView, len(document.Entries))
	seenParticipants := make(map[uuid.UUID]struct{}, len(document.Entries))
	seenPositions := make(map[int]struct{}, len(document.Entries))
	for index, entry := range document.Entries {
		displayName, ok := names[entry.ParticipantID]
		if !ok || entry.ParticipantID == uuid.Nil || strings.TrimSpace(displayName) == "" ||
			entry.Position < 1 || entry.Points < 0 || entry.Buchholz < 0 || entry.EffectiveTime < 0 {
			return nil, tournamentSnapshotInvalidError("standings entry")
		}
		if _, duplicate := seenParticipants[entry.ParticipantID]; duplicate {
			return nil, tournamentSnapshotInvalidError("duplicate standings participant")
		}
		if _, duplicate := seenPositions[entry.Position]; duplicate {
			return nil, tournamentSnapshotInvalidError("duplicate standings position")
		}
		seenParticipants[entry.ParticipantID] = struct{}{}
		seenPositions[entry.Position] = struct{}{}
		view[index] = usecase.PublicScoreboardEntryView{
			Rank:            entry.Position,
			DisplayName:     displayName,
			Points:          entry.Points,
			Buchholz:        entry.Buchholz,
			EffectiveTimeMS: entry.EffectiveTime / int64(time.Millisecond),
		}
	}
	return view, nil
}

//nolint:gocyclo // Public bracket decoding validates all participant, stage, score, and uniqueness invariants together.
func tournamentBracket(
	payload []byte,
	names map[uuid.UUID]string,
) ([]usecase.PublicBracketMatchView, error) {
	var document bracketPayloadDocument
	if err := json.Unmarshal(payload, &document); err != nil || document.Rounds == nil {
		return nil, tournamentSnapshotInvalidError("bracket payload")
	}
	view := make([]usecase.PublicBracketMatchView, len(document.Rounds))
	seenPositions := make(map[string]struct{}, len(document.Rounds))
	for index, match := range document.Rounds {
		stage := match.Stage
		if stage == "" {
			stage = tournamentBracketStageSemifinal
		}
		firstName, firstOK := names[match.FirstParticipantID]
		secondName, secondOK := names[match.SecondParticipantID]
		if !firstOK || !secondOK || match.FirstParticipantID == match.SecondParticipantID ||
			match.Position < 1 || strings.TrimSpace(firstName) == "" || strings.TrimSpace(secondName) == "" ||
			(stage != tournamentBracketStageSemifinal && stage != tournamentBracketStageFinal) ||
			strings.TrimSpace(match.State) == "" || match.FirstWins < 0 || match.SecondWins < 0 {
			return nil, tournamentSnapshotInvalidError("bracket match")
		}
		positionKey := fmt.Sprintf("%s:%d", stage, match.Position)
		if _, duplicate := seenPositions[positionKey]; duplicate {
			return nil, tournamentSnapshotInvalidError("duplicate bracket position")
		}
		seenPositions[positionKey] = struct{}{}
		view[index] = usecase.PublicBracketMatchView{
			Stage:             stage,
			Position:          match.Position,
			FirstDisplayName:  firstName,
			SecondDisplayName: secondName,
			FirstWins:         match.FirstWins,
			SecondWins:        match.SecondWins,
			State:             match.State,
			ScheduledAt:       nil,
		}
	}
	return view, nil
}

func publicTournamentReadSeries(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
) ([]usecase.PublicSeriesView, error) {
	rows, err := querier.ListPublicTournamentReadSeries(ctx, tournamentID)
	if err != nil {
		return nil, fmt.Errorf("TournamentSnapshotPostgres - PublicSnapshot - series: %w", err)
	}
	view := make([]usecase.PublicSeriesView, len(rows))
	for index, row := range rows {
		stage := domain.TournamentStage(row.Stage)
		if !stage.IsValid() || (stage == domain.TournamentStageSwiss &&
			(row.RoundNumber < 1 || row.RoundNumber > 4)) ||
			(stage != domain.TournamentStageSwiss && row.RoundNumber != 0) {
			return nil, tournamentSnapshotInvalidError("series stage")
		}
		var roundNumber *int
		if row.RoundNumber > 0 {
			value := int(row.RoundNumber)
			roundNumber = &value
		}
		view[index] = usecase.PublicSeriesView{
			SeriesID:            row.SeriesID,
			Stage:               row.Stage,
			RoundNumber:         roundNumber,
			Format:              row.Format,
			State:               row.State,
			FirstDisplayName:    row.FirstDisplayName,
			SecondDisplayName:   row.SecondDisplayName,
			FirstWins:           int(row.FirstParticipantWins),
			SecondWins:          int(row.SecondParticipantWins),
			CurrentGamePosition: int(row.CurrentGamePosition),
			ScheduledAt:         utcNullableTime(row.ScheduledAt),
		}
	}
	return view, nil
}

func publicTournamentReadResults(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
) ([]usecase.PublicOfficialResultView, error) {
	rows, err := querier.ListPublicTournamentReadResults(ctx, tournamentID)
	if err != nil {
		return nil, fmt.Errorf("TournamentSnapshotPostgres - PublicSnapshot - results: %w", err)
	}
	view := make([]usecase.PublicOfficialResultView, len(rows))
	for index, row := range rows {
		if !row.RecordedAt.Valid {
			return nil, tournamentSnapshotInvalidError("official result timestamp")
		}
		view[index] = usecase.PublicOfficialResultView{
			RevisionID:        row.RevisionID,
			SeriesID:          row.SeriesID,
			State:             row.State,
			WinnerDisplayName: row.WinnerDisplayName,
			FirstWins:         int(row.FirstParticipantWins),
			SecondWins:        int(row.SecondParticipantWins),
			RecordedAt:        row.RecordedAt.Time.UTC(),
		}
	}
	return view, nil
}

func publicTournamentReadDraft(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
) (*usecase.PublicDraftView, error) {
	row, err := querier.GetPublicTournamentReadDraft(ctx, tournamentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("TournamentSnapshotPostgres - PublicSnapshot - draft: %w", err)
	}
	pool, err := tournamentStringList(row.CategoryPool, "draft pool")
	if err != nil {
		return nil, err
	}
	selected, err := tournamentStringList(row.SelectedCategories, "draft selections")
	if err != nil {
		return nil, err
	}
	actionRows, err := querier.ListPublicTournamentReadDraftActions(ctx, row.DraftID)
	if err != nil {
		return nil, fmt.Errorf("TournamentSnapshotPostgres - PublicSnapshot - draft actions: %w", err)
	}
	actions := make([]usecase.PublicDraftActionView, len(actionRows))
	for index, action := range actionRows {
		if action.Turn < 1 || strings.TrimSpace(action.Action) == "" ||
			strings.TrimSpace(action.Category) == "" || strings.TrimSpace(action.ActorDisplayName) == "" ||
			!action.OccurredAt.Valid {
			return nil, tournamentSnapshotInvalidError("draft action")
		}
		actions[index] = usecase.PublicDraftActionView{
			Turn:             int(action.Turn),
			Action:           action.Action,
			Category:         action.Category,
			ActorDisplayName: action.ActorDisplayName,
			OccurredAt:       action.OccurredAt.Time.UTC(),
		}
	}
	return &usecase.PublicDraftView{
		SeriesID:           row.SeriesID,
		Format:             row.Format,
		State:              row.State,
		Pool:               pool,
		SelectedCategories: selected,
		Actions:            actions,
	}, nil
}
