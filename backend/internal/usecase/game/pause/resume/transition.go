package resume

import (
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func buildPauseResumeRecord(authority PauseResumeAuthority, command PauseResumeCommand, resumedAt time.Time) (PauseResumeRecord, error) {
	if resumedAt.Before(authority.Pause.PausedAt) || !resumeTimeCoversAuthorityHistory(authority, resumedAt) {
		return PauseResumeRecord{}, pauseResumeError("resume time precedes pause")
	}
	for _, presence := range authority.Presence {
		if presence.State != pausedomain.PresenceStateConnected {
			return PauseResumeRecord{}, ErrPauseResumePresence
		}
		if !pausedomain.TimeCoversPresenceHistory(resumedAt, presence) {
			return PauseResumeRecord{}, pauseResumeError("resume time precedes Presence")
		}
	}
	for _, interval := range authority.Reconnect {
		if interval.State == pausedomain.ReconnectStateOpen {
			return PauseResumeRecord{}, ErrPauseResumePresence
		}
		if !pausedomain.TimeCoversReconnectHistory(resumedAt, interval) {
			return PauseResumeRecord{}, pauseResumeError("resume time precedes Reconnect")
		}
	}
	pause := authority.Pause
	if pause.Revision == math.MaxInt64 || pause.Graph.Revision == math.MaxInt64 {
		return PauseResumeRecord{}, ErrPauseResumeOverflow
	}
	graph := clonePauseGraph(pause.Graph)
	graph.Presence = clonePausePresenceSlice(authority.Presence)
	graph.Reconnect = clonePauseReconnectSlice(authority.Reconnect)
	graph.Counters = append([]pausedomain.PauseReconnectCounter(nil), authority.Counters...)
	graph.FrozenDeadlines = clonePauseFrozenDeadlineSlice(authority.FrozenDeadlines)
	graph.TerminalActionRevision = authority.TerminalActionRevision
	if err := shiftPauseDeadlines(&graph, resumedAt); err != nil {
		return PauseResumeRecord{}, err
	}
	if err := restorePauseGraph(&graph, command, resumedAt); err != nil {
		return PauseResumeRecord{}, err
	}
	graph.Revision++
	graph.ActivePauseID = uuid.Nil
	graph.PausedAt = nil
	graph.DeadlinesSuppressed = false
	record := PauseResumeRecord{
		Scope: command.Scope, PauseID: command.PauseID, CommandID: command.CommandID, ActorID: command.ActorID,
		DraftResultRevisionID: command.DraftResultRevisionID,
		State:                 PauseStateResumed, Revision: pause.Revision + 1, Expected: clonePauseResumeExpectation(command.Expected),
		Graph: graph, ResumedAt: resumedAt,
	}
	if err := validatePauseResumeRecord(record); err != nil {
		return PauseResumeRecord{}, err
	}
	return clonePauseResumeRecord(record), nil
}

func resumeTimeCoversAuthorityHistory(authority PauseResumeAuthority, resumedAt time.Time) bool {
	if !pauseTimeCoversGraphHistory(authority.Pause.Graph, resumedAt) {
		return false
	}
	for _, presence := range authority.Presence {
		if !pausedomain.TimeCoversPresenceHistory(resumedAt, presence) {
			return false
		}
	}
	for _, interval := range authority.Reconnect {
		if !pausedomain.TimeCoversReconnectHistory(resumedAt, interval) {
			return false
		}
	}
	for _, frozen := range authority.FrozenDeadlines {
		if !pausedomain.TimeAtOrBefore(resumedAt, frozen.FrozenAt) || !pausedomain.TimePointerAtOrBefore(resumedAt, frozen.ResumedAt) {
			return false
		}
	}
	return true
}

func shiftPauseDeadlines(graph *PauseGraph, resumedAt time.Time) error {
	seen := make(map[PauseDeadlineKind]map[uuid.UUID]struct{})
	for index := range graph.FrozenDeadlines {
		frozen := &graph.FrozenDeadlines[index]
		if pauseDeadlineSeen(seen, frozen.Kind, frozen.OwnerID) {
			return ErrPauseResumeIncomplete
		}
		if err := shiftPauseDeadline(graph, frozen, resumedAt); err != nil {
			return err
		}
	}
	return nil
}

func pauseDeadlineSeen(seen map[PauseDeadlineKind]map[uuid.UUID]struct{}, kind PauseDeadlineKind, ownerID uuid.UUID) bool {
	if seen[kind] == nil {
		seen[kind] = make(map[uuid.UUID]struct{})
	}
	if _, duplicate := seen[kind][ownerID]; duplicate {
		return true
	}
	seen[kind][ownerID] = struct{}{}
	return false
}

func shiftPauseDeadline(graph *PauseGraph, frozen *PauseFrozenDeadline, resumedAt time.Time) error {
	if validateFrozenDeadline(*frozen, true) != nil || frozen.Revision == math.MaxInt64 {
		return ErrPauseResumeOverflow
	}
	deadline, ok := pausedomain.AddTime(resumedAt, frozen.Remaining)
	if !ok {
		return ErrPauseResumeOverflow
	}
	if err := applyResumedDeadline(graph, frozen.Kind, frozen.OwnerID, deadline); err != nil {
		return err
	}
	frozen.ResumedAt = pauseCloneTimePointer(&resumedAt)
	frozen.ResumedDeadline = pauseCloneTimePointer(&deadline)
	frozen.Revision++
	return nil
}

func applyResumedDeadline(graph *PauseGraph, kind PauseDeadlineKind, ownerID uuid.UUID, deadline time.Time) error {
	switch kind {
	case PauseDeadlineReadyWindow:
		if graph.Wave.Wave.ReadyWindow == nil || graph.Wave.Wave.ReadyWindow.ID != ownerID {
			return ErrPauseResumeIncomplete
		}
		graph.Wave.Wave.ReadyWindow.Deadline = deadline
	case PauseDeadlineGame:
		game := pauseGameByID(graph.Games, ownerID)
		if game == nil {
			return ErrPauseResumeIncomplete
		}
		game.Deadline = pauseCloneTimePointer(&deadline)
	case PauseDeadlineDraft:
		if graph.Draft == nil || graph.Draft.ID != ownerID {
			return ErrPauseResumeIncomplete
		}
		graph.Draft.TurnDeadline = deadline
		graph.Draft.AbsoluteDeadline = pauseCloneTimePointer(&deadline)
	default:
		return ErrPauseResumeIncomplete
	}
	return nil
}

func restorePauseGraph(graph *PauseGraph, command PauseResumeCommand, resumedAt time.Time) error {
	if graph.Tournament.Revision == math.MaxInt64 {
		return ErrPauseResumeOverflow
	}
	tournament := domain.Tournament{State: graph.Tournament.State, PausedFromState: graph.Tournament.PausedFromState}
	if tournament.PausedFromState == nil {
		return ErrPauseResumeIncomplete
	}
	changed, err := tournament.TransitionTo(*tournament.PausedFromState)
	if err != nil || !changed {
		return pauseResumeError("restore Tournament: %v", err)
	}
	graph.Tournament.State = tournament.State
	graph.Tournament.PausedFromState = nil
	graph.Tournament.Revision++
	graph.Tournament.UpdatedAt = resumedAt
	if err := restoreWave(graph); err != nil {
		return err
	}
	if err := restoreSeriesAndGames(graph, command.CommandID, resumedAt); err != nil {
		return err
	}
	return restoreDraft(graph, command, resumedAt)
}

func restoreWave(graph *PauseGraph) error {
	switch graph.Wave.Wave.State {
	case domain.WaveStatePaused:
		if graph.Wave.Revision == math.MaxInt64 {
			return ErrPauseResumeOverflow
		}
		if err := graph.Wave.Wave.Resume(); err != nil {
			return pauseResumeError("resume Wave: %v", err)
		}
		graph.Wave.Revision++
	case domain.WaveStateReadyWindowOpen, domain.WaveStateReady:
		if graph.Wave.Revision == math.MaxInt64 {
			return ErrPauseResumeOverflow
		}
		graph.Wave.Revision++
	case domain.WaveStatePlanned, domain.WaveStateCompleted,
		domain.WaveStateReadyWindowExpired, domain.WaveStateSuperseded:
		return nil
	case domain.WaveStateActive:
		return ErrPauseResumeIncomplete
	default:
		return ErrPauseResumeIncomplete
	}
	return nil
}

func restoreSeriesAndGames(graph *PauseGraph, commandID uuid.UUID, resumedAt time.Time) error {
	seriesByID := make(map[uuid.UUID]*PauseSeries, len(graph.Series))
	for index := range graph.Series {
		series := &graph.Series[index]
		seriesByID[series.Execution.Series.ID] = series
		if err := restoreSeriesRecord(series); err != nil {
			return err
		}
	}
	for index := range graph.Games {
		game := &graph.Games[index]
		if game.Game.State != domain.GameStatePaused {
			continue
		}
		if err := restoreGameRecord(game, commandID, resumedAt); err != nil {
			return err
		}
		series := seriesByID[game.SeriesID]
		if series == nil || !replaceSeriesGame(&series.Execution.Series, game.Game) {
			return ErrPauseResumeIncomplete
		}
	}
	return nil
}

func restoreSeriesRecord(series *PauseSeries) error {
	if series.Execution.Series.State != domain.SeriesStateTechnicalPause {
		return nil
	}
	if series.Revision == math.MaxInt64 || series.Execution.ResumeState == nil {
		return ErrPauseResumeOverflow
	}
	next, changed, err := seriesdomain.Transition(series.Execution, seriesdomain.TransitionCommand{NextState: *series.Execution.ResumeState})
	if err != nil || !changed {
		return pauseResumeError("resume Series: %v", err)
	}
	series.Execution = next
	series.Revision++
	return nil
}

func restoreGameRecord(game *PauseGame, commandID uuid.UUID, resumedAt time.Time) error {
	if game.Revision == math.MaxInt64 {
		return ErrPauseResumeOverflow
	}
	if game.SourcePause != nil {
		return restoreSourceGameRecord(game, commandID, resumedAt)
	}
	if game.ResumeState == nil || *game.ResumeState != domain.GameStateActive || game.Deadline == nil {
		return ErrPauseResumeIncomplete
	}
	game.Game.State = *game.ResumeState
	game.ResumeState = nil
	game.Revision++
	return nil
}

func restoreSourceGameRecord(game *PauseGame, commandID uuid.UUID, resumedAt time.Time) error {
	source := game.SourcePause
	if source.State != PauseStateActive || source.Revision == math.MaxInt64 || source.DecisionNumber == math.MaxInt64 ||
		source.Clock.Revision == math.MaxInt64 {
		return ErrPauseResumeOverflow
	}
	deadline, ok := pausedomain.AddTime(resumedAt, source.Clock.Remaining)
	if !ok {
		return ErrPauseResumeOverflow
	}
	resolvedAt := resumedAt
	source.State = PauseStateResumed
	source.CurrentRevisionID = pauseSourceResumeRevisionID(commandID, source.PauseID)
	source.Revision++
	source.DecisionNumber++
	source.ResolvedAt = &resolvedAt
	source.Clock.ResumedAt = pauseCloneTimePointer(&resumedAt)
	source.Clock.ResumedDeadline = pauseCloneTimePointer(&deadline)
	source.Clock.Revision++
	game.Game.State = domain.GameStateActive
	game.Deadline = pauseCloneTimePointer(&deadline)
	game.Revision++
	return nil
}

func restoreDraft(graph *PauseGraph, command PauseResumeCommand, resumedAt time.Time) error {
	if graph.Draft == nil || graph.Draft.State != draftusecase.ExecutionStatePaused {
		return nil
	}
	draft := draftusecase.CloneExecution(*graph.Draft)
	if draft.Revision == math.MaxInt64 || draft.AbsoluteDeadline == nil || draft.PausedRemaining <= 0 || draft.Recovery == nil {
		return ErrPauseResumeOverflow
	}
	if command.Expected.DraftPreviousRevisionID != draft.PreviousRevisionID ||
		command.DraftResultRevisionID == draft.ID || command.DraftResultRevisionID == draft.RevisionID ||
		command.DraftResultRevisionID == draft.PreviousRevisionID {
		return pauseResumeError("Draft result revision identity matches Draft")
	}
	draftusecase.AdvanceRevision(&draft, command.DraftResultRevisionID, command.CommandID, draft.ServiceEpoch)
	draft.State = draftusecase.ExecutionStateActive
	draft.PausedRemaining = 0
	draft.Recovery = nil
	draft.Transition = &draftusecase.TransitionEvidence{Operation: draftusecase.TransitionResume, ActorID: command.ActorID, Reason: "pause resumed", OccurredAt: resumedAt}
	if err := draft.Validate(); err != nil {
		return pauseResumeError("resume Draft: %v", err)
	}
	graph.Draft = &draft
	return nil
}

func pauseGameByID(games []PauseGame, id uuid.UUID) *PauseGame {
	for index := range games {
		if games[index].Game.ID == id {
			return &games[index]
		}
	}
	return nil
}
