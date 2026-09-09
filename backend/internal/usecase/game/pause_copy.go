package game

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

func cloneTournamentRecord(record TournamentRecord) *TournamentRecord {
	cloned := record
	cloned.PausedFromState = cloneTournamentStatePointer(record.PausedFromState)
	cloned.StartedAt = pauseCloneTimePointer(record.StartedAt)
	cloned.FinishedAt = pauseCloneTimePointer(record.FinishedAt)
	return &cloned
}

func pauseCloneWaveExecution(wave domain.Wave) domain.Wave {
	clone := wave
	clone.Members = append([]domain.WaveMember(nil), wave.Members...)
	if wave.ReadyWindow != nil {
		window := *wave.ReadyWindow
		window.ConsumedAt = pauseCloneTimePointer(wave.ReadyWindow.ConsumedAt)
		clone.ReadyWindow = &window
	}
	clone.StartedAt = pauseCloneTimePointer(wave.StartedAt)
	clone.PausedAt = pauseCloneTimePointer(wave.PausedAt)
	return clone
}

func clonePauseGraph(value PauseGraph) PauseGraph {
	clone := value
	clone.Tournament = *cloneTournamentRecord(value.Tournament)
	clone.Wave.Wave = pauseCloneWaveExecution(value.Wave.Wave)
	clone.Series = make([]PauseSeries, len(value.Series))
	for index := range value.Series {
		clone.Series[index] = value.Series[index]
		clone.Series[index].Execution = seriesdomain.CloneExecution(value.Series[index].Execution)
		clone.Series[index].CurrentGameID = pauseCloneUUIDPointer(value.Series[index].CurrentGameID)
	}
	clone.Games = make([]PauseGame, len(value.Games))
	for index := range value.Games {
		clone.Games[index] = value.Games[index]
		clone.Games[index].Game = pausedomain.CloneGame(value.Games[index].Game)
		clone.Games[index].Deadline = pauseCloneTimePointer(value.Games[index].Deadline)
		clone.Games[index].ResumeState = cloneGameStatePointer(value.Games[index].ResumeState)
	}
	if value.Draft != nil {
		draft := cloneDraftExecution(*value.Draft)
		clone.Draft = &draft
	}
	clone.Presence = clonePausePresenceSlice(value.Presence)
	clone.Reconnect = clonePauseReconnectSlice(value.Reconnect)
	clone.Counters = clonePauseSlice(value.Counters)
	clone.FrozenDeadlines = clonePauseFrozenDeadlineSlice(value.FrozenDeadlines)
	clone.PausedAt = pauseCloneTimePointer(value.PausedAt)
	return clone
}

func clonePausePresenceSlice(values []pausedomain.PausePresence) []pausedomain.PausePresence {
	clone := clonePauseSlice(values)
	for index := range clone {
		clone[index].DisconnectedAt = pauseCloneTimePointer(values[index].DisconnectedAt)
	}
	return clone
}

func clonePauseReconnectSlice(values []pausedomain.PauseReconnectInterval) []pausedomain.PauseReconnectInterval {
	clone := clonePauseSlice(values)
	for index := range clone {
		clone[index].ClosedAt = pauseCloneTimePointer(clone[index].ClosedAt)
		clone[index].ContinuedFromID = pauseCloneUUIDPointer(clone[index].ContinuedFromID)
		clone[index].SuspendedByPauseID = pauseCloneUUIDPointer(clone[index].SuspendedByPauseID)
	}
	return clone
}

func clonePauseFrozenDeadlineSlice(values []PauseFrozenDeadline) []PauseFrozenDeadline {
	clone := clonePauseSlice(values)
	for index := range clone {
		clone[index].ResumedAt = pauseCloneTimePointer(values[index].ResumedAt)
		clone[index].ResumedDeadline = pauseCloneTimePointer(values[index].ResumedDeadline)
	}
	return clone
}

func cloneNormalPauseRecord(value NormalPauseRecord) NormalPauseRecord {
	clone := value
	clone.Expected = clonePauseGraphRevisions(value.Expected)
	clone.Graph = clonePauseGraph(value.Graph)
	clone.SuspendedReconnect = clonePauseSlice(value.SuspendedReconnect)
	clone.ResolvedAt = pauseCloneTimePointer(value.ResolvedAt)
	return clone
}

func clonePauseGraphRevisions(value PauseGraphRevisions) PauseGraphRevisions {
	clone := value
	clone.Series = clonePauseSlice(value.Series)
	clone.Games = clonePauseSlice(value.Games)
	clone.Presence = clonePauseSlice(value.Presence)
	clone.Reconnect = clonePauseSlice(value.Reconnect)
	clone.Counters = clonePauseSlice(value.Counters)
	clone.FrozenDeadlines = clonePauseSlice(value.FrozenDeadlines)
	if value.Draft != nil {
		draft := *value.Draft
		clone.Draft = &draft
	}
	return clone
}

func clonePauseSlice[T any](value []T) []T {
	if value == nil {
		return nil
	}
	return append(make([]T, 0, len(value)), value...)
}

func cloneTournamentStatePointer(value *domain.TournamentState) *domain.TournamentState {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneGameStatePointer(value *domain.GameState) *domain.GameState {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
