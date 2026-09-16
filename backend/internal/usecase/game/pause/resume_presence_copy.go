package game

import pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"

func reconcilePauseResumePresence(record PauseResumePresenceRecord, command PauseResumePresenceCommand) (*PauseResumePresenceRecord, error) {
	if validatePauseResumePresenceRecord(record) != nil || !pauseResumePresenceCommandEqual(record.Command, command) {
		return nil, ErrPauseResumePresenceCommandReuse
	}
	clone := clonePauseResumePresenceRecord(record)
	return &clone, nil
}

func clonePauseResumePresenceCommand(value PauseResumePresenceCommand) PauseResumePresenceCommand {
	clone := value
	clone.Resume.Expected = clonePauseResumeExpectation(value.Resume.Expected)
	clone.SeriesExpected = clonePauseResumeDecisionExpectation(value.SeriesExpected)
	clone.GameExpected = clonePauseResumeDecisionExpectation(value.GameExpected)
	clone.Presence = clonePausePresenceSlice(value.Presence)
	clone.Reconnect = clonePauseReconnectSlice(value.Reconnect)
	clone.Counters = clonePauseSlice(value.Counters)
	clone.FrozenDeadlines = clonePauseFrozenDeadlineSlice(value.FrozenDeadlines)
	clone.FirstInterval = clonePauseResumeIntervalInput(value.FirstInterval)
	clone.SecondInterval = clonePauseResumeIntervalInput(value.SecondInterval)
	return clone
}

func clonePauseResumeIntervalInput(value *PauseResumeIntervalInput) *PauseResumeIntervalInput {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func clonePauseResumePresenceRecord(value PauseResumePresenceRecord) PauseResumePresenceRecord {
	clone := value
	clone.Command = clonePauseResumePresenceCommand(value.Command)
	clone.GameDecision = clonePauseResumeDecisionRecord(value.GameDecision)
	if value.SeriesDecision != nil {
		decision := clonePauseResumeDecisionRecord(*value.SeriesDecision)
		clone.SeriesDecision = &decision
	}
	clone.GameClock = clonePauseResumeGameClock(value.GameClock)
	clone.NormalPauseResolvedAt = pauseCloneTimePointer(value.NormalPauseResolvedAt)
	clone.Graph = clonePauseGraph(value.Graph)
	clone.First = clonePauseResumeParticipantResolution(value.First)
	clone.Second = clonePauseResumeParticipantResolution(value.Second)
	return clone
}

func clonePauseResumeDecisionRecord(value PauseResumeDecisionRecord) PauseResumeDecisionRecord {
	clone := value
	clone.FirstReconnectIntervalID = pauseCloneUUIDPointer(value.FirstReconnectIntervalID)
	clone.SecondReconnectIntervalID = pauseCloneUUIDPointer(value.SecondReconnectIntervalID)
	return clone
}

func clonePauseResumeParticipantResolution(value PauseResumeParticipantResolution) PauseResumeParticipantResolution {
	clone := value
	clone.SourceInterval = clonePauseReconnectPointer(value.SourceInterval)
	clone.CurrentInterval = clonePauseReconnectPointer(value.CurrentInterval)
	return clone
}

func clonePauseReconnectPointer(value *pausedomain.PauseReconnectInterval) *pausedomain.PauseReconnectInterval {
	if value == nil {
		return nil
	}
	clone := clonePauseReconnectSlice([]pausedomain.PauseReconnectInterval{*value})[0]
	return &clone
}
