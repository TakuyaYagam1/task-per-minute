package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

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

// tournamentScoreboard converts private projection evidence into the complete
// public roster. Participant IDs are used only while joining persisted rows;
// the returned view contains display names and server-owned status fields.
func tournamentScoreboard(
	standingsPayload []byte,
	topFourPayload []byte,
	participants []sqlc.ListTournamentReadParticipantsRow,
) ([]usecase.PublicScoreboardEntryView, error) {
	names, orderedIDs, err := tournamentScoreboardRoster(participants)
	if err != nil {
		return nil, err
	}
	document, entries, err := tournamentStandingsPayload(standingsPayload, names)
	if err != nil {
		return nil, err
	}
	if len(entries) != 0 && len(entries) != len(participants) {
		return nil, tournamentSnapshotInvalidError("standings roster coverage")
	}

	// An initial published projection has no standings entries. Return a
	// deterministic zero-state row for every roster member instead of exposing
	// an empty table and making the browser infer missing participants.
	syntheticStandings := len(entries) == 0
	if syntheticStandings {
		entries = make(map[uuid.UUID]standingsPayloadEntry, len(orderedIDs))
		for index, participantID := range orderedIDs {
			entries[participantID] = standingsPayloadEntry{ParticipantID: participantID, Position: index + 1}
		}
	}

	finalOrder, qualified, resolved, err := tournamentTopFourOrder(topFourPayload, names, entries, orderedIDs)
	if err != nil {
		return nil, err
	}
	if syntheticStandings && resolved {
		return nil, tournamentSnapshotInvalidError("top four without standings")
	}
	provisional, err := tournamentProvisionalTie(document.TieGroups, names, entries, resolved)
	if err != nil {
		return nil, err
	}
	return tournamentScoreboardView(finalOrder, names, entries, qualified, resolved, provisional), nil
}

func tournamentScoreboardRoster(
	participants []sqlc.ListTournamentReadParticipantsRow,
) (map[uuid.UUID]string, []uuid.UUID, error) {
	names := tournamentParticipantNames(participants)
	if len(names) != len(participants) {
		return nil, nil, tournamentSnapshotInvalidError("duplicate roster participant")
	}
	orderedIDs := make([]uuid.UUID, len(participants))
	previousSeed := int32(0)
	for index, participant := range participants {
		if participant.ParticipantID == uuid.Nil || strings.TrimSpace(participant.DisplayName) == "" ||
			participant.Seed < 1 || participant.Seed <= previousSeed {
			return nil, nil, tournamentSnapshotInvalidError("roster participant")
		}
		previousSeed = participant.Seed
		orderedIDs[index] = participant.ParticipantID
	}
	return names, orderedIDs, nil
}

func tournamentStandingsPayload(
	payload []byte,
	names map[uuid.UUID]string,
) (standingsPayloadDocument, map[uuid.UUID]standingsPayloadEntry, error) {
	var document standingsPayloadDocument
	if err := json.Unmarshal(payload, &document); err != nil || document.Entries == nil {
		return standingsPayloadDocument{}, nil, tournamentSnapshotInvalidError("standings payload")
	}
	entries := make(map[uuid.UUID]standingsPayloadEntry, len(document.Entries))
	seenPositions := make(map[int]struct{}, len(document.Entries))
	for _, entry := range document.Entries {
		displayName, ok := names[entry.ParticipantID]
		if !ok || !validTournamentStandingsEntry(entry, displayName) {
			return standingsPayloadDocument{}, nil, tournamentSnapshotInvalidError("standings entry")
		}
		if _, duplicate := entries[entry.ParticipantID]; duplicate {
			return standingsPayloadDocument{}, nil, tournamentSnapshotInvalidError("duplicate standings participant")
		}
		if _, duplicate := seenPositions[entry.Position]; duplicate {
			return standingsPayloadDocument{}, nil, tournamentSnapshotInvalidError("duplicate standings position")
		}
		entries[entry.ParticipantID] = entry
		seenPositions[entry.Position] = struct{}{}
	}
	return document, entries, nil
}

func validTournamentStandingsEntry(entry standingsPayloadEntry, displayName string) bool {
	return entry.ParticipantID != uuid.Nil && strings.TrimSpace(displayName) != "" && entry.Position >= 1 &&
		entry.Points >= 0 && entry.Wins >= 0 && entry.Losses >= 0 && entry.ByeCount >= 0 &&
		entry.Buchholz >= 0 && entry.EffectiveTime >= 0
}

func tournamentScoreboardView(
	order []uuid.UUID,
	names map[uuid.UUID]string,
	entries map[uuid.UUID]standingsPayloadEntry,
	qualified map[uuid.UUID]bool,
	resolved bool,
	provisional map[uuid.UUID]bool,
) []usecase.PublicScoreboardEntryView {
	view := make([]usecase.PublicScoreboardEntryView, len(order))
	for index, participantID := range order {
		entry := entries[participantID]
		status := "pending"
		if resolved && qualified[participantID] {
			status = "qualified"
		} else if resolved {
			status = "eliminated"
		}
		view[index] = usecase.PublicScoreboardEntryView{
			Rank: index + 1, DisplayName: names[participantID], Points: entry.Points,
			Wins: entry.Wins, Losses: entry.Losses, ByeCount: entry.ByeCount,
			Buchholz: entry.Buchholz, EffectiveTimeMS: entry.EffectiveTime / int64(time.Millisecond),
			ProvisionalTie: provisional[participantID], QualificationStatus: status,
		}
	}
	return view
}

func tournamentTopFourOrder(
	payload []byte,
	names map[uuid.UUID]string,
	entries map[uuid.UUID]standingsPayloadEntry,
	orderedRoster []uuid.UUID,
) ([]uuid.UUID, map[uuid.UUID]bool, bool, error) {
	qualified := make(map[uuid.UUID]bool)
	if len(payload) == 0 {
		return standingsOrder(entries, orderedRoster), qualified, false, nil
	}
	var document topFourPayloadDocument
	if err := json.Unmarshal(payload, &document); err != nil || document.Participants == nil {
		return nil, nil, false, tournamentSnapshotInvalidError("top four payload")
	}
	participants, err := topFourParticipants(document.Participants)
	if err != nil {
		return nil, nil, false, err
	}
	if len(participants) == 0 {
		return standingsOrder(entries, orderedRoster), qualified, false, nil
	}
	if len(participants) != 4 {
		return nil, nil, false, tournamentSnapshotInvalidError("top four roster coverage")
	}
	for _, participantID := range participants {
		if participantID == uuid.Nil || names[participantID] == "" {
			return nil, nil, false, tournamentSnapshotInvalidError("top four participant")
		}
		if _, duplicate := qualified[participantID]; duplicate {
			return nil, nil, false, tournamentSnapshotInvalidError("duplicate top four participant")
		}
		if _, ok := entries[participantID]; !ok {
			return nil, nil, false, tournamentSnapshotInvalidError("top four standings participant")
		}
		qualified[participantID] = true
	}
	order := make([]uuid.UUID, 0, len(entries))
	order = append(order, participants...)
	for _, participantID := range standingsOrder(entries, orderedRoster) {
		if !qualified[participantID] {
			order = append(order, participantID)
		}
	}
	return order, qualified, true, nil
}

func topFourParticipants(raw json.RawMessage) ([]uuid.UUID, error) {
	var participants []uuid.UUID
	if err := json.Unmarshal(raw, &participants); err == nil && participants != nil {
		return participants, nil
	}
	// Older canonical revisions encoded the same final order with explicit
	// seed objects. Accepting that shape keeps recovery readable across a
	// revision boundary while the current contract uses UUID array order.
	var seeded []topFourPayloadParticipant
	if err := json.Unmarshal(raw, &seeded); err != nil || seeded == nil {
		return nil, tournamentSnapshotInvalidError("top four participants")
	}
	sort.Slice(seeded, func(i, j int) bool { return seeded[i].Seed < seeded[j].Seed })
	participants = make([]uuid.UUID, len(seeded))
	for index, participant := range seeded {
		if participant.Seed != index+1 {
			return nil, tournamentSnapshotInvalidError("top four participant seed")
		}
		participants[index] = participant.ParticipantID
	}
	return participants, nil
}

func standingsOrder(entries map[uuid.UUID]standingsPayloadEntry, fallback []uuid.UUID) []uuid.UUID {
	order := make([]uuid.UUID, 0, len(entries))
	for participantID := range entries {
		order = append(order, participantID)
	}
	sort.Slice(order, func(i, j int) bool {
		left, right := entries[order[i]], entries[order[j]]
		if left.Position != right.Position {
			return left.Position < right.Position
		}
		return order[i].String() < order[j].String()
	})
	if len(order) != 0 {
		return order
	}
	return append([]uuid.UUID(nil), fallback...)
}

func tournamentProvisionalTie(
	tieGroups []standingsPayloadTieGroup,
	names map[uuid.UUID]string,
	entries map[uuid.UUID]standingsPayloadEntry,
	goldenResolved bool,
) (map[uuid.UUID]bool, error) {
	result := make(map[uuid.UUID]bool)
	if goldenResolved {
		return result, nil
	}
	for _, group := range tieGroups {
		if group.PositionFrom < 1 || group.PositionTo < group.PositionFrom ||
			len(group.ParticipantIDs) < 2 || group.PositionTo-group.PositionFrom+1 != len(group.ParticipantIDs) {
			return nil, tournamentSnapshotInvalidError("standings tie group")
		}
		seen := make(map[uuid.UUID]struct{}, len(group.ParticipantIDs))
		for _, participantID := range group.ParticipantIDs {
			if participantID == uuid.Nil || names[participantID] == "" {
				return nil, tournamentSnapshotInvalidError("standings tie participant")
			}
			if _, duplicate := seen[participantID]; duplicate {
				return nil, tournamentSnapshotInvalidError("duplicate standings tie participant")
			}
			if _, ok := entries[participantID]; !ok {
				return nil, tournamentSnapshotInvalidError("standings tie participant coverage")
			}
			seen[participantID] = struct{}{}
			if group.Impactful {
				result[participantID] = true
			}
		}
	}
	return result, nil
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
	view := make([]usecase.PublicBracketMatchView, 0, len(document.Rounds)+1)
	seenPositions := make(map[string]struct{}, len(document.Rounds))
	semifinalCount := 0
	finalCount := 0
	for _, match := range document.Rounds {
		stage := match.Stage
		if stage == "" {
			stage = tournamentBracketStageSemifinal
		}
		firstName, firstOK := names[match.FirstParticipantID]
		secondName, secondOK := names[match.SecondParticipantID]
		if !firstOK || !secondOK || match.FirstParticipantID == match.SecondParticipantID ||
			match.Position < 1 ||
			(stage != tournamentBracketStageSemifinal && stage != tournamentBracketStageFinal) ||
			strings.TrimSpace(firstName) == "" || utf8.RuneCountInString(firstName) > 64 ||
			strings.TrimSpace(secondName) == "" || utf8.RuneCountInString(secondName) > 64 ||
			strings.TrimSpace(match.State) == "" || match.FirstWins < 0 || match.SecondWins < 0 {
			return nil, tournamentSnapshotInvalidError("bracket match")
		}
		maxWins := 1
		if stage == tournamentBracketStageFinal {
			maxWins = 2
		}
		if match.FirstWins > maxWins || match.SecondWins > maxWins || !domain.SeriesState(match.State).IsValid() {
			return nil, tournamentSnapshotInvalidError("bracket match")
		}
		positionKey := fmt.Sprintf("%s:%d", stage, match.Position)
		if _, duplicate := seenPositions[positionKey]; duplicate {
			return nil, tournamentSnapshotInvalidError("duplicate bracket position")
		}
		seenPositions[positionKey] = struct{}{}
		format := string(domain.SeriesFormatBO1)
		if (stage == tournamentBracketStageSemifinal && match.Position > 2) ||
			(stage == tournamentBracketStageFinal && match.Position != 1) {
			return nil, tournamentSnapshotInvalidError("bracket position")
		}
		if stage == tournamentBracketStageFinal {
			format = string(domain.SeriesFormatBO3)
			finalCount++
		} else {
			semifinalCount++
		}
		firstDisplayName := firstName
		secondDisplayName := secondName
		var winnerDisplayName *string
		if match.State == string(domain.SeriesStateCompleted) {
			if match.FirstWins == match.SecondWins {
				return nil, tournamentSnapshotInvalidError("bracket match tie")
			}
			if match.FirstWins > match.SecondWins {
				winnerDisplayName = &firstDisplayName
			} else {
				winnerDisplayName = &secondDisplayName
			}
		}
		view = append(view, usecase.PublicBracketMatchView{
			Stage:             stage,
			Position:          match.Position,
			Format:            format,
			FirstDisplayName:  &firstDisplayName,
			SecondDisplayName: &secondDisplayName,
			FirstWins:         match.FirstWins,
			SecondWins:        match.SecondWins,
			State:             match.State,
			ScheduledAt:       nil,
			WinnerDisplayName: winnerDisplayName,
		})
	}
	if len(view) == 0 {
		return view, nil
	}
	if semifinalCount != 2 || finalCount > 1 {
		return nil, tournamentSnapshotInvalidError("bracket topology")
	}
	if finalCount == 0 {
		view = append(view, usecase.PublicBracketMatchView{
			Stage:      tournamentBracketStageFinal,
			Position:   1,
			Format:     string(domain.SeriesFormatBO3),
			FirstWins:  0,
			SecondWins: 0,
			State:      string(domain.SeriesStatePlanned),
		})
	}
	sort.Slice(view, func(left, right int) bool {
		if view[left].Stage == view[right].Stage {
			return view[left].Position < view[right].Position
		}
		return view[left].Stage == tournamentBracketStageSemifinal
	})
	return view, nil
}

func publicTournamentReadSwissRounds(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
) ([]usecase.PublicSwissRoundView, error) {
	rows, err := querier.ListPublicTournamentReadSwissRounds(ctx, tournamentID)
	if err != nil {
		return nil, fmt.Errorf("TournamentSnapshotPostgres - PublicSnapshot - swiss rounds: %w", err)
	}
	view := make([]usecase.PublicSwissRoundView, len(rows))
	previousRound := int16(0)
	for index, row := range rows {
		if row.RoundNumber < 1 || row.RoundNumber > 4 || row.RoundNumber <= previousRound ||
			strings.TrimSpace(row.State) != row.State || !domain.WaveState(row.State).IsValid() {
			return nil, tournamentSnapshotInvalidError("swiss round")
		}
		previousRound = row.RoundNumber
		current := usecase.PublicSwissRoundView{
			RoundNumber: int(row.RoundNumber),
			State:       row.State,
		}
		if row.ByePointsAwarded != nil {
			if *row.ByePointsAwarded != 1 || strings.TrimSpace(row.ByeDisplayName) == "" || utf8.RuneCountInString(row.ByeDisplayName) > 64 {
				return nil, tournamentSnapshotInvalidError("swiss bye")
			}
			current.Bye = &usecase.PublicSwissByeView{
				DisplayName:   row.ByeDisplayName,
				PointsAwarded: int(*row.ByePointsAwarded),
			}
		} else if strings.TrimSpace(row.ByeDisplayName) != "" {
			if utf8.RuneCountInString(row.ByeDisplayName) > 64 {
				return nil, tournamentSnapshotInvalidError("swiss bye")
			}
			current.Bye = &usecase.PublicSwissByeView{
				DisplayName:   row.ByeDisplayName,
				PointsAwarded: 1,
			}
		}
		view[index] = current
	}
	return view, nil
}

//nolint:gocyclo // Public series decoding validates all stage, score, state, and time invariants at the storage boundary.
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
		format := domain.SeriesFormat(row.Format)
		state := domain.SeriesState(row.State)
		if !stage.IsValid() || (stage == domain.TournamentStageSwiss &&
			(row.RoundNumber < 1 || row.RoundNumber > 4)) ||
			(stage != domain.TournamentStageSwiss && row.RoundNumber != 0) ||
			row.SeriesID == uuid.Nil || !format.IsValid() || !state.IsValid() ||
			strings.TrimSpace(row.FirstDisplayName) == "" || strings.TrimSpace(row.SecondDisplayName) == "" ||
			row.CurrentGamePosition < 0 || row.CurrentGamePosition > 3 ||
			(domain.SeriesScore{FirstParticipantWins: int(row.FirstParticipantWins), SecondParticipantWins: int(row.SecondParticipantWins)}).Validate(format) != nil {
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
