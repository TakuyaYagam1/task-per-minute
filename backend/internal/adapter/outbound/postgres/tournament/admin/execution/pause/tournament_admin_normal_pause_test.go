package pause

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	gamepause "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
)

func TestTournamentAdminNormalConnectedResumeLeavesOnlyDisconnectedGamePaused(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
	firstSeriesID, secondSeriesID := uuid.New(), uuid.New()
	firstGameID, secondGameID := uuid.New(), uuid.New()
	participants := [4]uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	seriesResume := domain.SeriesStateActive
	gameResume := domain.GameStateActive
	paused := gamepause.NormalPauseRecord{Graph: gamepause.PauseGraph{
		Series: []gamepause.PauseSeries{
			{Execution: seriesdomain.Execution{Series: domain.Series{ID: firstSeriesID, FirstParticipantID: participants[0], SecondParticipantID: participants[1], State: domain.SeriesStateTechnicalPause}, ResumeState: &seriesResume}, Revision: 2},
			{Execution: seriesdomain.Execution{Series: domain.Series{ID: secondSeriesID, FirstParticipantID: participants[2], SecondParticipantID: participants[3], State: domain.SeriesStateTechnicalPause}, ResumeState: &seriesResume}, Revision: 2},
		},
		Games: []gamepause.PauseGame{
			{SeriesID: firstSeriesID, Game: domain.Game{ID: firstGameID, State: domain.GameStatePaused}, Revision: 2, ResumeState: &gameResume},
			{SeriesID: secondSeriesID, Game: domain.Game{ID: secondGameID, State: domain.GameStatePaused}, Revision: 2, ResumeState: &gameResume},
		},
		FrozenDeadlines: []gamepause.PauseFrozenDeadline{
			{Kind: gamepause.PauseDeadlineGame, OwnerID: firstGameID, Remaining: time.Minute, Revision: 1},
			{Kind: gamepause.PauseDeadlineGame, OwnerID: secondGameID, Remaining: 2 * time.Minute, Revision: 1},
		},
	}}
	presence := gamepause.PauseResumePresenceRecord{
		Command: gamepause.PauseResumePresenceCommand{Presence: []pausedomain.PausePresence{
			{ParticipantID: participants[0], State: pausedomain.PresenceStateDisconnected},
			{ParticipantID: participants[1], State: pausedomain.PresenceStateConnected},
			{ParticipantID: participants[2], State: pausedomain.PresenceStateConnected},
			{ParticipantID: participants[3], State: pausedomain.PresenceStateConnected},
		}},
		DecidedAt: now,
	}

	result := tournamentAdminNormalConnectedResume(paused, presence)
	if result.Graph.Series[0].Execution.Series.State != domain.SeriesStateTechnicalPause ||
		result.Graph.Games[0].Game.State != domain.GameStatePaused ||
		result.Graph.FrozenDeadlines[0].ResumedDeadline != nil {
		t.Fatalf("disconnected execution resumed: series=%s game=%s clock=%+v",
			result.Graph.Series[0].Execution.Series.State, result.Graph.Games[0].Game.State,
			result.Graph.FrozenDeadlines[0])
	}
	if result.Graph.Series[1].Execution.Series.State != domain.SeriesStateActive ||
		result.Graph.Games[1].Game.State != domain.GameStateActive ||
		result.Graph.FrozenDeadlines[1].ResumedDeadline == nil ||
		!result.Graph.FrozenDeadlines[1].ResumedDeadline.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("connected execution not resumed: series=%s game=%s clock=%+v",
			result.Graph.Series[1].Execution.Series.State, result.Graph.Games[1].Game.State,
			result.Graph.FrozenDeadlines[1])
	}
}

func TestTournamentAdminNormalPausePresenceProjectionRetainsSuspendedSource(t *testing.T) {
	t.Parallel()

	gamePauseID, earlierPauseID, unrelatedPauseID := uuid.New(), uuid.New(), uuid.New()
	firstID, secondID := uuid.New(), uuid.New()
	sourceID, unrelatedID := uuid.New(), uuid.New()
	normalPauseID := uuid.New()
	resume := gamepause.PauseResumeAuthority{Pause: gamepause.NormalPauseRecord{
		PauseID: normalPauseID,
		Graph: gamepause.PauseGraph{
			Counters: []pausedomain.PauseReconnectCounter{
				{PauseID: earlierPauseID, ParticipantID: firstID, Used: 1, Revision: 3},
				{PauseID: gamePauseID, ParticipantID: firstID, Revision: 1},
				{PauseID: gamePauseID, ParticipantID: secondID, Revision: 1},
			},
			Reconnect: []pausedomain.PauseReconnectInterval{
				{ID: sourceID, PauseID: earlierPauseID, ParticipantID: firstID, Revision: 2},
				{ID: unrelatedID, PauseID: unrelatedPauseID, ParticipantID: firstID, Revision: 1},
			},
		},
		SuspendedReconnect: []gamepause.PauseChildRevision{{ID: sourceID, Revision: 1}},
	}}
	resume.Counters = append([]pausedomain.PauseReconnectCounter(nil), resume.Pause.Graph.Counters...)
	resume.Reconnect = append([]pausedomain.PauseReconnectInterval(nil), resume.Pause.Graph.Reconnect...)

	projected := tournamentAdminNormalPausePresenceProjection(resume, gamePauseID)
	if len(projected.Reconnect) != 1 || projected.Reconnect[0].ID != sourceID || projected.Reconnect[0].PauseID != gamePauseID {
		t.Fatalf("projected reconnect = %+v", projected.Reconnect)
	}
	if len(projected.Counters) != 2 || projected.Counters[0].ParticipantID != firstID ||
		projected.Counters[0].PauseID != gamePauseID || projected.Counters[0].Used != 1 ||
		projected.Counters[0].Revision != 3 || projected.Counters[1].ParticipantID != secondID {
		t.Fatalf("projected counters = %+v", projected.Counters)
	}
	if len(projected.Pause.SuspendedReconnect) != 1 || projected.Pause.SuspendedReconnect[0].ID != sourceID {
		t.Fatalf("projected suspended evidence = %+v", projected.Pause.SuspendedReconnect)
	}
}
