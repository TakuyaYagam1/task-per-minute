package game

import (
	"math"
	"reflect"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
)

func validReconnectGameClock(authority ReconnectAuthority) bool {
	clock := authority.GameClock
	if clock.PauseID != authority.PauseID || clock.GameID != authority.Game.ID || clock.Revision < 1 || !reconnectValidServerTime(clock.OriginalDeadline) {
		return false
	}
	if clock.FrozenAt.IsZero() {
		return (authority.Game.State == domain.GameStateActive || authority.Game.State.IsTerminal()) &&
			clock.Remaining == 0 && clock.ResumedAt == nil && clock.ResumedDeadline == nil
	}
	if authority.Game.State == domain.GameStateActive {
		return clock.Validate(false) == nil
	}
	return clock.Validate(true) == nil
}

func reconnectSeriesID(authority ReconnectAuthority) uuid.UUID {
	if len(authority.Presence) == 0 {
		return uuid.Nil
	}
	return authority.Presence[0].SeriesID
}

func reconnectPresenceByParticipant(values []pause.PausePresence, participantID uuid.UUID) *pause.PausePresence {
	for index := range values {
		if values[index].ParticipantID == participantID {
			return &values[index]
		}
	}
	return nil
}

func reconnectCounterByParticipant(values []pause.PauseReconnectCounter, participantID uuid.UUID) *pause.PauseReconnectCounter {
	for index := range values {
		if values[index].ParticipantID == participantID {
			return &values[index]
		}
	}
	return nil
}

func reconnectMutationIntervalByID(values []pause.PauseReconnectInterval, intervalID uuid.UUID) *pause.PauseReconnectInterval {
	for index := range values {
		if values[index].ID == intervalID {
			return &values[index]
		}
	}
	return nil
}

func reconnectOpponentConnected(authority ReconnectAuthority, participantID uuid.UUID) bool {
	for _, presence := range authority.Presence {
		if presence.ParticipantID != participantID {
			return presence.State == pause.PresenceStateConnected
		}
	}
	return false
}

func reconnectOpponentExpired(authority ReconnectAuthority, participantID uuid.UUID) bool {
	opponentID, ok := reconnectOpponentID(authority.Series, participantID)
	if !ok {
		return false
	}
	presence := reconnectPresenceByParticipant(authority.Presence, opponentID)
	if presence == nil || presence.State != pause.PresenceStateDisconnected {
		return false
	}
	interval := reconnectCurrentInterval(authority, opponentID)
	return interval != nil && interval.State == pause.ReconnectStateExpired
}

func reconnectOpponentMust(series domain.Series, participantID uuid.UUID) uuid.UUID {
	opponentID, _ := reconnectOpponentID(series, participantID)
	return opponentID
}

func reconnectOpponentID(series domain.Series, participantID uuid.UUID) (uuid.UUID, bool) {
	switch participantID {
	case series.FirstParticipantID:
		return series.SecondParticipantID, true
	case series.SecondParticipantID:
		return series.FirstParticipantID, true
	default:
		return uuid.Nil, false
	}
}

func reconnectAuthorityGameMirrored(authority ReconnectAuthority) bool {
	for _, slot := range authority.Series.Slots {
		for _, game := range slot.Attempts {
			if game.ID == authority.Game.ID {
				return reflect.DeepEqual(game, authority.Game)
			}
		}
	}
	return false
}

func transitionReconnectGame(authority *ReconnectAuthority, nextState domain.GameState, terminal *gamedomain.TerminalEvidence) error {
	for slotIndex := range authority.Series.Slots {
		slot := authority.Series.Slots[slotIndex]
		if len(slot.Attempts) == 0 || slot.Attempts[len(slot.Attempts)-1].ID != authority.Game.ID {
			continue
		}
		transitioned, changed, err := gamedomain.TransitionSlotAttempt(slot, gamedomain.TransitionCommand{
			GameID: authority.Game.ID, ExpectedAttemptNo: authority.Game.AttemptNo, ExpectedState: authority.Game.State,
			NextState: nextState, Terminal: terminal,
		})
		if err != nil || !changed {
			return reconnectError("transition Game: %v", err)
		}
		authority.Series.Slots[slotIndex] = transitioned
		authority.Game = pause.CloneGame(transitioned.Attempts[len(transitioned.Attempts)-1])
		if authority.GameRevision == math.MaxInt64 || authority.SeriesRevision == math.MaxInt64 {
			return reconnectError("revision overflow")
		}
		authority.GameRevision++
		authority.SeriesRevision++
		return nil
	}
	return reconnectError("current Game is missing from Series")
}
