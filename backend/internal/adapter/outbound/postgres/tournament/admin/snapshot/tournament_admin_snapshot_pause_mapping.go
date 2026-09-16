package snapshot

import (
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

const tournamentAdminSnapshotMaxFrozenDuration = 7 * 24 * time.Hour

type tournamentAdminSnapshotPauseScope struct {
	kind string
	id   uuid.UUID
}

type tournamentAdminSnapshotPauseIndex struct {
	byID                  map[uuid.UUID]sqlc.Pause
	activeByScope         map[tournamentAdminSnapshotPauseScope]sqlc.Pause
	activeWaveRoots       []sqlc.Pause
	activeTournamentRoots []sqlc.Pause
}

func tournamentAdminSnapshotPauses(
	rows []sqlc.Pause,
	header tournamentAdminSnapshotHeaderState,
	roster tournamentadmin.RosterView,
	waves []tournamentadmin.WaveView,
	seriesGraph tournamentAdminSnapshotSeriesGraph,
) (tournamentAdminSnapshotPauseIndex, error) {
	index := tournamentAdminSnapshotPauseIndex{
		byID:          make(map[uuid.UUID]sqlc.Pause, len(rows)),
		activeByScope: make(map[tournamentAdminSnapshotPauseScope]sqlc.Pause),
	}
	waveIDs := make(map[uuid.UUID]struct{}, len(waves))
	for _, wave := range waves {
		waveIDs[wave.Wave.ID] = struct{}{}
	}
	for _, row := range rows {
		if !tournamentAdminSnapshotPauseRowValid(row, header.tournament.ID, roster.ID, waveIDs, seriesGraph) {
			return tournamentAdminSnapshotPauseIndex{}, domain.ErrInternal
		}
		if _, duplicate := index.byID[row.ID]; duplicate {
			return tournamentAdminSnapshotPauseIndex{}, domain.ErrInternal
		}
		index.byID[row.ID] = row
		if row.State != string(gameusecase.PauseStateActive) {
			continue
		}
		scope := tournamentAdminSnapshotPauseScope{kind: row.ScopeKind, id: row.ScopeID}
		if _, duplicate := index.activeByScope[scope]; duplicate {
			return tournamentAdminSnapshotPauseIndex{}, domain.ErrInternal
		}
		index.activeByScope[scope] = row
		if row.ParentPauseID.Valid {
			continue
		}
		switch row.ScopeKind {
		case "wave":
			index.activeWaveRoots = append(index.activeWaveRoots, row)
		case "tournament":
			index.activeTournamentRoots = append(index.activeTournamentRoots, row)
		}
	}
	for _, row := range rows {
		if !tournamentAdminSnapshotPauseParentValid(row, index.byID) {
			return tournamentAdminSnapshotPauseIndex{}, domain.ErrInternal
		}
	}
	return index, nil
}

func tournamentAdminSnapshotPauseRowValid(
	row sqlc.Pause,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	waveIDs map[uuid.UUID]struct{},
	seriesGraph tournamentAdminSnapshotSeriesGraph,
) bool {
	return tournamentAdminSnapshotPauseHeaderValid(row, tournamentID, rosterID) &&
		tournamentAdminSnapshotPauseTimelineValid(row) &&
		tournamentAdminSnapshotPauseScopeValid(row, tournamentID, waveIDs, seriesGraph) &&
		tournamentAdminSnapshotPauseOriginValid(row.ScopeKind, row.PausedFromState)
}

func tournamentAdminSnapshotPauseHeaderValid(row sqlc.Pause, tournamentID, rosterID uuid.UUID) bool {
	return row.ID != uuid.Nil && row.TournamentID == tournamentID && row.RosterID == rosterID &&
		row.ScopeID != uuid.Nil && row.CurrentRevisionID != uuid.Nil && row.Revision >= 1 &&
		row.Depth >= 0 && row.Depth <= 3 && row.PausedFromState != "" &&
		row.PausedFromState == strings.TrimSpace(row.PausedFromState) && tournamentAdminSnapshotPauseReasonValid(row.Reason)
}

func tournamentAdminSnapshotPauseTimelineValid(row sqlc.Pause) bool {
	startedAt, started := tournamentAdminSnapshotRequiredTime(row.StartedAt)
	createdAt, created := tournamentAdminSnapshotRequiredTime(row.CreatedAt)
	updatedAt, updated := tournamentAdminSnapshotRequiredTime(row.UpdatedAt)
	resolvedAt, resolved := tournamentAdminSnapshotOptionalTime(row.ResolvedAt)
	if !started || !created || !updated || !resolved || createdAt.Before(startedAt) || updatedAt.Before(createdAt) ||
		resolvedAt != nil && (resolvedAt.Before(startedAt) || resolvedAt.After(updatedAt)) {
		return false
	}
	if row.State == string(gameusecase.PauseStateActive) {
		if resolvedAt != nil {
			return false
		}
	} else if (row.State != string(gameusecase.PauseStateResumed) &&
		row.State != string(gameusecase.PauseStateCancelled)) || resolvedAt == nil {
		return false
	}
	return true
}

func tournamentAdminSnapshotPauseReasonValid(reason string) bool {
	switch gameusecase.PauseReason(reason) {
	case gameusecase.PauseReasonOperator, gameusecase.PauseReasonDisconnect,
		gameusecase.PauseReasonPlatform, gameusecase.PauseReasonExecutionEpoch:
		return true
	default:
		return false
	}
}

//nolint:gocyclo // Scope validation deliberately audits every nullable identity axis in one boundary.
func tournamentAdminSnapshotPauseScopeValid(
	row sqlc.Pause,
	tournamentID uuid.UUID,
	waveIDs map[uuid.UUID]struct{},
	seriesGraph tournamentAdminSnapshotSeriesGraph,
) bool {
	switch row.ScopeKind {
	case "tournament":
		return row.ScopeID == tournamentID && !row.WaveID.Valid && !row.SeriesID.Valid && !row.GameAttemptID.Valid
	case "wave":
		_, exists := waveIDs[row.ScopeID]
		return exists && row.WaveID.Valid && row.WaveID.UUID == row.ScopeID &&
			!row.SeriesID.Valid && !row.GameAttemptID.Valid
	case "series":
		_, exists := seriesGraph.byID[row.ScopeID]
		return exists && !row.WaveID.Valid && row.SeriesID.Valid && row.SeriesID.UUID == row.ScopeID &&
			!row.GameAttemptID.Valid
	case "game_attempt":
		return !row.WaveID.Valid && row.SeriesID.Valid && row.GameAttemptID.Valid &&
			row.GameAttemptID.UUID == row.ScopeID && row.SeriesID.UUID == seriesGraph.gameSeriesByID[row.ScopeID] &&
			seriesGraph.gameRosterByID[row.ScopeID] != uuid.Nil
	default:
		return false
	}
}

func tournamentAdminSnapshotPauseOriginValid(kind, value string) bool {
	switch kind {
	case "tournament":
		state := domain.TournamentState(value)
		return state == domain.TournamentStateSwiss || state == domain.TournamentStateGolden ||
			state == domain.TournamentStatePlayoffs
	case "wave":
		state := domain.WaveState(value)
		return state.IsValid() && state != domain.WaveStatePaused
	case "series":
		state := domain.SeriesState(value)
		return state.IsValid() && state != domain.SeriesStateTechnicalPause && !state.IsTerminal()
	case "game_attempt":
		return domain.GameState(value) == domain.GameStateActive
	default:
		return false
	}
}

func tournamentAdminSnapshotPauseParentValid(row sqlc.Pause, rows map[uuid.UUID]sqlc.Pause) bool {
	if !row.ParentPauseID.Valid {
		return row.Depth == 0
	}
	parent, exists := rows[row.ParentPauseID.UUID]
	if !exists || !tournamentAdminSnapshotPauseParentIdentityValid(row, parent) {
		return false
	}
	startedAt, valid := tournamentAdminSnapshotRequiredTime(row.StartedAt)
	parentStartedAt, parentValid := tournamentAdminSnapshotRequiredTime(parent.StartedAt)
	if !valid || !parentValid || startedAt.Before(parentStartedAt) {
		return false
	}
	if parent.SeriesID.Valid && (!row.SeriesID.Valid || parent.SeriesID.UUID != row.SeriesID.UUID) {
		return false
	}
	return tournamentAdminSnapshotPauseEdgeValid(parent.ScopeKind, row.ScopeKind)
}

func tournamentAdminSnapshotPauseParentIdentityValid(row, parent sqlc.Pause) bool {
	return row.ParentPauseID.UUID != row.ID && row.Depth == parent.Depth+1 &&
		row.TournamentID == parent.TournamentID && row.RosterID == parent.RosterID
}

func tournamentAdminSnapshotPauseEdgeValid(parentKind, childKind string) bool {
	switch parentKind {
	case "tournament":
		return childKind == "wave" || childKind == "series"
	case "wave":
		return childKind == "series"
	case "series":
		return childKind == "game_attempt"
	default:
		return false
	}
}

func (index tournamentAdminSnapshotPauseIndex) normalRoot(
	state domain.TournamentState,
) (*sqlc.Pause, error) {
	if len(index.activeWaveRoots) > 1 || len(index.activeTournamentRoots) > 1 {
		return nil, domain.ErrInternal
	}
	if len(index.activeWaveRoots) == 1 {
		root := index.activeWaveRoots[0]
		if state != domain.TournamentStateTechnicalPause || len(index.activeTournamentRoots) != 0 ||
			!tournamentAdminSnapshotNormalPauseReason(root.Reason) {
			return nil, domain.ErrInternal
		}
		return &root, nil
	}
	if state == domain.TournamentStateTechnicalPause {
		if len(index.activeTournamentRoots) != 1 {
			return nil, domain.ErrInternal
		}
		return nil, nil
	}
	if len(index.activeTournamentRoots) != 0 {
		return nil, domain.ErrInternal
	}
	return nil, nil
}

func tournamentAdminSnapshotNormalPauseReason(reason string) bool {
	return reason == string(gameusecase.PauseReasonOperator) || reason == string(gameusecase.PauseReasonPlatform) ||
		reason == string(gameusecase.PauseReasonExecutionEpoch)
}

func tournamentAdminSnapshotWaveByID(
	waves []tournamentadmin.WaveView,
	id uuid.UUID,
) (tournamentadmin.WaveView, bool) {
	for _, wave := range waves {
		if wave.Wave.ID == id {
			return wave, true
		}
	}
	return tournamentadmin.WaveView{}, false
}

func tournamentAdminSnapshotRootMatchesWave(root sqlc.Pause, wave tournamentadmin.WaveView) bool {
	if !root.WaveID.Valid || root.ScopeID != wave.Wave.ID || root.ParentPauseID.Valid || root.Depth != 0 ||
		root.State != string(gameusecase.PauseStateActive) {
		return false
	}
	startedAt, valid := tournamentAdminSnapshotRequiredTime(root.StartedAt)
	if !valid {
		return false
	}
	origin := domain.WaveState(root.PausedFromState)
	switch origin {
	case domain.WaveStateActive:
		return wave.Wave.State == domain.WaveStatePaused && wave.Wave.PausedAt != nil &&
			wave.Wave.PausedAt.Equal(startedAt)
	case domain.WaveStateReadyWindowOpen, domain.WaveStateReady:
		return wave.Wave.State == origin && wave.Wave.ReadyWindow != nil &&
			wave.Wave.ReadyWindow.Deadline.After(startedAt)
	case domain.WaveStatePlanned, domain.WaveStateCompleted,
		domain.WaveStateReadyWindowExpired, domain.WaveStateSuperseded:
		return wave.Wave.State == origin
	case domain.WaveStatePaused:
		return false
	default:
		return false
	}
}

//nolint:gocyclo // One bounded pass keeps each Series, current Game, and active pause edge atomic.
func tournamentAdminSnapshotPauseExecutions(
	root sqlc.Pause,
	wave tournamentadmin.WaveView,
	seriesGraph tournamentAdminSnapshotSeriesGraph,
	pauseIndex tournamentAdminSnapshotPauseIndex,
) ([]gameusecase.PauseSeries, []gameusecase.PauseGame, map[uuid.UUID]struct{}, error) {
	seriesIDs := make(map[uuid.UUID]struct{}, len(wave.SeriesIDs)/2)
	for _, seriesID := range wave.SeriesIDs {
		seriesIDs[seriesID] = struct{}{}
	}
	if len(seriesIDs) == 0 {
		return nil, nil, nil, domain.ErrInternal
	}
	seriesValues := make([]gameusecase.PauseSeries, 0, len(seriesIDs))
	games := make([]gameusecase.PauseGame, 0, len(seriesIDs))
	for _, series := range seriesGraph.values {
		if _, selected := seriesIDs[series.ID]; !selected {
			continue
		}
		seriesPause, hasSeriesPause := pauseIndex.activeByScope[tournamentAdminSnapshotPauseScope{
			kind: "series",
			id:   series.ID,
		}]
		execution := seriesdomain.Execution{Series: series}
		if series.State == domain.SeriesStateTechnicalPause {
			if !hasSeriesPause || !tournamentAdminSnapshotParallelSeriesPause(seriesPause, root) {
				return nil, nil, nil, domain.ErrInternal
			}
			resumeState := domain.SeriesState(seriesPause.PausedFromState)
			execution.ResumeState = &resumeState
		} else if hasSeriesPause && pauseIndex.descendsFrom(seriesPause.ID, root.ID) {
			return nil, nil, nil, domain.ErrInternal
		}
		if execution.Validate() != nil {
			return nil, nil, nil, domain.ErrInternal
		}

		pauseSeries := gameusecase.PauseSeries{
			Execution: execution,
			Revision:  seriesGraph.revisionByID[series.ID],
		}
		game, hasGame := tournamentAdminSnapshotCurrentGame(series)
		if hasGame {
			gameID := game.ID
			pauseSeries.CurrentGameID = &gameID
			pauseGame, err := tournamentAdminSnapshotPauseGame(
				*game,
				series.ID,
				execution,
				root,
				seriesGraph,
				pauseIndex,
			)
			if err != nil {
				return nil, nil, nil, err
			}
			games = append(games, pauseGame)
		} else if execution.ResumeState != nil && *execution.ResumeState == domain.SeriesStateActive {
			return nil, nil, nil, domain.ErrInternal
		}
		seriesValues = append(seriesValues, pauseSeries)
	}
	if len(seriesValues) != len(seriesIDs) {
		return nil, nil, nil, domain.ErrInternal
	}
	return seriesValues, games, seriesIDs, nil
}

//nolint:gocyclo // Snapshot recovery validates Game state, pause ancestry, and clock evidence together.
func tournamentAdminSnapshotPauseGame(
	game domain.Game,
	seriesID uuid.UUID,
	execution seriesdomain.Execution,
	root sqlc.Pause,
	seriesGraph tournamentAdminSnapshotSeriesGraph,
	pauseIndex tournamentAdminSnapshotPauseIndex,
) (gameusecase.PauseGame, error) {
	value := gameusecase.PauseGame{
		SeriesID: seriesID,
		Game:     game,
		Revision: seriesGraph.gameRevisionByID[game.ID],
	}
	gamePause, paused := pauseIndex.activeByScope[tournamentAdminSnapshotPauseScope{kind: "game_attempt", id: game.ID}]
	belongsToNormalPause := paused && (pauseIndex.descendsFrom(gamePause.ID, root.ID) ||
		tournamentAdminSnapshotParallelGamePause(gamePause, root, pauseIndex))
	if game.State == domain.GameStatePaused {
		if !paused || execution.ResumeState == nil || *execution.ResumeState != domain.SeriesStateActive ||
			gamePause.PausedFromState != string(domain.GameStateActive) ||
			(!belongsToNormalPause && gamePause.Reason != string(gameusecase.PauseReasonDisconnect)) {
			return gameusecase.PauseGame{}, domain.ErrInternal
		}
		resumeState := domain.GameStateActive
		value.ResumeState = &resumeState
	} else {
		if belongsToNormalPause {
			return gameusecase.PauseGame{}, domain.ErrInternal
		}
		if game.State == domain.GameStateActive ||
			execution.ResumeState != nil && *execution.ResumeState == domain.SeriesStateActive {
			return gameusecase.PauseGame{}, domain.ErrInternal
		}
	}
	if value.Revision < 1 || value.Game.Validate() != nil {
		return gameusecase.PauseGame{}, domain.ErrInternal
	}
	return value, nil
}

func tournamentAdminSnapshotParallelSeriesPause(seriesPause, root sqlc.Pause) bool {
	seriesStartedAt, seriesStarted := tournamentAdminSnapshotRequiredTime(seriesPause.StartedAt)
	rootStartedAt, rootStarted := tournamentAdminSnapshotRequiredTime(root.StartedAt)
	return seriesStarted && rootStarted && !seriesPause.ParentPauseID.Valid && seriesPause.Depth == 0 &&
		seriesPause.Reason == root.Reason && tournamentAdminSnapshotNormalPauseReason(seriesPause.Reason) &&
		seriesStartedAt.Equal(rootStartedAt)
}

func tournamentAdminSnapshotParallelGamePause(
	gamePause, root sqlc.Pause,
	pauseIndex tournamentAdminSnapshotPauseIndex,
) bool {
	if !gamePause.ParentPauseID.Valid || gamePause.Depth != 1 || gamePause.Reason != root.Reason {
		return false
	}
	seriesPause, exists := pauseIndex.byID[gamePause.ParentPauseID.UUID]
	if !exists || seriesPause.ScopeKind != "series" || !seriesPause.SeriesID.Valid ||
		!gamePause.SeriesID.Valid || seriesPause.SeriesID.UUID != gamePause.SeriesID.UUID ||
		!tournamentAdminSnapshotParallelSeriesPause(seriesPause, root) {
		return false
	}
	gameStartedAt, gameStarted := tournamentAdminSnapshotRequiredTime(gamePause.StartedAt)
	seriesStartedAt, seriesStarted := tournamentAdminSnapshotRequiredTime(seriesPause.StartedAt)
	return gameStarted && seriesStarted && gameStartedAt.Equal(seriesStartedAt)
}

func (index tournamentAdminSnapshotPauseIndex) descendsFrom(id, rootID uuid.UUID) bool {
	seen := make(map[uuid.UUID]struct{}, 4)
	for id != uuid.Nil {
		if id == rootID {
			return true
		}
		if _, duplicate := seen[id]; duplicate {
			return false
		}
		seen[id] = struct{}{}
		row, exists := index.byID[id]
		if !exists || !row.ParentPauseID.Valid {
			return false
		}
		id = row.ParentPauseID.UUID
	}
	return false
}

func tournamentAdminSnapshotPauseTournament(
	header tournamentAdminSnapshotHeaderState,
) gameusecase.TournamentRecord {
	view := header.tournament
	return gameusecase.TournamentRecord{
		ID:              view.ID,
		RosterID:        view.RosterID,
		Preset:          view.Preset,
		State:           view.State,
		PausedFromState: view.PausedFromState,
		Revision:        view.Revision,
		RosterSize:      view.RosterSize,
		CreatedAt:       view.CreatedAt,
		UpdatedAt:       view.UpdatedAt,
		StartedAt:       view.StartedAt,
		FinishedAt:      view.FinishedAt,
	}
}

func tournamentAdminSnapshotSeriesNeedsDraft(values []gameusecase.PauseSeries) bool {
	for _, value := range values {
		if value.Execution.ResumeState != nil && *value.Execution.ResumeState == domain.SeriesStateDraft {
			return true
		}
	}
	return false
}

//nolint:gocyclo // Draft recovery accepts both persisted pre-start topologies and participant orderings.
func tournamentAdminSnapshotDraftMatchesSeries(
	draft draftusecase.Execution,
	values []gameusecase.PauseSeries,
) bool {
	if draft.Validate() != nil || draft.State != draftusecase.ExecutionStatePaused || draft.Recovery == nil ||
		draft.Recovery.Reason != draftusecase.RecoveryReasonOperatorPause ||
		draft.Recovery.Policy != draftusecase.RecoveryPolicyShiftRemaining {
		return false
	}
	for _, value := range values {
		series := value.Execution.Series
		if series.ID != draft.SeriesID {
			continue
		}
		participantsMatch := series.FirstParticipantID == draft.FirstParticipantID &&
			series.SecondParticipantID == draft.SecondParticipantID ||
			series.FirstParticipantID == draft.SecondParticipantID &&
				series.SecondParticipantID == draft.FirstParticipantID
		seriesStateMatches := series.State == domain.SeriesStatePlanned ||
			series.State == domain.SeriesStateTechnicalPause && value.Execution.ResumeState != nil &&
				*value.Execution.ResumeState == domain.SeriesStateDraft
		return participantsMatch && seriesStateMatches && series.Format == draft.Format
	}
	return false
}

func tournamentAdminSnapshotPresenceRows(
	rows []sqlc.PresenceState,
	seriesIDs map[uuid.UUID]struct{},
) []sqlc.PresenceState {
	result := make([]sqlc.PresenceState, 0, len(rows))
	for _, row := range rows {
		if _, selected := seriesIDs[row.SeriesID]; selected {
			result = append(result, row)
		}
	}
	return result
}

func tournamentAdminSnapshotReconnectRows(
	rows []sqlc.ReconnectInterval,
	seriesIDs map[uuid.UUID]struct{},
) []sqlc.ReconnectInterval {
	result := make([]sqlc.ReconnectInterval, 0, len(rows))
	for _, row := range rows {
		if _, selected := seriesIDs[row.SeriesID]; selected {
			result = append(result, row)
		}
	}
	return result
}

func tournamentAdminSnapshotCounterRows(
	rows []sqlc.ReconnectSlotCounter,
	seriesIDs map[uuid.UUID]struct{},
	pauseIndex tournamentAdminSnapshotPauseIndex,
) ([]sqlc.ReconnectSlotCounter, bool) {
	result := make([]sqlc.ReconnectSlotCounter, 0, len(rows))
	for _, row := range rows {
		pauseRow, exists := pauseIndex.byID[row.PauseID]
		if !exists || pauseRow.ScopeKind != "game_attempt" || !pauseRow.SeriesID.Valid {
			return nil, false
		}
		if _, selected := seriesIDs[pauseRow.SeriesID.UUID]; !selected {
			continue
		}
		if !tournamentAdminSnapshotStoredTimeline(row.CreatedAt, row.UpdatedAt) {
			return nil, false
		}
		result = append(result, row)
	}
	return result, true
}

type tournamentAdminSnapshotCounterKey struct {
	pauseID       uuid.UUID
	rosterID      uuid.UUID
	participantID uuid.UUID
}

type tournamentAdminSnapshotReconnectSegment struct {
	number       int
	continuation int
}

func tournamentAdminSnapshotConnectivityValid(
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	seriesIDs map[uuid.UUID]struct{},
	seriesGraph tournamentAdminSnapshotSeriesGraph,
	pauseIndex tournamentAdminSnapshotPauseIndex,
	root sqlc.Pause,
	presence []pausedomain.PausePresence,
	reconnect []pausedomain.PauseReconnectInterval,
	counters []pausedomain.PauseReconnectCounter,
) bool {
	participantSeries, ok := tournamentAdminSnapshotParticipantSeries(seriesIDs, seriesGraph)
	if !ok {
		return false
	}
	presenceByParticipant, ok := tournamentAdminSnapshotPresenceIndex(
		tournamentID,
		rosterID,
		participantSeries,
		presence,
	)
	if !ok {
		return false
	}
	counterByKey, ok := tournamentAdminSnapshotCounterIndex(
		rosterID,
		participantSeries,
		pauseIndex,
		counters,
	)
	if !ok {
		return false
	}
	rootCounts, ok := tournamentAdminSnapshotReconnectIndex(
		rosterID,
		participantSeries,
		presenceByParticipant,
		counterByKey,
		seriesGraph,
		pauseIndex,
		reconnect,
	)
	if !ok || pausedomain.ValidateReconnectLineage(reconnect) != nil ||
		!tournamentAdminSnapshotReconnectSetPaused(reconnect, root) {
		return false
	}
	for key, counter := range counterByKey {
		if rootCounts[key] != counter.Used {
			return false
		}
	}
	return true
}

func tournamentAdminSnapshotReconnectSetPaused(
	values []pausedomain.PauseReconnectInterval,
	root sqlc.Pause,
) bool {
	pausedAt, valid := tournamentAdminSnapshotRequiredTime(root.StartedAt)
	if !valid {
		return false
	}
	for _, value := range values {
		if value.State == pausedomain.ReconnectStateOpen {
			return false
		}
		if value.SuspendedByPauseID == nil || *value.SuspendedByPauseID != root.ID {
			continue
		}
		if value.State != pausedomain.ReconnectStateCancelled || value.ClosedAt == nil ||
			!value.ClosedAt.Equal(pausedAt) || !value.UpdatedAt.Equal(pausedAt) {
			return false
		}
	}
	return true
}

func tournamentAdminSnapshotParticipantSeries(
	seriesIDs map[uuid.UUID]struct{},
	seriesGraph tournamentAdminSnapshotSeriesGraph,
) (map[uuid.UUID]uuid.UUID, bool) {
	participantSeries := make(map[uuid.UUID]uuid.UUID, len(seriesIDs)*2)
	for seriesID := range seriesIDs {
		series, exists := seriesGraph.byID[seriesID]
		if !exists {
			return nil, false
		}
		for _, participantID := range []uuid.UUID{series.FirstParticipantID, series.SecondParticipantID} {
			if _, duplicate := participantSeries[participantID]; duplicate {
				return nil, false
			}
			participantSeries[participantID] = seriesID
		}
	}
	return participantSeries, true
}

func tournamentAdminSnapshotPresenceIndex(
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	participantSeries map[uuid.UUID]uuid.UUID,
	presence []pausedomain.PausePresence,
) (map[uuid.UUID]pausedomain.PausePresence, bool) {
	presenceByParticipant := make(map[uuid.UUID]pausedomain.PausePresence, len(presence))
	presenceIDs := make(map[uuid.UUID]struct{}, len(presence))
	for _, value := range presence {
		if value.Validate() != nil || value.TournamentID != tournamentID || value.RosterID != rosterID ||
			participantSeries[value.ParticipantID] != value.SeriesID {
			return nil, false
		}
		if _, duplicate := presenceByParticipant[value.ParticipantID]; duplicate {
			return nil, false
		}
		if _, duplicate := presenceIDs[value.ID]; duplicate {
			return nil, false
		}
		presenceByParticipant[value.ParticipantID] = value
		presenceIDs[value.ID] = struct{}{}
	}
	if len(presenceByParticipant) != len(participantSeries) {
		return nil, false
	}
	return presenceByParticipant, true
}

func tournamentAdminSnapshotCounterIndex(
	rosterID uuid.UUID,
	participantSeries map[uuid.UUID]uuid.UUID,
	pauseIndex tournamentAdminSnapshotPauseIndex,
	counters []pausedomain.PauseReconnectCounter,
) (map[tournamentAdminSnapshotCounterKey]pausedomain.PauseReconnectCounter, bool) {
	counterByKey := make(map[tournamentAdminSnapshotCounterKey]pausedomain.PauseReconnectCounter, len(counters))
	for _, counter := range counters {
		key := tournamentAdminSnapshotCounterKey{counter.PauseID, counter.RosterID, counter.ParticipantID}
		pauseRow, exists := pauseIndex.byID[counter.PauseID]
		if counter.Validate() != nil || counter.RosterID != rosterID || !exists ||
			!pauseRow.SeriesID.Valid || participantSeries[counter.ParticipantID] != pauseRow.SeriesID.UUID {
			return nil, false
		}
		if _, duplicate := counterByKey[key]; duplicate {
			return nil, false
		}
		counterByKey[key] = counter
	}
	return counterByKey, true
}

func tournamentAdminSnapshotReconnectIndex(
	rosterID uuid.UUID,
	participantSeries map[uuid.UUID]uuid.UUID,
	presenceByParticipant map[uuid.UUID]pausedomain.PausePresence,
	counterByKey map[tournamentAdminSnapshotCounterKey]pausedomain.PauseReconnectCounter,
	seriesGraph tournamentAdminSnapshotSeriesGraph,
	pauseIndex tournamentAdminSnapshotPauseIndex,
	reconnect []pausedomain.PauseReconnectInterval,
) (map[tournamentAdminSnapshotCounterKey]int, bool) {
	rootCounts := make(map[tournamentAdminSnapshotCounterKey]int, len(counterByKey))
	seenIntervals := make(map[uuid.UUID]struct{}, len(reconnect))
	seenSegments := make(map[tournamentAdminSnapshotCounterKey]map[tournamentAdminSnapshotReconnectSegment]struct{})
	for _, interval := range reconnect {
		key := tournamentAdminSnapshotCounterKey{interval.PauseID, interval.RosterID, interval.ParticipantID}
		if !tournamentAdminSnapshotReconnectMembershipValid(
			interval,
			rosterID,
			participantSeries,
			presenceByParticipant,
			counterByKey,
			seriesGraph,
			pauseIndex,
		) {
			return nil, false
		}
		if _, duplicate := seenIntervals[interval.ID]; duplicate {
			return nil, false
		}
		seenIntervals[interval.ID] = struct{}{}
		if seenSegments[key] == nil {
			seenSegments[key] = make(map[tournamentAdminSnapshotReconnectSegment]struct{})
		}
		segment := tournamentAdminSnapshotReconnectSegment{interval.Number, interval.ContinuationNumber}
		if _, duplicate := seenSegments[key][segment]; duplicate {
			return nil, false
		}
		seenSegments[key][segment] = struct{}{}
		if interval.ContinuationNumber == 0 {
			rootCounts[key]++
		}
	}
	return rootCounts, true
}

func tournamentAdminSnapshotReconnectMembershipValid(
	interval pausedomain.PauseReconnectInterval,
	rosterID uuid.UUID,
	participantSeries map[uuid.UUID]uuid.UUID,
	presenceByParticipant map[uuid.UUID]pausedomain.PausePresence,
	counterByKey map[tournamentAdminSnapshotCounterKey]pausedomain.PauseReconnectCounter,
	seriesGraph tournamentAdminSnapshotSeriesGraph,
	pauseIndex tournamentAdminSnapshotPauseIndex,
) bool {
	key := tournamentAdminSnapshotCounterKey{interval.PauseID, interval.RosterID, interval.ParticipantID}
	counter, hasCounter := counterByKey[key]
	currentPresence, hasPresence := presenceByParticipant[interval.ParticipantID]
	pauseRow, hasPause := pauseIndex.byID[interval.PauseID]
	return interval.Validate() == nil && interval.RosterID == rosterID && hasCounter && hasPresence && hasPause &&
		participantSeries[interval.ParticipantID] == interval.SeriesID &&
		currentPresence.PresenceEpoch >= interval.PresenceEpoch && interval.Number <= counter.Used &&
		pauseRow.ScopeKind == "game_attempt" && pauseRow.SeriesID.Valid && pauseRow.GameAttemptID.Valid &&
		pauseRow.SeriesID.UUID == interval.SeriesID && pauseRow.GameAttemptID.UUID == interval.GameID &&
		seriesGraph.gameSeriesByID[interval.GameID] == interval.SeriesID
}

func tournamentAdminSnapshotFrozenDeadlines(
	root sqlc.Pause,
	wave tournamentadmin.WaveView,
	games []gameusecase.PauseGame,
	draft *draftusecase.Execution,
	clockRows []sqlc.PauseClock,
	pauseIndex tournamentAdminSnapshotPauseIndex,
) ([]gameusecase.PauseFrozenDeadline, error) {
	pausedAt, valid := tournamentAdminSnapshotRequiredTime(root.StartedAt)
	if !valid {
		return nil, domain.ErrInternal
	}
	result := make([]gameusecase.PauseFrozenDeadline, 0, len(games)+2)
	ready, exists, err := tournamentAdminSnapshotReadyWindowDeadline(wave, pausedAt)
	if err != nil {
		return nil, err
	}
	if exists {
		result = append(result, ready)
	}
	gameDeadlines, err := tournamentAdminSnapshotGameDeadlines(games, clockRows, pauseIndex, pausedAt)
	if err != nil {
		return nil, err
	}
	result = append(result, gameDeadlines...)
	draftDeadline, exists, err := tournamentAdminSnapshotDraftDeadline(draft, pausedAt)
	if err != nil {
		return nil, err
	}
	if exists {
		result = append(result, draftDeadline)
	}
	return result, nil
}

func tournamentAdminSnapshotReadyWindowDeadline(
	wave tournamentadmin.WaveView,
	pausedAt time.Time,
) (gameusecase.PauseFrozenDeadline, bool, error) {
	if wave.Wave.State != domain.WaveStateReadyWindowOpen && wave.Wave.State != domain.WaveStateReady {
		return gameusecase.PauseFrozenDeadline{}, false, nil
	}
	window := wave.Wave.ReadyWindow
	if window == nil {
		return gameusecase.PauseFrozenDeadline{}, false, domain.ErrInternal
	}
	frozen, ok := tournamentAdminSnapshotFrozenDeadline(
		gameusecase.PauseDeadlineReadyWindow,
		window.ID,
		window.Deadline,
		pausedAt,
		1,
	)
	if !ok {
		return gameusecase.PauseFrozenDeadline{}, false, domain.ErrInternal
	}
	return frozen, true, nil
}

func tournamentAdminSnapshotGameDeadlines(
	games []gameusecase.PauseGame,
	clockRows []sqlc.PauseClock,
	pauseIndex tournamentAdminSnapshotPauseIndex,
	pausedAt time.Time,
) ([]gameusecase.PauseFrozenDeadline, error) {
	result := make([]gameusecase.PauseFrozenDeadline, 0, len(games))
	for _, game := range games {
		if game.Game.State != domain.GameStatePaused || game.ResumeState == nil || *game.ResumeState != domain.GameStateActive {
			continue
		}
		gamePause, exists := pauseIndex.activeByScope[tournamentAdminSnapshotPauseScope{
			kind: "game_attempt",
			id:   game.Game.ID,
		}]
		if !exists {
			return nil, domain.ErrInternal
		}
		clock, found, err := tournamentAdminSnapshotActiveGameClock(clockRows, gamePause, game.Game.ID, pausedAt)
		if err != nil || !found {
			return nil, domain.ErrInternal
		}
		result = append(result, gameusecase.PauseFrozenDeadline{
			Kind:             gameusecase.PauseDeadlineGame,
			OwnerID:          game.Game.ID,
			OriginalDeadline: clock.OriginalDeadline,
			FrozenAt:         clock.FrozenAt,
			Remaining:        clock.Remaining,
			Revision:         clock.Revision,
		})
	}
	return result, nil
}

func tournamentAdminSnapshotDraftDeadline(
	draft *draftusecase.Execution,
	pausedAt time.Time,
) (gameusecase.PauseFrozenDeadline, bool, error) {
	if draft == nil {
		return gameusecase.PauseFrozenDeadline{}, false, nil
	}
	if draft.Recovery == nil || !draft.Recovery.RecordedAt.Equal(pausedAt) ||
		draft.PausedRemaining <= 0 || draft.PausedRemaining > tournamentAdminSnapshotMaxFrozenDuration {
		return gameusecase.PauseFrozenDeadline{}, false, domain.ErrInternal
	}
	frozen, ok := tournamentAdminSnapshotFrozenDeadline(
		gameusecase.PauseDeadlineDraft,
		draft.ID,
		draft.Recovery.PreviousDeadline,
		pausedAt,
		1,
	)
	if !ok || frozen.Remaining != draft.PausedRemaining {
		return gameusecase.PauseFrozenDeadline{}, false, domain.ErrInternal
	}
	return frozen, true, nil
}

func tournamentAdminSnapshotActiveGameClock(
	rows []sqlc.PauseClock,
	pauseRow sqlc.Pause,
	gameID uuid.UUID,
	pausedAt time.Time,
) (pausedomain.PauseResumeGameClock, bool, error) {
	var result pausedomain.PauseResumeGameClock
	found := false
	for _, row := range rows {
		if row.PauseID != pauseRow.ID {
			continue
		}
		if found || row.GameAttemptID != gameID || !tournamentAdminSnapshotStoredTimeline(row.CreatedAt, row.UpdatedAt) {
			return pausedomain.PauseResumeGameClock{}, false, domain.ErrInternal
		}
		clock, err := recoveryGameClock(row)
		if err != nil || !clock.FrozenAt.Equal(pausedAt) || clock.Remaining > tournamentAdminSnapshotMaxFrozenDuration {
			return pausedomain.PauseResumeGameClock{}, false, domain.ErrInternal
		}
		result = clock
		found = true
	}
	return result, found, nil
}

func tournamentAdminSnapshotFrozenDeadline(
	kind gameusecase.PauseDeadlineKind,
	ownerID uuid.UUID,
	deadline time.Time,
	frozenAt time.Time,
	revision int64,
) (gameusecase.PauseFrozenDeadline, bool) {
	if ownerID == uuid.Nil || revision < 1 || !domain.IsValidServerTime(deadline) ||
		!domain.IsValidServerTime(frozenAt) || !deadline.After(frozenAt) {
		return gameusecase.PauseFrozenDeadline{}, false
	}
	remaining := deadline.Sub(frozenAt)
	if remaining <= 0 || remaining > tournamentAdminSnapshotMaxFrozenDuration {
		return gameusecase.PauseFrozenDeadline{}, false
	}
	return gameusecase.PauseFrozenDeadline{
		Kind:             kind,
		OwnerID:          ownerID,
		OriginalDeadline: deadline,
		FrozenAt:         frozenAt,
		Remaining:        remaining,
		Revision:         revision,
	}, true
}

func tournamentAdminSnapshotPauseGraphValid(
	view tournamentadmin.PauseGraphView,
	root sqlc.Pause,
	seriesIDs map[uuid.UUID]struct{},
	pauseIndex tournamentAdminSnapshotPauseIndex,
) bool {
	graph := view.Graph
	if !tournamentAdminSnapshotPauseGraphRootValid(view, root, len(seriesIDs)) {
		return false
	}
	gameByID, valid := tournamentAdminSnapshotPauseGameIndex(graph.Games)
	if !valid || !tournamentAdminSnapshotPauseSeriesSetValid(graph.Series, seriesIDs, gameByID) {
		return false
	}
	return tournamentAdminSnapshotDraftGraphValid(graph.Draft, graph.Series) &&
		tournamentAdminSnapshotFrozenSetValid(graph) &&
		tournamentAdminSnapshotPausedGameLinksValid(graph.Games, pauseIndex)
}

//nolint:gocyclo // Root validation binds all persisted aggregate identities and timeline evidence at one boundary.
func tournamentAdminSnapshotPauseGraphRootValid(
	view tournamentadmin.PauseGraphView,
	root sqlc.Pause,
	seriesCount int,
) bool {
	graph := view.Graph
	rootStartedAt, valid := tournamentAdminSnapshotRequiredTime(root.StartedAt)
	return graph.Scope.Validate() == nil && graph.Revision == root.Revision && graph.Revision >= 1 &&
		graph.ActivePauseID == root.ID && graph.PausedAt != nil && graph.PausedAt.Equal(rootStartedAt) && valid &&
		graph.DeadlinesSuppressed && graph.TerminalActionRevision >= 0 &&
		graph.Tournament.ID == graph.Scope.TournamentID && graph.Tournament.RosterID == graph.Scope.RosterID &&
		graph.Tournament.State == domain.TournamentStateTechnicalPause && graph.Tournament.PausedFromState != nil &&
		graph.Tournament.Revision >= 1 && graph.Tournament.RosterSize >= 0 &&
		domain.IsValidServerTime(graph.Tournament.CreatedAt) && domain.IsValidServerTime(graph.Tournament.UpdatedAt) &&
		!graph.Tournament.UpdatedAt.Before(graph.Tournament.CreatedAt) &&
		graph.Wave.Wave.ID == graph.Scope.WaveID && graph.Wave.Revision == view.Wave.Revision &&
		reflect.DeepEqual(graph.Wave.Wave, view.Wave.Wave) &&
		tournamentAdminSnapshotWaveViewValid(view.Wave, graph.Scope.TournamentID) && len(graph.Series) == seriesCount
}

func tournamentAdminSnapshotPauseGameIndex(
	games []gameusecase.PauseGame,
) (map[uuid.UUID]gameusecase.PauseGame, bool) {
	gameByID := make(map[uuid.UUID]gameusecase.PauseGame, len(games))
	for _, game := range games {
		if game.Game.ID == uuid.Nil || game.Revision < 1 || game.Game.Validate() != nil {
			return nil, false
		}
		if _, duplicate := gameByID[game.Game.ID]; duplicate {
			return nil, false
		}
		gameByID[game.Game.ID] = game
	}
	return gameByID, true
}

func tournamentAdminSnapshotPauseSeriesSetValid(
	seriesValues []gameusecase.PauseSeries,
	seriesIDs map[uuid.UUID]struct{},
	gameByID map[uuid.UUID]gameusecase.PauseGame,
) bool {
	seenSeries := make(map[uuid.UUID]struct{}, len(seriesValues))
	for _, series := range seriesValues {
		id := series.Execution.Series.ID
		if _, selected := seriesIDs[id]; !selected || series.Revision < 1 || series.Execution.Validate() != nil {
			return false
		}
		if _, duplicate := seenSeries[id]; duplicate {
			return false
		}
		seenSeries[id] = struct{}{}
		current, hasCurrent := tournamentAdminSnapshotCurrentGame(series.Execution.Series)
		if hasCurrent != (series.CurrentGameID != nil) {
			return false
		}
		if hasCurrent {
			game, exists := gameByID[current.ID]
			if !exists || *series.CurrentGameID != current.ID || game.SeriesID != id || !reflect.DeepEqual(game.Game, *current) {
				return false
			}
		}
	}
	return len(seenSeries) == len(seriesIDs) && len(gameByID) == tournamentAdminSnapshotCurrentGameCount(seriesValues)
}

func tournamentAdminSnapshotCurrentGameCount(seriesValues []gameusecase.PauseSeries) int {
	count := 0
	for _, series := range seriesValues {
		if series.CurrentGameID != nil {
			count++
		}
	}
	return count
}

func tournamentAdminSnapshotPausedGameLinksValid(
	games []gameusecase.PauseGame,
	pauseIndex tournamentAdminSnapshotPauseIndex,
) bool {
	for _, game := range games {
		if game.Game.State != domain.GameStatePaused {
			continue
		}
		pauseRow, exists := pauseIndex.activeByScope[tournamentAdminSnapshotPauseScope{kind: "game_attempt", id: game.Game.ID}]
		if !exists || pauseRow.PausedFromState != string(domain.GameStateActive) {
			return false
		}
	}
	return true
}

func tournamentAdminSnapshotDraftGraphValid(
	draft *draftusecase.Execution,
	seriesValues []gameusecase.PauseSeries,
) bool {
	if draft == nil {
		return !tournamentAdminSnapshotSeriesNeedsDraft(seriesValues)
	}
	return tournamentAdminSnapshotDraftMatchesSeries(*draft, seriesValues)
}

type tournamentAdminSnapshotDeadlineKey struct {
	kind    gameusecase.PauseDeadlineKind
	ownerID uuid.UUID
}

func tournamentAdminSnapshotFrozenSetValid(graph gameusecase.PauseGraph) bool {
	expected, valid := tournamentAdminSnapshotExpectedDeadlines(graph)
	if !valid {
		return false
	}
	seen := make(map[tournamentAdminSnapshotDeadlineKey]struct{}, len(graph.FrozenDeadlines))
	for _, frozen := range graph.FrozenDeadlines {
		key := tournamentAdminSnapshotDeadlineKey{frozen.Kind, frozen.OwnerID}
		deadline, exists := expected[key]
		if !exists || !tournamentAdminSnapshotFrozenEntryValid(frozen, graph.PausedAt, deadline) {
			return false
		}
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}
	return len(seen) == len(expected)
}

func tournamentAdminSnapshotExpectedDeadlines(
	graph gameusecase.PauseGraph,
) (map[tournamentAdminSnapshotDeadlineKey]time.Time, bool) {
	expected := make(map[tournamentAdminSnapshotDeadlineKey]time.Time)
	if graph.Wave.Wave.State == domain.WaveStateReadyWindowOpen || graph.Wave.Wave.State == domain.WaveStateReady {
		if graph.Wave.Wave.ReadyWindow == nil {
			return nil, false
		}
		window := graph.Wave.Wave.ReadyWindow
		expected[tournamentAdminSnapshotDeadlineKey{gameusecase.PauseDeadlineReadyWindow, window.ID}] = window.Deadline
	}
	for _, game := range graph.Games {
		if game.Game.State == domain.GameStatePaused && game.ResumeState != nil && *game.ResumeState == domain.GameStateActive {
			expected[tournamentAdminSnapshotDeadlineKey{gameusecase.PauseDeadlineGame, game.Game.ID}] = time.Time{}
		}
	}
	if graph.Draft != nil && graph.Draft.State == draftusecase.ExecutionStatePaused && graph.Draft.Recovery != nil {
		expected[tournamentAdminSnapshotDeadlineKey{gameusecase.PauseDeadlineDraft, graph.Draft.ID}] = graph.Draft.Recovery.PreviousDeadline
	}
	return expected, true
}

func tournamentAdminSnapshotFrozenEntryValid(
	frozen gameusecase.PauseFrozenDeadline,
	pausedAt *time.Time,
	expectedDeadline time.Time,
) bool {
	return frozen.Revision >= 1 && frozen.ResumedAt == nil && frozen.ResumedDeadline == nil && pausedAt != nil &&
		frozen.FrozenAt.Equal(*pausedAt) && frozen.Remaining > 0 &&
		frozen.Remaining <= tournamentAdminSnapshotMaxFrozenDuration &&
		frozen.OriginalDeadline.Equal(frozen.FrozenAt.Add(frozen.Remaining)) &&
		(expectedDeadline.IsZero() || frozen.OriginalDeadline.Equal(expectedDeadline))
}
