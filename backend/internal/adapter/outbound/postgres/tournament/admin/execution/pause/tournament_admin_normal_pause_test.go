package pause

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	gamepause "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
)

func TestRejectActiveNormalWavePauseIgnoresChildPauses(t *testing.T) {
	t.Parallel()

	parentID := uuid.New()
	require.NoError(t, rejectActiveNormalWavePause([]sqlc.Pause{{
		ScopeKind: "game_attempt", State: string(gamepause.PauseStateActive),
		ParentPauseID: uuid.NullUUID{UUID: parentID, Valid: true}, Depth: 1,
	}}))
	require.ErrorIs(t, rejectActiveNormalWavePause([]sqlc.Pause{{
		ScopeKind: "wave", State: string(gamepause.PauseStateActive), Depth: 0,
	}}), domain.ErrConflict)
	require.NotErrorIs(t, rejectActiveNormalWavePause(nil), domain.ErrConflict)
}

func TestRepeatNormalWavePauseConflictsBeforeAdoptingChildPauses(t *testing.T) {
	t.Parallel()

	wavePauseID := uuid.New()
	seriesPauseID := uuid.New()
	require.ErrorIs(t, rejectActiveNormalWavePause([]sqlc.Pause{
		{ID: wavePauseID, ScopeKind: "wave", State: string(gamepause.PauseStateActive), Depth: 0},
		{ID: uuid.New(), ScopeKind: "game_attempt", ScopeID: uuid.New(), State: string(gamepause.PauseStateActive),
			ParentPauseID: uuid.NullUUID{UUID: seriesPauseID, Valid: true}, Depth: 1},
	}), domain.ErrConflict)
}

func TestRecoveryGameClockPreservesSubMillisecondFrozenDuration(t *testing.T) {
	t.Parallel()

	frozenAt := time.Date(2026, time.September, 23, 12, 0, 0, 123456000, time.UTC)
	remaining := 40*time.Second + 466*time.Microsecond

	clock, err := recoveryGameClock(sqlc.PauseClock{
		PauseID: uuid.New(), GameAttemptID: uuid.New(),
		OriginalDeadline:  tstz(frozenAt.Add(remaining)),
		FrozenAt:          tstz(frozenAt),
		FrozenRemainingMs: remaining.Milliseconds(),
		Revision:          1,
	})
	require.NoError(t, err)
	require.Equal(t, remaining, clock.Remaining)
}

func TestTournamentAdminNormalPauseFrozenPreservesSubMillisecondFrozenDuration(t *testing.T) {
	t.Parallel()

	gameID := uuid.New()
	frozenAt := time.Date(2026, time.September, 23, 12, 0, 0, 123456000, time.UTC)
	remaining := 40*time.Second + 466*time.Microsecond
	expected := gamepause.PauseFrozenDeadline{
		Kind: gamepause.PauseDeadlineGame, OwnerID: gameID,
		OriginalDeadline: frozenAt.Add(remaining), FrozenAt: frozenAt,
		Remaining: remaining, Revision: 3,
	}

	actual, err := tournamentAdminNormalPauseFrozen([]sqlc.PauseClock{{
		GameAttemptID: gameID, OriginalDeadline: tstz(expected.OriginalDeadline), FrozenAt: tstz(frozenAt),
		FrozenRemainingMs: remaining.Milliseconds(), Revision: expected.Revision,
	}}, nil, []gamepause.PauseFrozenDeadline{expected})
	require.NoError(t, err)
	require.Equal(t, []gamepause.PauseFrozenDeadline{expected}, actual)
}

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

	result, err := tournamentAdminNormalConnectedResume(paused, presence)
	require.NoError(t, err)
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

func TestTournamentAdminNormalConnectedResumeRestoresIndependentSourceBesideWaitingSeries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 24, 12, 0, 0, 0, time.UTC)
	seriesID, waitingSeriesID, gameID, waitingGameID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	participants := [4]uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	remaining := time.Minute + 466*time.Microsecond
	source := &gamepause.PauseGameSourcePause{
		PauseID: uuid.New(), GameID: gameID, SeriesID: seriesID, ScopeKind: "game_attempt", ScopeID: gameID,
		Reason: gamepause.PauseReasonDisconnect, State: gamepause.PauseStateActive,
		CurrentRevisionID: uuid.New(), Revision: 3, DecisionNumber: 1, StartedAt: now.Add(-time.Minute),
		Clock: gamepause.PauseFrozenDeadline{
			Kind: gamepause.PauseDeadlineGame, OwnerID: gameID, Revision: 1,
			OriginalDeadline: now.Add(466 * time.Microsecond), FrozenAt: now.Add(-time.Minute), Remaining: remaining,
		},
		Presence: []gamepause.PausePresenceSnapshot{{ParticipantID: participants[0], State: pausedomain.PresenceStateDisconnected}},
	}
	game := domain.Game{ID: gameID, State: domain.GameStatePaused}
	resumeState := domain.SeriesStateActive
	paused := gamepause.NormalPauseRecord{Graph: gamepause.PauseGraph{
		Series: []gamepause.PauseSeries{
			{Revision: 8, CurrentGameID: &gameID, Execution: seriesdomain.Execution{Series: domain.Series{
				ID: seriesID, State: domain.SeriesStateActive, FirstParticipantID: participants[0], SecondParticipantID: participants[1],
				Slots: []domain.GameSlot{{Attempts: []domain.Game{game}}},
			}}},
			{Revision: 9, CurrentGameID: &waitingGameID, Execution: seriesdomain.Execution{ResumeState: &resumeState, Series: domain.Series{
				ID: waitingSeriesID, State: domain.SeriesStateTechnicalPause, FirstParticipantID: participants[2], SecondParticipantID: participants[3],
			}}},
		},
		Games: []gamepause.PauseGame{
			{Game: game, SeriesID: seriesID, Revision: 7, SourcePause: source},
			{Game: domain.Game{ID: waitingGameID, State: domain.GameStatePaused}, SeriesID: waitingSeriesID, Revision: 5},
		},
	}}
	presence := gamepause.PauseResumePresenceRecord{
		Command: gamepause.PauseResumePresenceCommand{
			Resume: gamepause.PauseResumeCommand{CommandID: uuid.New()},
			Presence: []pausedomain.PausePresence{
				{ParticipantID: participants[0], State: pausedomain.PresenceStateConnected},
				{ParticipantID: participants[1], State: pausedomain.PresenceStateConnected},
				{ParticipantID: participants[2], State: pausedomain.PresenceStateDisconnected},
				{ParticipantID: participants[3], State: pausedomain.PresenceStateConnected},
			},
		}, DecidedAt: now,
	}
	result, err := tournamentAdminNormalConnectedResume(paused, presence)
	require.NoError(t, err)
	got := result.Graph.Games[0]
	require.Equal(t, domain.GameStateActive, got.Game.State)
	require.Equal(t, now.Add(remaining), *got.Deadline)
	require.Equal(t, int64(8), got.Revision)
	require.Equal(t, source.PauseID, got.SourcePause.PauseID)
	require.Equal(t, gamepause.PauseStateResumed, got.SourcePause.State)
	require.Equal(t, int64(4), got.SourcePause.Revision)
	require.Equal(t, int64(2), got.SourcePause.Clock.Revision)
	require.Equal(t, int64(2), got.SourcePause.DecisionNumber)
	require.Equal(t, source.Clock.Remaining, got.SourcePause.Clock.Remaining)
	require.Equal(t, source.Presence, got.SourcePause.Presence)
	require.Equal(t, now.Add(remaining), *got.SourcePause.Clock.ResumedDeadline)
	require.Equal(t, int64(8), result.Graph.Series[0].Revision, "independent Series stays unchanged")
	require.Equal(t, domain.GameStateActive, result.Graph.Series[0].Execution.Series.Slots[0].Attempts[0].State)
	require.Equal(t, domain.GameStatePaused, result.Graph.Games[1].Game.State)
	require.Equal(t, domain.SeriesStateTechnicalPause, result.Graph.Series[1].Execution.Series.State)
	require.Equal(t, gamepause.PauseStateActive, source.State, "source receipt must not be mutated")
	require.Nil(t, source.Clock.ResumedAt)
	require.Equal(t, domain.GameStatePaused, paused.Graph.Series[0].Execution.Series.Slots[0].Attempts[0].State)
	got.SourcePause.Presence[0].PresenceEpoch++
	require.Zero(t, source.Presence[0].PresenceEpoch, "source snapshots need an independent clone")
}
