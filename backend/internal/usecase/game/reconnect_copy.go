package game

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

func cloneSettlementScoreRevision(
	value seriesdomain.ScoreRevision,
) seriesdomain.ScoreRevision {
	clone := value
	clone.PreviousRevisionID = reconnectCloneSeriesScoreRevisionIDPointer(value.PreviousRevisionID)
	clone.GameResultRevisionIDs = append(
		[]domain.OfficialResultRevisionID(nil),
		value.GameResultRevisionIDs...,
	)
	return clone
}

func cloneReconnectCommand(value ReconnectCommand) ReconnectCommand { return value }

func cloneReconnectTimeoutCommand(value TimeoutCommand) TimeoutCommand {
	return value
}

func cloneDisconnectCommand(value DisconnectCommand) DisconnectCommand {
	clone := value
	if value.ContinuedFromID != nil {
		continued := *value.ContinuedFromID
		clone.ContinuedFromID = &continued
	}
	return clone
}

func cloneReconnectAuthority(value ReconnectAuthority) ReconnectAuthority {
	clone := value
	clone.Game = pause.CloneGame(value.Game)
	clone.Series = pause.CloneSeries(value.Series)
	clone.GameClock.ResumedAt = reconnectCloneTimePointer(value.GameClock.ResumedAt)
	clone.GameClock.ResumedDeadline = reconnectCloneTimePointer(value.GameClock.ResumedDeadline)
	clone.Presence = append([]pause.PausePresence(nil), value.Presence...)
	for index := range clone.Presence {
		clone.Presence[index].DisconnectedAt = reconnectCloneTimePointer(value.Presence[index].DisconnectedAt)
	}
	clone.Reconnect = append([]pause.PauseReconnectInterval(nil), value.Reconnect...)
	for index := range clone.Reconnect {
		clone.Reconnect[index].ContinuedFromID = reconnectCloneUUIDPointer(value.Reconnect[index].ContinuedFromID)
		clone.Reconnect[index].SuspendedByPauseID = reconnectCloneUUIDPointer(value.Reconnect[index].SuspendedByPauseID)
		clone.Reconnect[index].ClosedAt = reconnectCloneTimePointer(value.Reconnect[index].ClosedAt)
	}
	clone.Counters = append([]pause.PauseReconnectCounter(nil), value.Counters...)
	clone.CurrentGameResultRevisionIDs = append(
		[]domain.OfficialResultRevisionID(nil),
		value.CurrentGameResultRevisionIDs...,
	)
	clone.Current = cloneReconnectTerminalOutcome(value.Current)
	return clone
}

func cloneReconnectTerminalOutcome(value *TerminalOutcome) *TerminalOutcome {
	if value == nil {
		return nil
	}
	clone := *value
	if value.GameResultRevision != nil {
		revision := *value.GameResultRevision
		clone.GameResultRevision = &revision
	}
	if value.VoidGameResultRevision != nil {
		revision := *value.VoidGameResultRevision
		clone.VoidGameResultRevision = &revision
	}
	clone.ScoreRevision = cloneSettlementScoreRevision(value.ScoreRevision)
	if value.SeriesResultRevision != nil {
		revision := *value.SeriesResultRevision
		revision.PreviousRevisionID = reconnectCloneOfficialResultRevisionIDPointer(
			value.SeriesResultRevision.PreviousRevisionID,
		)
		revision.WinnerID = reconnectCloneUUIDPointer(value.SeriesResultRevision.WinnerID)
		clone.SeriesResultRevision = &revision
	}
	if value.ReplayRoute != nil {
		route := *value.ReplayRoute
		clone.ReplayRoute = &route
	}
	return &clone
}

func cloneReconnectRecord(record *ReconnectRecord) *ReconnectRecord {
	if record == nil {
		return nil
	}
	clone := cloneReconnectRecordValue(*record)
	return &clone
}

func cloneReconnectRecordValue(value ReconnectRecord) ReconnectRecord {
	clone := value
	if value.ReconnectCommand != nil {
		command := cloneReconnectCommand(*value.ReconnectCommand)
		clone.ReconnectCommand = &command
	}
	if value.TimeoutCommand != nil {
		command := cloneReconnectTimeoutCommand(*value.TimeoutCommand)
		clone.TimeoutCommand = &command
	}
	if value.DisconnectCommand != nil {
		command := cloneDisconnectCommand(*value.DisconnectCommand)
		clone.DisconnectCommand = &command
	}
	clone.ReconnectAuthority = cloneReconnectAuthority(value.ReconnectAuthority)
	if value.GameResultRevision != nil {
		revision := *value.GameResultRevision
		clone.GameResultRevision = &revision
	}
	if value.VoidGameResultRevision != nil {
		revision := *value.VoidGameResultRevision
		clone.VoidGameResultRevision = &revision
	}
	if value.ScoreRevision != nil {
		revision := cloneSettlementScoreRevision(*value.ScoreRevision)
		clone.ScoreRevision = &revision
	}
	if value.SeriesResultRevision != nil {
		revision := *value.SeriesResultRevision
		revision.PreviousRevisionID = reconnectCloneOfficialResultRevisionIDPointer(
			value.SeriesResultRevision.PreviousRevisionID,
		)
		revision.WinnerID = reconnectCloneUUIDPointer(value.SeriesResultRevision.WinnerID)
		clone.SeriesResultRevision = &revision
	}
	if value.ReplayRoute != nil {
		route := *value.ReplayRoute
		clone.ReplayRoute = &route
	}
	if value.Evidence != nil {
		evidence := *value.Evidence
		clone.Evidence = &evidence
	}
	return clone
}
