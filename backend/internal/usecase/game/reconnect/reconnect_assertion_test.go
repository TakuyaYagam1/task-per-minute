package reconnect_test

import (
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	reconnectusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
	"github.com/google/uuid"
)

func task045Presence(t *testing.T, authority reconnectusecase.ReconnectAuthority, participantID uuid.UUID) pause.PausePresence {
	t.Helper()
	for _, value := range authority.Presence {
		if value.ParticipantID == participantID {
			return value
		}
	}
	t.Fatalf("Presence for participant %s not found", participantID)
	return pause.PausePresence{}
}

func task045Counter(t *testing.T, authority reconnectusecase.ReconnectAuthority, participantID uuid.UUID) pause.PauseReconnectCounter {
	t.Helper()
	for _, value := range authority.Counters {
		if value.ParticipantID == participantID {
			return value
		}
	}
	t.Fatalf("counter for participant %s not found", participantID)
	return pause.PauseReconnectCounter{}
}

func task045Interval(t *testing.T, authority reconnectusecase.ReconnectAuthority, intervalID uuid.UUID) pause.PauseReconnectInterval {
	t.Helper()
	for _, value := range authority.Reconnect {
		if value.ID == intervalID {
			return value
		}
	}
	t.Fatalf("reconnect interval %s not found", intervalID)
	return pause.PauseReconnectInterval{}
}

func task045PresenceEqual(first, second pause.PausePresence) bool {
	if first.ID != second.ID || first.TournamentID != second.TournamentID || first.RosterID != second.RosterID ||
		first.SeriesID != second.SeriesID || first.ParticipantID != second.ParticipantID || first.State != second.State ||
		first.PresenceEpoch != second.PresenceEpoch || first.Revision != second.Revision ||
		!first.ConnectedAt.Equal(second.ConnectedAt) || !first.UpdatedAt.Equal(second.UpdatedAt) {
		return false
	}
	if first.DisconnectedAt == nil || second.DisconnectedAt == nil {
		return first.DisconnectedAt == nil && second.DisconnectedAt == nil
	}
	return first.DisconnectedAt.Equal(*second.DisconnectedAt)
}

func task045GameEqual(first, second domain.Game) bool {
	if first.ID != second.ID || first.SlotID != second.SlotID || first.AttemptNo != second.AttemptNo ||
		first.State != second.State || first.ResultReason != second.ResultReason {
		return false
	}
	if first.WinnerID == nil || second.WinnerID == nil {
		if first.WinnerID != nil || second.WinnerID != nil {
			return false
		}
	} else if *first.WinnerID != *second.WinnerID {
		return false
	}
	if first.ResultRevisionID == nil || second.ResultRevisionID == nil {
		return first.ResultRevisionID == nil && second.ResultRevisionID == nil
	}
	return *first.ResultRevisionID == *second.ResultRevisionID
}

func task045RecordCommandID(record reconnectusecase.ReconnectRecord) uuid.UUID {
	switch {
	case record.ReconnectCommand != nil:
		return record.ReconnectCommand.CommandID
	case record.TimeoutCommand != nil:
		return record.TimeoutCommand.CommandID
	case record.DisconnectCommand != nil:
		return record.DisconnectCommand.CommandID
	default:
		return uuid.Nil
	}
}

func cloneTask045Record(value reconnectusecase.ReconnectRecord) reconnectusecase.ReconnectRecord {
	clone := value
	clone.ReconnectAuthority = cloneTask045Authority(value.ReconnectAuthority)
	if value.ReconnectCommand != nil {
		command := *value.ReconnectCommand
		clone.ReconnectCommand = &command
	}
	if value.TimeoutCommand != nil {
		command := *value.TimeoutCommand
		clone.TimeoutCommand = &command
	}
	if value.DisconnectCommand != nil {
		command := *value.DisconnectCommand
		if command.ContinuedFromID != nil {
			id := *command.ContinuedFromID
			command.ContinuedFromID = &id
		}
		clone.DisconnectCommand = &command
	}
	if value.GameResultRevision != nil {
		revision := *value.GameResultRevision
		clone.GameResultRevision = &revision
	}
	if value.VoidGameResultRevision != nil {
		revision := *value.VoidGameResultRevision
		clone.VoidGameResultRevision = &revision
	}
	if value.ScoreRevision != nil {
		revision := *value.ScoreRevision
		if revision.PreviousRevisionID != nil {
			previous := *revision.PreviousRevisionID
			revision.PreviousRevisionID = &previous
		}
		revision.GameResultRevisionIDs = append([]domain.OfficialResultRevisionID(nil), revision.GameResultRevisionIDs...)
		clone.ScoreRevision = &revision
	}
	if value.SeriesResultRevision != nil {
		revision := *value.SeriesResultRevision
		if revision.PreviousRevisionID != nil {
			previous := *revision.PreviousRevisionID
			revision.PreviousRevisionID = &previous
		}
		if revision.WinnerID != nil {
			winner := *revision.WinnerID
			revision.WinnerID = &winner
		}
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

func cloneTask045Authority(value reconnectusecase.ReconnectAuthority) reconnectusecase.ReconnectAuthority {
	clone := value
	clone.Presence = append([]pause.PausePresence(nil), value.Presence...)
	for index := range clone.Presence {
		if value.Presence[index].DisconnectedAt != nil {
			at := *value.Presence[index].DisconnectedAt
			clone.Presence[index].DisconnectedAt = &at
		}
	}
	clone.Reconnect = append([]pause.PauseReconnectInterval(nil), value.Reconnect...)
	for index := range clone.Reconnect {
		if value.Reconnect[index].ContinuedFromID != nil {
			id := *value.Reconnect[index].ContinuedFromID
			clone.Reconnect[index].ContinuedFromID = &id
		}
		if value.Reconnect[index].ClosedAt != nil {
			at := *value.Reconnect[index].ClosedAt
			clone.Reconnect[index].ClosedAt = &at
		}
	}
	clone.Counters = append([]pause.PauseReconnectCounter(nil), value.Counters...)
	clone.CurrentGameResultRevisionIDs = append([]domain.OfficialResultRevisionID(nil), value.CurrentGameResultRevisionIDs...)
	clone.Series.Slots = append([]domain.GameSlot(nil), value.Series.Slots...)
	for slotIndex := range clone.Series.Slots {
		clone.Series.Slots[slotIndex].Attempts = append([]domain.Game(nil), value.Series.Slots[slotIndex].Attempts...)
	}
	if value.GameClock.ResumedAt != nil {
		at := *value.GameClock.ResumedAt
		clone.GameClock.ResumedAt = &at
	}
	if value.GameClock.ResumedDeadline != nil {
		at := *value.GameClock.ResumedDeadline
		clone.GameClock.ResumedDeadline = &at
	}
	if value.Current != nil {
		current := *value.Current
		if value.Current.GameResultRevision != nil {
			revision := *value.Current.GameResultRevision
			current.GameResultRevision = &revision
		}
		if value.Current.VoidGameResultRevision != nil {
			revision := *value.Current.VoidGameResultRevision
			current.VoidGameResultRevision = &revision
		}
		current.ScoreRevision.GameResultRevisionIDs = append([]domain.OfficialResultRevisionID(nil), value.Current.ScoreRevision.GameResultRevisionIDs...)
		if value.Current.ScoreRevision.PreviousRevisionID != nil {
			previous := *value.Current.ScoreRevision.PreviousRevisionID
			current.ScoreRevision.PreviousRevisionID = &previous
		}
		if value.Current.SeriesResultRevision != nil {
			revision := *value.Current.SeriesResultRevision
			if revision.PreviousRevisionID != nil {
				previous := *revision.PreviousRevisionID
				revision.PreviousRevisionID = &previous
			}
			if revision.WinnerID != nil {
				winner := *revision.WinnerID
				revision.WinnerID = &winner
			}
			current.SeriesResultRevision = &revision
		}
		if value.Current.ReplayRoute != nil {
			route := *value.Current.ReplayRoute
			current.ReplayRoute = &route
		}
		clone.Current = &current
	}
	return clone
}
