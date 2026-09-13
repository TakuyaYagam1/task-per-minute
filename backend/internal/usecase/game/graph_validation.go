package game

import (
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func validateTournamentRecordPointer(record *TournamentRecord, tournamentID uuid.UUID) error {
	if !validPauseTournamentRecordHeader(record, tournamentID) {
		return domain.ErrInternal
	}
	if err := (domain.Tournament{
		State: record.State, PausedFromState: record.PausedFromState,
	}).Validate(); err != nil {
		return domain.ErrInternal
	}
	for _, timestamp := range []*time.Time{record.StartedAt, record.FinishedAt} {
		if timestamp != nil && !pauseValidServerTime(*timestamp) {
			return domain.ErrInternal
		}
	}
	return nil
}

func validPauseTournamentRecordHeader(record *TournamentRecord, tournamentID uuid.UUID) bool {
	return record != nil && record.ID == tournamentID && record.ID != uuid.Nil &&
		record.RosterID != uuid.Nil && record.Preset == domain.TournamentPresetV1 &&
		record.Revision >= 1 && record.RosterSize >= 0 && record.RosterSize <= domain.TournamentMaxParticipants &&
		pauseValidServerTime(record.CreatedAt) && pauseValidServerTime(record.UpdatedAt) &&
		!record.UpdatedAt.Before(record.CreatedAt)
}

func validateNormalPauseRecord(record NormalPauseRecord) error {
	if !validNormalPauseRecordHeader(record) || !validNormalPauseRecordGraphLink(record) {
		return normalPauseError("invalid active pause record")
	}
	if err := validatePauseGraph(record.Graph, true); err != nil {
		return err
	}
	return nil
}

func validNormalPauseRecordHeader(record NormalPauseRecord) bool {
	return validPauseGraphScope(record.Scope) && record.CommandID != uuid.Nil && record.PauseID != uuid.Nil &&
		record.ScopeKind == pausedomain.ScopeWave && record.ScopeID == record.Scope.WaveID &&
		record.ActorID != uuid.Nil && record.Reason.allowsNormalPause() && record.State == PauseStateActive &&
		record.CommandID != record.PauseID && record.CommandID != record.ActorID && record.PauseID != record.ActorID &&
		record.Revision == 1 && pauseValidServerTime(record.PausedAt) && record.ResolvedAt == nil &&
		validDraftResultRevisionIdentity(record.Expected.Draft, record.Expected.DraftPreviousRevisionID, record.DraftResultRevisionID,
			record.CommandID, record.PauseID, record.ActorID)
}

func validNormalPauseRecordGraphLink(record NormalPauseRecord) bool {
	return record.Graph.Scope == record.Scope && record.Graph.ActivePauseID == record.PauseID &&
		record.Graph.PausedAt != nil && record.Graph.PausedAt.Equal(record.PausedAt) &&
		record.Graph.DeadlinesSuppressed && normalPauseReconnectSetTerminal(record.Graph.Reconnect) &&
		pauseTimeCoversGraphHistory(record.Graph, record.PausedAt) &&
		pausedGraphMatchesExpected(record.Graph, record.Expected, record.DraftResultRevisionID,
			record.CommandID, record.ActorID, record.Reason, record.PausedAt, record.PauseID, record.SuspendedReconnect)
}

func normalPauseReconnectSetTerminal(values []pausedomain.PauseReconnectInterval) bool {
	for _, value := range values {
		if value.State == pausedomain.ReconnectStateOpen {
			return false
		}
	}
	return true
}

func validatePauseGraph(graph PauseGraph, paused bool) error {
	if !validPauseGraphRoot(graph) {
		return normalPauseError("invalid graph root")
	}
	if !validPauseGraphStateEvidence(graph, paused) {
		return normalPauseError("invalid graph pause evidence")
	}
	index, err := validatePauseSeriesDescendants(graph)
	if err != nil {
		return fmt.Errorf("pause graph - Series: %w", err)
	}
	if err := validatePauseGameDescendants(graph, index); err != nil {
		return fmt.Errorf("pause graph - Games: %w", err)
	}
	if err := validatePauseDraftDescendant(graph, index); err != nil {
		return fmt.Errorf("pause graph - Draft: %w", err)
	}
	if err := validatePausePresenceSet(graph, index); err != nil {
		return fmt.Errorf("pause graph - Presence: %w", err)
	}
	if err := validateReconnectSet(graph, index); err != nil {
		return fmt.Errorf("pause graph - Reconnect: %w", err)
	}
	if err := validateFrozenDeadlineSet(graph, paused); err != nil {
		return fmt.Errorf("pause graph - frozen deadlines: %w", err)
	}
	return nil
}

func validPauseGraphRoot(graph PauseGraph) bool {
	return validPauseGraphScope(graph.Scope) && graph.Revision >= 1 &&
		graph.Tournament.ID == graph.Scope.TournamentID && graph.Tournament.RosterID == graph.Scope.RosterID &&
		graph.Wave.Wave.ID == graph.Scope.WaveID && graph.Wave.Wave.TournamentID == graph.Scope.TournamentID &&
		graph.Wave.Revision >= 1 && validateTournamentRecordPointer(&graph.Tournament, graph.Scope.TournamentID) == nil &&
		graph.Wave.Wave.Validate() == nil && len(graph.Series) > 0 && graph.TerminalActionRevision >= 0
}

func validPauseGraphStateEvidence(graph PauseGraph, paused bool) bool {
	if paused != graph.DeadlinesSuppressed {
		return false
	}
	if paused {
		return graph.ActivePauseID != uuid.Nil && graph.PausedAt != nil
	}
	return graph.ActivePauseID == uuid.Nil && graph.PausedAt == nil
}

type pauseGraphIndex struct {
	seriesByID            map[uuid.UUID]PauseSeries
	participantSeries     map[uuid.UUID]uuid.UUID
	currentGames          map[uuid.UUID]uuid.UUID
	gamesByID             map[uuid.UUID]PauseGame
	presenceByParticipant map[uuid.UUID]pausedomain.PausePresence
}

func validatePauseSeriesDescendants(graph PauseGraph) (pauseGraphIndex, error) {
	index := pauseGraphIndex{
		seriesByID:            make(map[uuid.UUID]PauseSeries, len(graph.Series)),
		participantSeries:     make(map[uuid.UUID]uuid.UUID, len(graph.Wave.Wave.Members)),
		currentGames:          make(map[uuid.UUID]uuid.UUID, len(graph.Series)),
		gamesByID:             make(map[uuid.UUID]PauseGame, len(graph.Games)),
		presenceByParticipant: make(map[uuid.UUID]pausedomain.PausePresence, len(graph.Presence)),
	}
	waveMembers := make(map[uuid.UUID]struct{}, len(graph.Wave.Wave.Members))
	for _, member := range graph.Wave.Wave.Members {
		waveMembers[member.ParticipantID] = struct{}{}
	}
	for _, series := range graph.Series {
		id := series.Execution.Series.ID
		if id == uuid.Nil || series.Revision < 1 || series.Execution.Validate() != nil || series.Execution.Series.TournamentID != graph.Scope.TournamentID {
			return pauseGraphIndex{}, normalPauseError("invalid Series descendant")
		}
		if _, duplicate := index.seriesByID[id]; duplicate {
			return pauseGraphIndex{}, normalPauseError("duplicate Series descendant")
		}
		index.seriesByID[id] = series
		participants := [2]uuid.UUID{series.Execution.Series.FirstParticipantID, series.Execution.Series.SecondParticipantID}
		for _, participantID := range participants {
			if _, exists := waveMembers[participantID]; !exists {
				return pauseGraphIndex{}, ErrNormalPauseGraphIncomplete
			}
			if _, duplicate := index.participantSeries[participantID]; duplicate {
				return pauseGraphIndex{}, ErrNormalPauseGraphIncomplete
			}
			index.participantSeries[participantID] = id
		}
		if series.CurrentGameID != nil {
			if *series.CurrentGameID == uuid.Nil {
				return pauseGraphIndex{}, normalPauseError("empty current Game identity")
			}
			if _, duplicate := index.currentGames[*series.CurrentGameID]; duplicate {
				return pauseGraphIndex{}, normalPauseError("duplicate current Game identity")
			}
			index.currentGames[*series.CurrentGameID] = id
		}
	}
	if len(index.participantSeries) != len(waveMembers) {
		return pauseGraphIndex{}, ErrNormalPauseGraphIncomplete
	}
	return index, nil
}

func validatePauseGameDescendants(graph PauseGraph, index pauseGraphIndex) error {
	seenGames := make(map[uuid.UUID]struct{}, len(graph.Games))
	for _, game := range graph.Games {
		seriesID, expected := index.currentGames[game.Game.ID]
		if !expected || seriesID != game.SeriesID || game.Revision < 1 || game.Game.Validate() != nil || game.Game.ID == uuid.Nil {
			return ErrNormalPauseGraphIncomplete
		}
		if _, duplicate := seenGames[game.Game.ID]; duplicate {
			return normalPauseError("duplicate Game descendant")
		}
		seenGames[game.Game.ID] = struct{}{}
		series := index.seriesByID[seriesID]
		embedded, found := embeddedSeriesGame(series.Execution.Series, game.Game.ID)
		if !found || !reflect.DeepEqual(embedded, game.Game) || !validSeriesGameState(series, game) {
			return ErrNormalPauseGraphIncomplete
		}
		index.gamesByID[game.Game.ID] = game
	}
	if len(seenGames) != len(index.currentGames) {
		return ErrNormalPauseGraphIncomplete
	}
	return nil
}

func embeddedSeriesGame(series domain.Series, gameID uuid.UUID) (domain.Game, bool) {
	var result domain.Game
	found := false
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			if game.ID != gameID {
				continue
			}
			if found {
				return domain.Game{}, false
			}
			result, found = game, true
		}
	}
	return result, found
}

func validSeriesGameState(series PauseSeries, game PauseGame) bool {
	switch series.Execution.Series.State {
	case domain.SeriesStateActive:
		return game.Game.State == domain.GameStateActive && game.ResumeState == nil
	case domain.SeriesStateTechnicalPause:
		if series.Execution.ResumeState == nil || *series.Execution.ResumeState != domain.SeriesStateActive {
			return game.Game.State != domain.GameStateActive && game.Game.State != domain.GameStatePaused
		}
		return game.Game.State == domain.GameStatePaused && game.ResumeState != nil && *game.ResumeState == domain.GameStateActive
	case domain.SeriesStatePlanned, domain.SeriesStateLocked, domain.SeriesStateDraft,
		domain.SeriesStateReady, domain.SeriesStateReplayRequired, domain.SeriesStateCompleted,
		domain.SeriesStateCancelled:
		return game.Game.State != domain.GameStateActive && game.Game.State != domain.GameStatePaused
	default:
		return false
	}
}

func validatePauseDraftDescendant(graph PauseGraph, index pauseGraphIndex) error {
	if graph.Draft == nil {
		return validateMissingPauseDraft(graph.Series)
	}
	if graph.Draft.Validate() != nil {
		return normalPauseError("invalid Draft descendant")
	}
	series, exists := index.seriesByID[graph.Draft.SeriesID]
	if !exists {
		return ErrNormalPauseGraphIncomplete
	}
	participantsMatch := series.Execution.Series.FirstParticipantID == graph.Draft.FirstParticipantID &&
		series.Execution.Series.SecondParticipantID == graph.Draft.SecondParticipantID ||
		series.Execution.Series.FirstParticipantID == graph.Draft.SecondParticipantID &&
			series.Execution.Series.SecondParticipantID == graph.Draft.FirstParticipantID
	if !participantsMatch {
		return ErrNormalPauseGraphIncomplete
	}
	if !pauseDraftStateMatchesSeries(*graph.Draft, series) {
		return ErrNormalPauseGraphIncomplete
	}
	return nil
}

func validateMissingPauseDraft(seriesValues []PauseSeries) error {
	for _, series := range seriesValues {
		if series.Execution.Series.State == domain.SeriesStateDraft ||
			(series.Execution.Series.State == domain.SeriesStateTechnicalPause && series.Execution.ResumeState != nil && *series.Execution.ResumeState == domain.SeriesStateDraft) {
			return ErrNormalPauseGraphIncomplete
		}
	}
	return nil
}

func pauseDraftStateMatchesSeries(draft draftusecase.Execution, series PauseSeries) bool {
	switch draft.State {
	case draftusecase.ExecutionStateActive:
		return series.Execution.Series.State == domain.SeriesStatePlanned || series.Execution.Series.State == domain.SeriesStateDraft
	case draftusecase.ExecutionStatePaused:
		return series.Execution.Series.State == domain.SeriesStatePlanned ||
			series.Execution.Series.State == domain.SeriesStateTechnicalPause && series.Execution.ResumeState != nil &&
				*series.Execution.ResumeState == domain.SeriesStateDraft
	case draftusecase.ExecutionStateRecoveryRequired:
		return series.Execution.Series.State == domain.SeriesStatePlanned || series.Execution.Series.State == domain.SeriesStateDraft
	case draftusecase.ExecutionStateCompleted, draftusecase.ExecutionStateSuperseded:
		return true
	default:
		return false
	}
}
