package reconnect_test

import (
	"fmt"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	reconnectusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
	"github.com/google/uuid"
)

func task045Authority(now time.Time, firstDisconnected, secondDisconnected bool) reconnectusecase.ReconnectAuthority {
	tournamentID, rosterID, waveID := task045ID(1), task045ID(2), task045ID(3)
	seriesID, slotID, gameID, pauseID := task045ID(4), task045ID(5), task045ID(6), task045ID(7)
	firstID, secondID := task045ID(8), task045ID(9)
	game := domain.Game{ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.GameStateActive}
	series := domain.Series{ID: seriesID, TournamentID: tournamentID, FirstParticipantID: firstID, SecondParticipantID: secondID,
		Format: domain.SeriesFormatBO1, State: domain.SeriesStateActive,
		Slots: []domain.GameSlot{{ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb,
			ScoreBefore: domain.SeriesScore{}, Attempts: []domain.Game{game}}}}
	authority := reconnectusecase.ReconnectAuthority{
		Scope: pause.GraphScope{TournamentID: tournamentID, RosterID: rosterID, WaveID: waveID,
			Authority: authoritydomain.Identity{TournamentID: tournamentID, HolderID: task045ID(20), LeaseID: task045ID(21), Epoch: 1, ProcessKind: authoritydomain.ProcessAuthority}},
		Revision: 10, PauseID: pauseID, GameRevision: 4, SeriesRevision: 3,
		CurrentOrdinal: 7, CurrentProjectionRevision: 11,
		CurrentGameResultRevisionIDs: []domain.OfficialResultRevisionID{domain.OfficialResultRevisionID(task045ID(22))},
		Game:                         game, Series: series,
		GameClock: pause.PauseResumeGameClock{PauseID: pauseID, GameID: gameID, OriginalDeadline: now.Add(90 * time.Second), Revision: 1},
		Presence: []pause.PausePresence{
			{ID: task045ID(10), TournamentID: tournamentID, RosterID: rosterID, SeriesID: seriesID, ParticipantID: firstID, State: pause.PresenceStateConnected, PresenceEpoch: 1, Revision: 1, ConnectedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute)},
			{ID: task045ID(11), TournamentID: tournamentID, RosterID: rosterID, SeriesID: seriesID, ParticipantID: secondID, State: pause.PresenceStateConnected, PresenceEpoch: 1, Revision: 1, ConnectedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute)}},
		Counters: []pause.PauseReconnectCounter{
			{PauseID: pauseID, RosterID: rosterID, ParticipantID: firstID, Limit: domain.ReconnectCycleLimit, Revision: 1},
			{PauseID: pauseID, RosterID: rosterID, ParticipantID: secondID, Limit: domain.ReconnectCycleLimit, Revision: 1}}}
	if firstDisconnected {
		task045SetDisconnected(&authority, 0, task045ID(12), now)
	}
	if secondDisconnected {
		task045SetDisconnected(&authority, 1, task045ID(13), now)
	}
	if firstDisconnected || secondDisconnected {
		authority.Game.State = domain.GameStatePaused
		authority.Series.Slots[0].Attempts[0] = authority.Game
		authority.GameClock.FrozenAt = now.Add(-5 * time.Second)
		authority.GameClock.Remaining = 40 * time.Second
		authority.GameClock.OriginalDeadline = authority.GameClock.FrozenAt.Add(authority.GameClock.Remaining)
		authority.GameClock.Revision = 2
		authority.GameRevision++
		authority.SeriesRevision++
	}
	return authority
}

func task045SetDisconnected(authority *reconnectusecase.ReconnectAuthority, index int, intervalID uuid.UUID, now time.Time) {
	presence := &authority.Presence[index]
	disconnectedAt := now.Add(-5 * time.Second)
	presence.State = pause.PresenceStateDisconnected
	presence.PresenceEpoch = 2
	presence.Revision = 2
	presence.DisconnectedAt = &disconnectedAt
	presence.UpdatedAt = disconnectedAt
	authority.Counters[index].Used = 1
	authority.Counters[index].Revision = 2
	authority.Reconnect = append(authority.Reconnect, pause.PauseReconnectInterval{ID: intervalID, PauseID: authority.PauseID,
		RosterID: authority.Scope.RosterID, SeriesID: authority.Series.ID, GameID: authority.Game.ID,
		ParticipantID: presence.ParticipantID, PresenceEpoch: presence.PresenceEpoch, Number: 1,
		State: pause.ReconnectStateOpen, OpenedAt: disconnectedAt, Deadline: now.Add(20 * time.Second), Revision: 1, UpdatedAt: disconnectedAt})
}

func task045AddCompletedRoots(authority *reconnectusecase.ReconnectAuthority, index, count int, now time.Time) {
	presence := &authority.Presence[index]
	kept := authority.Reconnect[:0]
	for _, interval := range authority.Reconnect {
		if interval.ParticipantID != presence.ParticipantID {
			kept = append(kept, interval)
		}
	}
	authority.Reconnect = kept
	for cycle := 1; cycle <= count; cycle++ {
		openedAt := now.Add(time.Duration(-120+cycle*20+index*5) * time.Second)
		closedAt := openedAt.Add(5 * time.Second)
		authority.Reconnect = append(authority.Reconnect, pause.PauseReconnectInterval{
			ID: task045ID(700 + index*20 + cycle), PauseID: authority.PauseID, RosterID: authority.Scope.RosterID,
			SeriesID: authority.Series.ID, GameID: authority.Game.ID, ParticipantID: presence.ParticipantID,
			PresenceEpoch: int64(cycle * 2), Number: cycle, State: pause.ReconnectStateReconnected,
			OpenedAt: openedAt, Deadline: openedAt.Add(30 * time.Second), ClosedAt: &closedAt, Revision: 2, UpdatedAt: closedAt,
		})
		presence.ConnectedAt = closedAt
		presence.UpdatedAt = closedAt
	}
	presence.State = pause.PresenceStateConnected
	presence.PresenceEpoch = int64(count*2 + 1)
	presence.Revision = int64(count*2 + 1)
	presence.DisconnectedAt = nil
	authority.Counters[index].Used = count
	authority.Counters[index].Revision = int64(count + 1)
	task045RecalculateLifecycleRevisions(authority, now)
}

func task045AddPriorRootForCurrentCycle(authority *reconnectusecase.ReconnectAuthority, index int, now time.Time) {
	presence := &authority.Presence[index]
	for intervalIndex := range authority.Reconnect {
		interval := &authority.Reconnect[intervalIndex]
		if interval.ParticipantID == presence.ParticipantID {
			interval.Number = 2
			interval.PresenceEpoch = 4
		}
	}
	openedAt := now.Add(-60 * time.Second)
	closedAt := now.Add(-50 * time.Second)
	authority.Reconnect = append(authority.Reconnect, pause.PauseReconnectInterval{
		ID: task045ID(740 + index), PauseID: authority.PauseID, RosterID: authority.Scope.RosterID,
		SeriesID: authority.Series.ID, GameID: authority.Game.ID, ParticipantID: presence.ParticipantID,
		PresenceEpoch: 2, Number: 1, State: pause.ReconnectStateReconnected, OpenedAt: openedAt,
		Deadline: now.Add(-40 * time.Second), ClosedAt: &closedAt, Revision: 2, UpdatedAt: closedAt,
	})
	presence.PresenceEpoch = 4
	presence.Revision = 4
	authority.Counters[index].Used = 2
	authority.Counters[index].Revision = 3
	task045RecalculateLifecycleRevisions(authority, now)
}

func task045SetResumedClock(authority *reconnectusecase.ReconnectAuthority, now time.Time) {
	task045RecalculateLifecycleRevisions(authority, now)
}

func task045RecalculateLifecycleRevisions(authority *reconnectusecase.ReconnectAuthority, now time.Time) {
	completed := 0
	for _, interval := range authority.Reconnect {
		if interval.ContinuationNumber == 0 && interval.State == pause.ReconnectStateReconnected {
			completed++
		}
	}
	disconnected := false
	for _, presence := range authority.Presence {
		if presence.State == pause.PresenceStateDisconnected {
			disconnected = true
			break
		}
	}
	authority.GameRevision = 4 + int64(completed*2)
	authority.SeriesRevision = 3 + int64(completed*2)
	authority.GameClock.Revision = 1 + int64(completed*2)
	if disconnected {
		authority.GameRevision++
		authority.SeriesRevision++
		authority.GameClock.FrozenAt = now.Add(-5 * time.Second)
		authority.GameClock.Remaining = 40 * time.Second
		authority.GameClock.OriginalDeadline = authority.GameClock.FrozenAt.Add(authority.GameClock.Remaining)
		authority.GameClock.ResumedAt = nil
		authority.GameClock.ResumedDeadline = nil
		authority.GameClock.Revision++
		return
	}
	if completed == 0 {
		authority.GameClock.OriginalDeadline = now.Add(90 * time.Second)
		authority.GameClock.FrozenAt = time.Time{}
		authority.GameClock.Remaining = 0
		authority.GameClock.ResumedAt = nil
		authority.GameClock.ResumedDeadline = nil
		return
	}
	resumedAt := now.Add(-10 * time.Second)
	resumedDeadline := now.Add(90 * time.Second)
	authority.GameClock.OriginalDeadline = now
	authority.GameClock.FrozenAt = now.Add(-100 * time.Second)
	authority.GameClock.Remaining = 100 * time.Second
	authority.GameClock.ResumedAt = &resumedAt
	authority.GameClock.ResumedDeadline = &resumedDeadline
}

func task045ReplaceCurrentGame(authority *reconnectusecase.ReconnectAuthority, replacementID uuid.UUID, now time.Time) domain.Game {
	prior := authority.Game
	resultID := authority.CurrentGameResultRevisionIDs[0]
	prior.State = domain.GameStateVoid
	prior.ResultReason = domain.GameResultReasonDisconnect
	prior.ResultRevisionID = &resultID
	replacement := domain.Game{ID: replacementID, SlotID: prior.SlotID, AttemptNo: prior.AttemptNo + 1, State: domain.GameStateActive}
	for slotIndex := range authority.Series.Slots {
		if authority.Series.Slots[slotIndex].ID == prior.SlotID {
			authority.Series.Slots[slotIndex].Attempts = []domain.Game{prior, replacement}
		}
	}
	authority.Game = replacement
	priorScoreID := domain.SeriesScoreRevisionID(task045ID(24))
	authority.Series.CurrentScoreRevisionID = &priorScoreID
	authority.GameClock = pause.PauseResumeGameClock{PauseID: authority.PauseID, GameID: replacement.ID,
		OriginalDeadline: now.Add(2 * time.Minute), Revision: 1}
	authority.GameRevision++
	authority.SeriesRevision++
	return prior
}

func task045SettlementIDs(base int) reconnectusecase.SettlementIDs {
	return reconnectusecase.SettlementIDs{GameResultRevisionID: domain.OfficialResultRevisionID(task045ID(base)),
		ScoreRevisionID:        domain.SeriesScoreRevisionID(task045ID(base + 1)),
		SeriesResultRevisionID: domain.OfficialResultRevisionID(task045ID(base + 2)), ReplayRouteID: task045ID(base + 3),
		AuditEventID: task045ID(base + 4), OutboxEventID: task045ID(base + 5), ProjectionRevisionID: task045ID(base + 6)}
}

func task045ID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("45000000-0000-0000-0000-%012d", value))
}
