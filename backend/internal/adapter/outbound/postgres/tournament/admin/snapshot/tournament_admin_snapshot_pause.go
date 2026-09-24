package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	draftpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	pausemodel "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/model"
	tournamentadminexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
	rostercapability "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/snapshot"
)

func (r *TournamentAdminSnapshotPostgres) loadPauseGraph(
	ctx context.Context,
	querier *sqlc.Queries,
	header tournamentAdminSnapshotHeaderState,
	roster rostercapability.RosterView,
	waves []tournamentadminexecution.WaveView,
	seriesGraph tournamentAdminSnapshotSeriesGraph,
	pauseRows []sqlc.Pause,
) (*tournamentadmin.PauseGraphView, error) {
	pauseIndex, err := tournamentAdminSnapshotPauses(pauseRows, header, roster, waves, seriesGraph)
	if err != nil {
		return nil, fmt.Errorf("validate pause rows: %w", err)
	}
	root, err := pauseIndex.normalRoot(header.tournament.State)
	if err != nil || root == nil {
		return nil, err
	}
	if header.authority == nil {
		return nil, domain.ErrInternal
	}

	wave, exists := tournamentAdminSnapshotWaveByID(waves, root.WaveID.UUID)
	if !exists || !tournamentAdminSnapshotRootMatchesWave(*root, wave) {
		return nil, domain.ErrInternal
	}
	seriesValues, games, seriesIDs, err := tournamentAdminSnapshotPauseExecutions(
		*root,
		wave,
		seriesGraph,
		pauseIndex,
	)
	if err != nil {
		return nil, fmt.Errorf("load pause executions: %w", err)
	}

	draft, err := r.loadSnapshotPauseDraft(ctx, querier, wave, roster, seriesValues)
	if err != nil {
		return nil, fmt.Errorf("load pause Draft: %w", err)
	}
	presence, reconnect, counters, err := tournamentAdminSnapshotPauseConnectivity(
		ctx,
		querier,
		header,
		roster,
		seriesIDs,
		seriesGraph,
		pauseIndex,
		*root,
	)
	if err != nil {
		return nil, fmt.Errorf("load pause connectivity: %w", err)
	}
	clocks, err := querier.ListTournamentAdminSnapshotPauseClocks(ctx, sqlc.ListTournamentAdminSnapshotPauseClocksParams{
		TournamentID: header.tournament.ID,
		RosterID:     roster.ID,
	})
	if err != nil {
		return nil, tournamentAdminSnapshotQueryError("pause clocks", err)
	}
	if err := attachTournamentAdminSnapshotSourcePauses(
		ctx, querier, *root, seriesValues, games, pauseIndex, clocks, counters,
	); err != nil {
		return nil, fmt.Errorf("load source pause evidence: %w", err)
	}
	frozen, err := tournamentAdminSnapshotFrozenDeadlines(*root, wave, games, draft, clocks, pauseIndex)
	if err != nil {
		return nil, fmt.Errorf("load pause frozen deadlines: %w", err)
	}

	pausedAt, valid := tournamentAdminSnapshotRequiredTime(root.StartedAt)
	if !valid {
		return nil, domain.ErrInternal
	}
	graph := gameusecase.PauseGraph{
		Scope: pausedomain.GraphScope{
			TournamentID: header.tournament.ID,
			RosterID:     roster.ID,
			WaveID:       wave.Wave.ID,
			Authority:    *header.authority,
		},
		Revision:               root.Revision,
		Tournament:             tournamentAdminSnapshotPauseTournament(header),
		Wave:                   gameusecase.PauseWave{Wave: wave.Wave, Revision: wave.Revision},
		Series:                 seriesValues,
		Games:                  games,
		Draft:                  draft,
		Presence:               presence,
		Reconnect:              reconnect,
		Counters:               counters,
		FrozenDeadlines:        frozen,
		ActivePauseID:          root.ID,
		PausedAt:               &pausedAt,
		DeadlinesSuppressed:    true,
		TerminalActionRevision: 0,
	}
	view := &tournamentadmin.PauseGraphView{Graph: graph, Wave: wave}
	if !tournamentAdminSnapshotPauseGraphValid(*view, *root, seriesIDs, pauseIndex) {
		return nil, fmt.Errorf("validate pause graph: %w", domain.ErrInternal)
	}
	return view, nil
}

const tournamentAdminSnapshotPauseReceiptVersion = 1

type tournamentAdminSnapshotPauseReceipt struct {
	Version int                            `json:"version"`
	View    json.RawMessage                `json:"view"`
	Pause   *gameusecase.NormalPauseRecord `json:"normal_pause"`
}

func attachTournamentAdminSnapshotSourcePauses(
	ctx context.Context,
	querier *sqlc.Queries,
	root sqlc.Pause,
	seriesValues []gameusecase.PauseSeries,
	games []gameusecase.PauseGame,
	pauseIndex tournamentAdminSnapshotPauseIndex,
	clockRows []sqlc.PauseClock,
	counters []pausedomain.PauseReconnectCounter,
) error {
	seriesByID := make(map[uuid.UUID]gameusecase.PauseSeries, len(seriesValues))
	for _, series := range seriesValues {
		seriesByID[series.Execution.Series.ID] = series
	}
	gameIndexes, err := tournamentAdminSnapshotSourceGameIndexes(games, seriesByID, pauseIndex, root)
	if err != nil {
		return err
	}
	if len(gameIndexes) == 0 {
		return nil
	}

	stored, err := tournamentAdminSnapshotLoadPauseReceipt(ctx, querier, root)
	if err != nil {
		return err
	}
	storedGames, err := tournamentAdminSnapshotReceiptSourceGames(stored.Graph.Games)
	if err != nil {
		return err
	}
	if len(storedGames) != len(gameIndexes) {
		return fmt.Errorf("pause receipt source Game set mismatch: %w", domain.ErrInternal)
	}

	for gameID, index := range gameIndexes {
		game := &games[index]
		series := seriesByID[game.SeriesID]
		row := pauseIndex.activeByScope[tournamentAdminSnapshotPauseScope{kind: "game_attempt", id: gameID}]
		if err := attachTournamentAdminSnapshotSourcePause(
			ctx, querier, root, game, series, row, clockRows, stored.Graph.Counters,
			counters, storedGames,
		); err != nil {
			return err
		}
	}
	return nil
}

func tournamentAdminSnapshotSourceGameIndexes(
	games []gameusecase.PauseGame,
	seriesByID map[uuid.UUID]gameusecase.PauseSeries,
	pauseIndex tournamentAdminSnapshotPauseIndex,
	root sqlc.Pause,
) (map[uuid.UUID]int, error) {
	indexes := make(map[uuid.UUID]int)
	for index, game := range games {
		series, exists := seriesByID[game.SeriesID]
		if !exists {
			return nil, fmt.Errorf("adopted pause Series missing: %w", domain.ErrInternal)
		}
		if series.Execution.Series.State != domain.SeriesStateActive || game.Game.State != domain.GameStatePaused {
			continue
		}
		if series.Execution.ResumeState != nil || game.ResumeState != nil || game.SourcePause != nil {
			return nil, fmt.Errorf("adopted pause has normal resume evidence: %w", domain.ErrInternal)
		}
		row, exists := pauseIndex.activeByScope[tournamentAdminSnapshotPauseScope{
			kind: "game_attempt", id: game.Game.ID,
		}]
		if !exists || !tournamentAdminSnapshotIndependentSourcePause(row, game.Game, game.SeriesID, root) {
			return nil, fmt.Errorf("independent source pause row invalid: %w", domain.ErrInternal)
		}
		if _, duplicate := indexes[game.Game.ID]; duplicate {
			return nil, fmt.Errorf("duplicate adopted source Game: %w", domain.ErrInternal)
		}
		indexes[game.Game.ID] = index
	}
	return indexes, nil
}

func tournamentAdminSnapshotLoadPauseReceipt(
	ctx context.Context,
	querier *sqlc.Queries,
	root sqlc.Pause,
) (gameusecase.NormalPauseRecord, error) {
	document, err := querier.GetTournamentAdminNormalPauseReceipt(ctx, sqlc.GetTournamentAdminNormalPauseReceiptParams{
		TournamentID: root.TournamentID,
		WaveID:       root.WaveID.UUID,
		PauseID:      root.ID,
	})
	if err != nil {
		return gameusecase.NormalPauseRecord{}, tournamentAdminSnapshotQueryError("normal pause receipt", err)
	}
	var receipt tournamentAdminSnapshotPauseReceipt
	if json.Unmarshal(document, &receipt) != nil || receipt.Version != tournamentAdminSnapshotPauseReceiptVersion ||
		len(receipt.View) == 0 || receipt.Pause == nil || pausemodel.ValidateNormalPauseRecord(*receipt.Pause) != nil {
		return gameusecase.NormalPauseRecord{}, fmt.Errorf("invalid normal pause receipt evidence: %w", domain.ErrInternal)
	}
	if !tournamentAdminSnapshotPauseReceiptMatchesRoot(*receipt.Pause, root) {
		return gameusecase.NormalPauseRecord{}, fmt.Errorf("normal pause receipt root mismatch: %w", domain.ErrInternal)
	}
	return *receipt.Pause, nil
}

func tournamentAdminSnapshotPauseReceiptMatchesRoot(record gameusecase.NormalPauseRecord, root sqlc.Pause) bool {
	rootStartedAt, validRootTime := tournamentAdminSnapshotRequiredTime(root.StartedAt)
	return validRootTime && tournamentAdminSnapshotPauseReceiptIdentityMatches(record, root) &&
		tournamentAdminSnapshotPauseReceiptStateMatches(record, root, rootStartedAt) &&
		tournamentAdminSnapshotPauseReceiptGraphMatches(record, root, rootStartedAt)
}

func tournamentAdminSnapshotPauseReceiptIdentityMatches(record gameusecase.NormalPauseRecord, root sqlc.Pause) bool {
	return record.PauseID == root.ID && record.Scope.TournamentID == root.TournamentID &&
		record.Scope.RosterID == root.RosterID && record.Scope.WaveID == root.WaveID.UUID
}

func tournamentAdminSnapshotPauseReceiptStateMatches(
	record gameusecase.NormalPauseRecord,
	root sqlc.Pause,
	rootStartedAt time.Time,
) bool {
	return record.State == gameusecase.PauseStateActive && record.Revision == root.Revision && record.Revision >= 1 &&
		record.PausedAt.Equal(rootStartedAt)
}

func tournamentAdminSnapshotPauseReceiptGraphMatches(
	record gameusecase.NormalPauseRecord,
	root sqlc.Pause,
	rootStartedAt time.Time,
) bool {
	return record.Graph.Scope.TournamentID == root.TournamentID &&
		record.Graph.Scope.RosterID == root.RosterID && record.Graph.Scope.WaveID == root.WaveID.UUID &&
		record.Graph.ActivePauseID == root.ID && record.Graph.PausedAt != nil &&
		record.Graph.PausedAt.Equal(rootStartedAt)
}

func tournamentAdminSnapshotReceiptSourceGames(
	games []gameusecase.PauseGame,
) (map[uuid.UUID]gameusecase.PauseGame, error) {
	result := make(map[uuid.UUID]gameusecase.PauseGame)
	for _, game := range games {
		if game.SourcePause == nil {
			continue
		}
		if _, duplicate := result[game.Game.ID]; duplicate {
			return nil, fmt.Errorf("duplicate source Game in pause receipt: %w", domain.ErrInternal)
		}
		result[game.Game.ID] = game
	}
	return result, nil
}

func attachTournamentAdminSnapshotSourcePause(
	ctx context.Context,
	querier *sqlc.Queries,
	root sqlc.Pause,
	game *gameusecase.PauseGame,
	series gameusecase.PauseSeries,
	row sqlc.Pause,
	clockRows []sqlc.PauseClock,
	storedCounters, currentCounters []pausedomain.PauseReconnectCounter,
	storedGames map[uuid.UUID]gameusecase.PauseGame,
) error {
	storedGame, exists := storedGames[game.Game.ID]
	if !exists || !tournamentAdminSnapshotStoredSourceGameMatches(storedGame, *game) {
		return fmt.Errorf("pause receipt source Game mismatch: %w", domain.ErrInternal)
	}
	source := storedGame.SourcePause
	if source == nil || !tournamentAdminSnapshotSourcePauseEvidenceMatches(*source, row, root, *game, series, clockRows) {
		return fmt.Errorf("pause receipt source evidence mismatch: %w", domain.ErrInternal)
	}
	if !tournamentAdminSnapshotSourceCountersMatch(
		storedCounters, currentCounters, source.PauseID,
		series.Execution.Series.FirstParticipantID, series.Execution.Series.SecondParticipantID,
	) {
		return fmt.Errorf("source reconnect counters mismatch: %w", domain.ErrInternal)
	}
	decisionNumber, err := querier.GetTournamentAdminNormalPauseDecisionNumber(ctx, source.PauseID)
	if err != nil {
		return tournamentAdminSnapshotQueryError("source pause decision number", err)
	}
	if decisionNumber != source.DecisionNumber {
		return fmt.Errorf("source pause decision number mismatch: %w", domain.ErrInternal)
	}
	game.SourcePause = source
	return nil
}

func tournamentAdminSnapshotStoredSourceGameMatches(stored, current gameusecase.PauseGame) bool {
	return stored.SeriesID == current.SeriesID && stored.Game.ID == current.Game.ID &&
		stored.Game.SlotID == current.Game.SlotID && stored.Game.AttemptNo == current.Game.AttemptNo &&
		stored.Game.State == domain.GameStatePaused && stored.ResumeState == nil && stored.SourcePause != nil
}

func tournamentAdminSnapshotSourcePauseEvidenceMatches(
	source gameusecase.PauseGameSourcePause,
	row, root sqlc.Pause,
	game gameusecase.PauseGame,
	series gameusecase.PauseSeries,
	clockRows []sqlc.PauseClock,
) bool {
	return tournamentAdminSnapshotSourcePauseRowMatches(source, row, root, game) &&
		tournamentAdminSnapshotSourcePauseClockMatches(source, game.Game.ID, clockRows) &&
		tournamentAdminSnapshotSourcePauseGameMatches(game, series) &&
		tournamentAdminSnapshotSourcePresenceValid(source, series.Execution.Series.FirstParticipantID,
			series.Execution.Series.SecondParticipantID)
}

func tournamentAdminSnapshotSourcePauseRowMatches(
	source gameusecase.PauseGameSourcePause,
	row, root sqlc.Pause,
	game gameusecase.PauseGame,
) bool {
	startedAt, started := tournamentAdminSnapshotRequiredTime(row.StartedAt)
	rootStartedAt, rootStarted := tournamentAdminSnapshotRequiredTime(root.StartedAt)
	return started && rootStarted &&
		tournamentAdminSnapshotSourcePauseRowIdentityMatches(source, row, root, game) &&
		tournamentAdminSnapshotSourcePauseRowStateMatches(source, row) &&
		tournamentAdminSnapshotSourcePauseRowTimelineMatches(source, startedAt, rootStartedAt)
}

func tournamentAdminSnapshotSourcePauseRowIdentityMatches(
	source gameusecase.PauseGameSourcePause,
	row, root sqlc.Pause,
	game gameusecase.PauseGame,
) bool {
	return source.PauseID == row.ID &&
		tournamentAdminSnapshotIndependentSourcePauseIdentityMatches(row, game.Game, game.SeriesID, root) &&
		source.ScopeKind == row.ScopeKind && source.ScopeID == row.ScopeID &&
		source.SeriesID == game.SeriesID && source.GameID == game.Game.ID
}

func tournamentAdminSnapshotSourcePauseRowStateMatches(
	source gameusecase.PauseGameSourcePause,
	row sqlc.Pause,
) bool {
	return tournamentAdminSnapshotIndependentSourcePauseStateMatches(row) &&
		source.Reason == gameusecase.PauseReasonDisconnect && source.ParentPauseID == nil &&
		source.Depth == 0 && source.State == gameusecase.PauseStateActive &&
		source.CurrentRevisionID == row.CurrentRevisionID && source.Revision == row.Revision &&
		source.ResolvedAt == nil && source.DecisionNumber >= 0
}

func tournamentAdminSnapshotSourcePauseRowTimelineMatches(
	source gameusecase.PauseGameSourcePause,
	startedAt, rootStartedAt time.Time,
) bool {
	return source.StartedAt.Equal(startedAt) && !startedAt.After(rootStartedAt)
}

func tournamentAdminSnapshotSourcePauseGameMatches(
	game gameusecase.PauseGame,
	series gameusecase.PauseSeries,
) bool {
	return series.Execution.Series.State == domain.SeriesStateActive && series.Execution.ResumeState == nil &&
		series.CurrentGameID != nil && *series.CurrentGameID == game.Game.ID &&
		game.ResumeState == nil && game.Game.State == domain.GameStatePaused
}

func tournamentAdminSnapshotSourcePauseClockMatches(
	source gameusecase.PauseGameSourcePause,
	gameID uuid.UUID,
	clockRows []sqlc.PauseClock,
) bool {
	if !tournamentAdminSnapshotSourcePauseClockValid(source, gameID) {
		return false
	}
	clock, found := tournamentAdminSnapshotSourcePauseClockRow(clockRows, source.PauseID)
	return found && tournamentAdminSnapshotSourcePauseClockRowMatches(clock, source, gameID)
}

func tournamentAdminSnapshotSourcePauseClockValid(source gameusecase.PauseGameSourcePause, gameID uuid.UUID) bool {
	clock := source.Clock
	return clock.Kind == gameusecase.PauseDeadlineGame && clock.OwnerID == gameID && clock.Revision >= 1 &&
		clock.FrozenAt.Equal(source.StartedAt) && clock.ResumedAt == nil && clock.ResumedDeadline == nil &&
		domain.IsValidServerTime(clock.OriginalDeadline) && domain.IsValidServerTime(clock.FrozenAt) &&
		clock.Remaining > 0 && clock.Remaining <= tournamentAdminSnapshotMaxFrozenDuration &&
		clock.OriginalDeadline.Sub(clock.FrozenAt) == clock.Remaining
}

func tournamentAdminSnapshotSourcePauseClockRow(
	rows []sqlc.PauseClock,
	pauseID uuid.UUID,
) (sqlc.PauseClock, bool) {
	var result sqlc.PauseClock
	found := false
	for _, row := range rows {
		if row.PauseID != pauseID {
			continue
		}
		if found {
			return sqlc.PauseClock{}, false
		}
		result = row
		found = true
	}
	return result, found
}

func tournamentAdminSnapshotSourcePauseClockRowMatches(
	row sqlc.PauseClock,
	source gameusecase.PauseGameSourcePause,
	gameID uuid.UUID,
) bool {
	if row.GameAttemptID != gameID || row.Revision != source.Clock.Revision ||
		row.FrozenRemainingMs != source.Clock.Remaining.Milliseconds() ||
		row.ResumedAt.Valid || row.ResumedDeadline.Valid ||
		!tournamentAdminSnapshotStoredTimeline(row.CreatedAt, row.UpdatedAt) {
		return false
	}
	originalDeadline, validDeadline := tournamentAdminSnapshotRequiredTime(row.OriginalDeadline)
	frozenAt, validFrozenAt := tournamentAdminSnapshotRequiredTime(row.FrozenAt)
	createdAt, validCreatedAt := tournamentAdminSnapshotRequiredTime(row.CreatedAt)
	return validDeadline && validFrozenAt && validCreatedAt &&
		originalDeadline.Equal(source.Clock.OriginalDeadline) && frozenAt.Equal(source.Clock.FrozenAt) &&
		!createdAt.Before(source.StartedAt)
}

func tournamentAdminSnapshotSourcePresenceValid(
	source gameusecase.PauseGameSourcePause,
	firstParticipantID, secondParticipantID uuid.UUID,
) bool {
	if firstParticipantID == uuid.Nil || secondParticipantID == uuid.Nil || firstParticipantID == secondParticipantID ||
		len(source.Presence) != 2 {
		return false
	}
	seen := make(map[uuid.UUID]struct{}, 2)
	for _, snapshot := range source.Presence {
		if snapshot.ParticipantID != firstParticipantID && snapshot.ParticipantID != secondParticipantID ||
			(snapshot.State != pausedomain.PresenceStateConnected && snapshot.State != pausedomain.PresenceStateDisconnected) ||
			snapshot.PresenceEpoch < 1 || snapshot.Revision < 1 ||
			!domain.IsValidServerTime(snapshot.CapturedAt) || !snapshot.CapturedAt.Equal(source.StartedAt) {
			return false
		}
		if _, duplicate := seen[snapshot.ParticipantID]; duplicate {
			return false
		}
		seen[snapshot.ParticipantID] = struct{}{}
	}
	return len(seen) == 2
}

func tournamentAdminSnapshotSourceCountersMatch(
	stored, current []pausedomain.PauseReconnectCounter,
	pauseID, firstParticipantID, secondParticipantID uuid.UUID,
) bool {
	if pauseID == uuid.Nil || firstParticipantID == uuid.Nil || secondParticipantID == uuid.Nil ||
		firstParticipantID == secondParticipantID {
		return false
	}
	want, wantValid := tournamentAdminSnapshotSourceCounterIndex(stored, pauseID, firstParticipantID, secondParticipantID)
	got, gotValid := tournamentAdminSnapshotSourceCounterIndex(current, pauseID, firstParticipantID, secondParticipantID)
	return wantValid && gotValid && want[firstParticipantID] == got[firstParticipantID] &&
		want[secondParticipantID] == got[secondParticipantID]
}

func tournamentAdminSnapshotSourceCounterIndex(
	values []pausedomain.PauseReconnectCounter,
	pauseID, firstParticipantID, secondParticipantID uuid.UUID,
) (map[uuid.UUID]pausedomain.PauseReconnectCounter, bool) {
	result := make(map[uuid.UUID]pausedomain.PauseReconnectCounter, 2)
	for _, counter := range values {
		if counter.PauseID != pauseID {
			continue
		}
		if !tournamentAdminSnapshotSourceCounterValid(counter, firstParticipantID, secondParticipantID) {
			return nil, false
		}
		if _, duplicate := result[counter.ParticipantID]; duplicate {
			return nil, false
		}
		result[counter.ParticipantID] = counter
	}
	return result, len(result) == 2
}

func tournamentAdminSnapshotSourceCounterValid(
	counter pausedomain.PauseReconnectCounter,
	firstParticipantID, secondParticipantID uuid.UUID,
) bool {
	return counter.Validate() == nil &&
		(counter.ParticipantID == firstParticipantID || counter.ParticipantID == secondParticipantID)
}

func (r *TournamentAdminSnapshotPostgres) loadSnapshotPauseDraft(
	ctx context.Context,
	querier *sqlc.Queries,
	wave tournamentadminexecution.WaveView,
	roster rostercapability.RosterView,
	seriesValues []gameusecase.PauseSeries,
) (*draftusecase.Execution, error) {
	draftIDs, err := querier.ListTournamentAdminSnapshotActiveDraftIDs(ctx, wave.Wave.ID)
	if err != nil {
		return nil, tournamentAdminSnapshotQueryError("active drafts", err)
	}
	if len(draftIDs) > 1 || len(draftIDs) == 1 && draftIDs[0] == uuid.Nil {
		return nil, domain.ErrInternal
	}
	if len(draftIDs) == 0 {
		if tournamentAdminSnapshotSeriesNeedsDraft(seriesValues) {
			return nil, domain.ErrInternal
		}
		return nil, nil
	}
	aggregate, err := r.drafts.Get(ctx, draftIDs[0])
	if err != nil {
		if errors.Is(err, draftpostgres.ErrDraftNotFound) {
			return nil, domain.ErrInternal
		}
		return nil, err
	}
	if aggregate == nil || aggregate.Draft.RosterID != roster.ID || len(aggregate.Revisions) == 0 {
		return nil, domain.ErrInternal
	}
	execution, err := participantDraftExecution(aggregate, aggregate.Revisions[len(aggregate.Revisions)-1].Revision)
	if err != nil || execution == nil || !tournamentAdminSnapshotDraftMatchesSeries(*execution, seriesValues) {
		return nil, domain.ErrInternal
	}
	return execution, nil
}

func tournamentAdminSnapshotPauseConnectivity(
	ctx context.Context,
	querier *sqlc.Queries,
	header tournamentAdminSnapshotHeaderState,
	roster rostercapability.RosterView,
	seriesIDs map[uuid.UUID]struct{},
	seriesGraph tournamentAdminSnapshotSeriesGraph,
	pauseIndex tournamentAdminSnapshotPauseIndex,
	root sqlc.Pause,
) ([]pausedomain.PausePresence, []pausedomain.PauseReconnectInterval, []pausedomain.PauseReconnectCounter, error) {
	params := sqlc.ListTournamentAdminSnapshotPresenceParams{
		TournamentID: header.tournament.ID,
		RosterID:     roster.ID,
		PauseID:      root.ID,
	}
	presenceRows, err := querier.ListTournamentAdminSnapshotPresence(ctx, params)
	if err != nil {
		return nil, nil, nil, tournamentAdminSnapshotQueryError("presence", err)
	}
	intervalRows, err := querier.ListTournamentAdminSnapshotReconnectIntervals(
		ctx,
		sqlc.ListTournamentAdminSnapshotReconnectIntervalsParams{
			TournamentID: header.tournament.ID,
			RosterID:     roster.ID,
		},
	)
	if err != nil {
		return nil, nil, nil, tournamentAdminSnapshotQueryError("reconnect intervals", err)
	}
	counterRows, err := querier.ListTournamentAdminSnapshotReconnectCounters(
		ctx,
		sqlc.ListTournamentAdminSnapshotReconnectCountersParams{
			TournamentID: header.tournament.ID,
			RosterID:     roster.ID,
		},
	)
	if err != nil {
		return nil, nil, nil, tournamentAdminSnapshotQueryError("reconnect counters", err)
	}

	presenceRows = tournamentAdminSnapshotPresenceRows(presenceRows, seriesIDs)
	intervalRows = tournamentAdminSnapshotReconnectRows(intervalRows, seriesIDs)
	counterRows, ok := tournamentAdminSnapshotCounterRows(counterRows, seriesIDs, pauseIndex)
	if !ok {
		return nil, nil, nil, domain.ErrInternal
	}
	presence, err := recoveryPresence(presenceRows)
	if err != nil {
		return nil, nil, nil, domain.ErrInternal
	}
	reconnect, err := recoveryIntervals(intervalRows)
	if err != nil {
		return nil, nil, nil, domain.ErrInternal
	}
	counters, err := recoveryCounters(counterRows)
	if err != nil {
		return nil, nil, nil, domain.ErrInternal
	}
	if !tournamentAdminSnapshotConnectivityValid(
		header.tournament.ID,
		roster.ID,
		seriesIDs,
		seriesGraph,
		pauseIndex,
		root,
		presence,
		reconnect,
		counters,
	) {
		return nil, nil, nil, domain.ErrInternal
	}
	return presence, reconnect, counters, nil
}
