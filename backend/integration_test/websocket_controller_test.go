//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	wscontroller "github.com/TakuyaYagam1/task-per-minute/internal/controller/websocket"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
)

func TestWebSocketController_MatchAndFlagSubmit(t *testing.T) {
	f := newWebSocketFixture(t)
	ctx := context.Background()

	f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 90)
	alice := f.joinPlayer(t, uniq("alice"))
	bob := f.joinPlayer(t, uniq("bob"))

	aliceConn := f.connect(t, *alice.SessionToken)
	defer closeWS(t, aliceConn)
	bobConn := f.connect(t, *bob.SessionToken)
	defer closeWS(t, bobConn)

	writeWSEvent(t, aliceConn, wscontroller.EventJoinQueue, nil)
	require.Equal(t, wscontroller.EventQueueJoined, readWSEventType(t, aliceConn, wscontroller.EventQueueJoined).Type)

	writeWSEvent(t, bobConn, wscontroller.EventJoinQueue, nil)

	aliceMatch := readWSEventType(t, aliceConn, wscontroller.EventMatchFound)
	aliceAssigned := readWSEventType(t, aliceConn, wscontroller.EventTaskAssigned)
	bobMatch := readWSEventType(t, bobConn, wscontroller.EventMatchFound)
	bobAssigned := readWSEventType(t, bobConn, wscontroller.EventTaskAssigned)

	duelID := decodeMatchDuelID(t, aliceMatch)
	require.Equal(t, duelID, decodeMatchDuelID(t, bobMatch))
	require.Equal(t, duelID, decodeAssignmentDuelID(t, aliceAssigned))
	require.Equal(t, duelID, decodeAssignmentDuelID(t, bobAssigned))

	aliceTask, err := f.duels.GetPlayerTask(ctx, duelID, alice.ID)
	require.NoError(t, err)

	writeWSEvent(t, aliceConn, wscontroller.EventFlagSubmit, map[string]any{
		"duel_id": duelID,
		"flag":    aliceTask.Flag,
	})

	aliceFinished := readWSEventType(t, aliceConn, wscontroller.EventDuelFinished)
	bobFinished := readWSEventType(t, bobConn, wscontroller.EventDuelFinished)
	require.Equal(t, alice.ID, decodeWinnerID(t, aliceFinished))
	require.Equal(t, alice.ID, decodeWinnerID(t, bobFinished))

	got, err := f.duels.GetByID(ctx, duelID)
	require.NoError(t, err)
	require.Equal(t, domain.DuelStatusFinished, got.Status)
	require.NotNil(t, got.WinnerID)
	require.Equal(t, alice.ID, *got.WinnerID)

	require.Eventually(t, func() bool {
		_, ok := f.hubs.Get(duelID)
		return !ok
	}, time.Second, 10*time.Millisecond)
}

func TestWebSocketController_DeletedPlayerSessionCannotConnect(t *testing.T) {
	f := newWebSocketFixture(t)
	ctx := context.Background()

	player := f.joinPlayer(t, uniq("deleted_ws"))
	sessionToken := *player.SessionToken
	require.NoError(t, f.players.SoftDeleteAdminPlayer(ctx, player.ID, "deleted_"+uuid.NewString()[:8], time.Now().UTC()))

	dialCtx, cancel := context.WithTimeout(ctx, wsTestTimeout)
	defer cancel()
	conn, resp, err := coderws.Dial(dialCtx, wsEndpoint(f.httpServer.URL), wsDialOptions(sessionToken))
	if conn != nil {
		closeWSSilent(conn)
	}
	require.Error(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestWebSocketController_HintUnlocksInOrder(t *testing.T) {
	f := newIsolatedWebSocketFixtureWithReconnectWindow(t, duelusecase.DefaultReconnectWindow)

	f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 4)
	alice := f.joinPlayer(t, uniq("alice"))
	bob := f.joinPlayer(t, uniq("bob"))

	aliceConn := f.connect(t, *alice.SessionToken)
	defer closeWSSilent(aliceConn)
	bobConn := f.connect(t, *bob.SessionToken)
	defer closeWSSilent(bobConn)

	writeWSEvent(t, aliceConn, wscontroller.EventJoinQueue, nil)
	require.Equal(t, wscontroller.EventQueueJoined, readWSEventType(t, aliceConn, wscontroller.EventQueueJoined).Type)
	writeWSEvent(t, bobConn, wscontroller.EventJoinQueue, nil)

	require.Equal(t, wscontroller.EventMatchFound, readWSEventType(t, aliceConn, wscontroller.EventMatchFound).Type)
	assigned := decodeTaskAssigned(t, readWSEventType(t, aliceConn, wscontroller.EventTaskAssigned))
	require.Len(t, assigned.Task.HintSchedule, 3)

	for want := 1; want <= 3; want++ {
		event := readWSEventType(t, aliceConn, wscontroller.EventHintUnlocked)
		hint := decodeHintUnlocked(t, event)
		require.Equal(t, assigned.DuelID, hint.DuelID)
		require.Equal(t, assigned.Task.ID, hint.TaskID)
		require.Equal(t, want, hint.HintIndex)
		require.NotEmpty(t, hint.Hint)
	}
}

func TestWebSocketController_ReconnectFreezesHints(t *testing.T) {
	f := newIsolatedWebSocketFixtureWithReconnectWindow(t, 2*time.Second)
	match := f.matchPlayers(t, 4)
	defer closeWSSilent(match.bobConn)

	disconnectWS(t, match.aliceConn)
	require.Equal(t, match.alice.ID, decodeOpponentDisconnected(t,
		readWSEventType(t, match.bobConn, wscontroller.EventOpponentDisconnected)).PlayerID)

	time.Sleep(1200 * time.Millisecond)

	aliceReconnect := f.connect(t, *match.alice.SessionToken)
	defer closeWSSilent(aliceReconnect)

	resume := decodeDuelResume(t, readWSEventType(t, aliceReconnect, wscontroller.EventDuelResume))
	require.Equal(t, match.duelID, resume.DuelID)
	require.NotNil(t, resume.Task)
	require.Empty(t, resume.Task.UnlockedHints)
	require.Len(t, resume.Task.HintSchedule, 3)
	require.True(t, resume.Task.HintSchedule[0].UnlockAt.After(time.Now()))

	firstHint := decodeHintUnlocked(t, readWSEventType(t, aliceReconnect, wscontroller.EventHintUnlocked))
	require.Equal(t, 1, firstHint.HintIndex)
	require.Equal(t, resume.Task.ID, firstHint.TaskID)
}

func TestWebSocketController_ReconnectRestoresTaskWithoutHintSnapshot(t *testing.T) {
	f := newIsolatedWebSocketFixtureWithReconnectWindow(t, 2*time.Second)
	match := f.matchPlayers(t, 90)
	defer closeWSSilent(match.bobConn)

	require.True(t, f.hints.StopDuel(match.duelID), "test setup should drop in-memory hint snapshot")

	disconnectWS(t, match.aliceConn)
	require.Equal(t, match.alice.ID, decodeOpponentDisconnected(t,
		readWSEventType(t, match.bobConn, wscontroller.EventOpponentDisconnected)).PlayerID)

	aliceReconnect := f.connect(t, *match.alice.SessionToken)
	defer closeWSSilent(aliceReconnect)

	resume := decodeDuelResume(t, readWSEventType(t, aliceReconnect, wscontroller.EventDuelResume))
	require.Equal(t, match.duelID, resume.DuelID)
	require.NotNil(t, resume.Task)
	require.NotEqual(t, uuid.Nil, resume.Task.ID)
	require.Len(t, resume.Task.HintSchedule, 3)
	require.Empty(t, resume.Task.UnlockedHints)
}

func TestWebSocketController_UnknownEventAndInvalidToken(t *testing.T) {
	f := newWebSocketFixture(t)
	player := f.joinPlayer(t, uniq("alice"))

	resp, err := http.Get(f.httpServer.URL + "/ws?token=" + uuid.NewString())
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	conn := f.connect(t, *player.SessionToken)
	defer closeWS(t, conn)

	writeWSEvent(t, conn, "definitely_unknown", nil)
	event := readWSEventType(t, conn, wscontroller.EventError)
	require.Equal(t, wscontroller.ErrorUnknownEvent, event.Code)
}

func TestWebSocketController_ReconnectResumesDuel(t *testing.T) {
	f := newWebSocketFixtureWithReconnectWindow(t, 500*time.Millisecond)
	ctx := context.Background()
	match := f.matchPlayers(t, 3)
	defer closeWSSilent(match.bobConn)

	original, err := f.duels.GetByID(ctx, match.duelID)
	require.NoError(t, err)

	disconnectWS(t, match.aliceConn)
	disconnected := readWSEventType(t, match.bobConn, wscontroller.EventOpponentDisconnected)
	require.Equal(t, match.alice.ID, decodeOpponentDisconnected(t, disconnected).PlayerID)

	time.Sleep(50 * time.Millisecond)
	aliceReconnect := f.connect(t, *match.alice.SessionToken)
	defer closeWSSilent(aliceReconnect)

	resume := readWSEventType(t, aliceReconnect, wscontroller.EventDuelResume)
	reconnected := readWSEventType(t, match.bobConn, wscontroller.EventOpponentReconnected)
	resumePayload := decodeDuelResume(t, resume)
	require.Equal(t, match.duelID, resumePayload.DuelID)
	require.Equal(t, match.alice.ID, decodeOpponentReconnected(t, reconnected).PlayerID)

	updated, err := f.duels.GetByID(ctx, match.duelID)
	require.NoError(t, err)
	require.True(t, updated.Deadline.After(original.Deadline))
	require.WithinDuration(t, updated.Deadline, resumePayload.Deadline, time.Second)

	aliceTask, err := f.duels.GetPlayerTask(ctx, match.duelID, match.alice.ID)
	require.NoError(t, err)
	writeWSEvent(t, aliceReconnect, wscontroller.EventFlagSubmit, map[string]any{
		"duel_id": match.duelID,
		"flag":    aliceTask.Flag,
	})
	require.Equal(t, match.alice.ID, decodeWinnerID(t, readWSEventType(t, aliceReconnect, wscontroller.EventDuelFinished)))
	require.Equal(t, match.alice.ID, decodeWinnerID(t, readWSEventType(t, match.bobConn, wscontroller.EventDuelFinished)))
}

func TestWebSocketController_DoubleDisconnectPartialReconnectWaitsForOpponent(t *testing.T) {
	f := newWebSocketFixtureWithReconnectWindow(t, time.Second)
	ctx := context.Background()
	match := f.matchPlayers(t, 10)

	original, err := f.duels.GetByID(ctx, match.duelID)
	require.NoError(t, err)

	disconnectWS(t, match.aliceConn)
	require.Equal(t, match.alice.ID, decodeOpponentDisconnected(t,
		readWSEventType(t, match.bobConn, wscontroller.EventOpponentDisconnected)).PlayerID)
	disconnectWS(t, match.bobConn)

	aliceReconnect := f.connect(t, *match.alice.SessionToken)
	defer closeWSSilent(aliceReconnect)

	aliceResume := decodeDuelResume(t, readWSEventType(t, aliceReconnect, wscontroller.EventDuelResume))
	require.Equal(t, match.duelID, aliceResume.DuelID)
	require.NotNil(t, aliceResume.Task)
	require.True(t, aliceResume.OpponentDisconnected)
	require.NotNil(t, aliceResume.OpponentReconnectDeadline)

	waiting := decodeOpponentDisconnected(t, readWSEventType(t, aliceReconnect, wscontroller.EventOpponentDisconnected))
	require.Equal(t, match.duelID, waiting.DuelID)
	require.Equal(t, match.bob.ID, waiting.PlayerID)
	require.WithinDuration(t, *aliceResume.OpponentReconnectDeadline, waiting.ReconnectDeadline, time.Second)

	stillFrozen, err := f.duels.GetByID(ctx, match.duelID)
	require.NoError(t, err)
	require.Equal(t, original.Deadline, stillFrozen.Deadline, "deadline must stay frozen until the last player returns")

	bobReconnect := f.connect(t, *match.bob.SessionToken)
	defer closeWSSilent(bobReconnect)

	bobResume := decodeDuelResume(t, readWSEventType(t, bobReconnect, wscontroller.EventDuelResume))
	reconnected := decodeOpponentReconnected(t, readWSEventType(t, aliceReconnect, wscontroller.EventOpponentReconnected))
	require.Equal(t, match.duelID, bobResume.DuelID)
	require.False(t, bobResume.OpponentDisconnected)
	require.Equal(t, match.duelID, reconnected.DuelID)
	require.Equal(t, match.bob.ID, reconnected.PlayerID)

	updated, err := f.duels.GetByID(ctx, match.duelID)
	require.NoError(t, err)
	require.True(t, updated.Deadline.After(original.Deadline))
	require.WithinDuration(t, updated.Deadline, bobResume.Deadline, time.Second)
}

func TestWebSocketController_ConnectionReplacementDuringDuelReceivesResume(t *testing.T) {
	f := newWebSocketFixtureWithReconnectWindow(t, 500*time.Millisecond)
	ctx := context.Background()
	match := f.matchPlayers(t, 30)
	defer closeWSSilent(match.aliceConn)
	defer closeWSSilent(match.bobConn)

	active, err := f.duels.GetActiveByPlayerID(ctx, match.alice.ID)
	require.NoError(t, err)
	require.NotNil(t, active)

	aliceReplacement := f.connect(t, *match.alice.SessionToken)
	defer closeWSSilent(aliceReplacement)

	resume := decodeDuelResume(t, readWSEventType(t, aliceReplacement, wscontroller.EventDuelResume))
	require.Equal(t, match.duelID, resume.DuelID)
	require.NotNil(t, resume.Task)

	aliceTask, err := f.duels.GetPlayerTask(ctx, match.duelID, match.alice.ID)
	require.NoError(t, err)
	writeWSEvent(t, aliceReplacement, wscontroller.EventFlagSubmit, map[string]any{
		"duel_id": match.duelID,
		"flag":    aliceTask.Flag,
	})

	require.Equal(t, match.alice.ID, decodeWinnerID(t, readWSEventType(t, match.bobConn, wscontroller.EventDuelFinished)))
	require.Equal(t, match.alice.ID, decodeWinnerID(t, readWSEventType(t, aliceReplacement, wscontroller.EventDuelFinished)))
}

func TestWebSocketController_StaleSessionTokenCannotSendEvents(t *testing.T) {
	f := newWebSocketFixture(t)
	player := f.joinPlayer(t, uniq("alice"))

	conn := f.connect(t, *player.SessionToken)
	defer closeWSSilent(conn)

	refreshed := f.joinPlayer(t, player.Username)
	require.NotEqual(t, *player.SessionToken, *refreshed.SessionToken)

	writeWSEvent(t, conn, wscontroller.EventPing, nil)
	event := readWSEventType(t, conn, wscontroller.EventError)
	require.Equal(t, string(apperr.CodeInvalidSession), event.Code)
}

func TestWebSocketController_QueuedSessionRotationPreventsStaleMatch(t *testing.T) {
	f := newWebSocketFixture(t)
	ctx := context.Background()

	f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 90)
	alice := f.joinPlayer(t, uniq("alice"))
	aliceConn := f.connect(t, *alice.SessionToken)
	defer closeWSSilent(aliceConn)

	writeWSEvent(t, aliceConn, wscontroller.EventJoinQueue, nil)
	require.Equal(t, wscontroller.EventQueueJoined, readWSEventType(t, aliceConn, wscontroller.EventQueueJoined).Type)

	refreshedAlice := f.joinPlayer(t, alice.Username)
	require.NotEqual(t, *alice.SessionToken, *refreshedAlice.SessionToken)
	require.Equal(t, domain.PlayerStatusIdle, refreshedAlice.Status)

	bob := f.joinPlayer(t, uniq("bob"))
	bobConn := f.connect(t, *bob.SessionToken)
	defer closeWSSilent(bobConn)

	writeWSEvent(t, bobConn, wscontroller.EventJoinQueue, nil)
	require.Equal(t, wscontroller.EventQueueJoined, readWSEventType(t, bobConn, wscontroller.EventQueueJoined).Type)

	active, err := f.duels.GetActiveByPlayerID(ctx, bob.ID)
	require.NoError(t, err)
	require.Nil(t, active, "bob must not be matched against alice's stale queued session")

	bobCurrent, err := f.players.GetByID(ctx, bob.ID)
	require.NoError(t, err)
	require.Equal(t, domain.PlayerStatusQueued, bobCurrent.Status)

	writeWSEvent(t, aliceConn, wscontroller.EventPing, nil)
	stale := readWSEventType(t, aliceConn, wscontroller.EventError)
	require.Equal(t, string(apperr.CodeInvalidSession), stale.Code)
}

func TestWebSocketController_ReconnectTimeoutDrawsDuel(t *testing.T) {
	f := newWebSocketFixtureWithReconnectWindow(t, 80*time.Millisecond)
	match := f.matchPlayers(t, 3)
	defer closeWSSilent(match.bobConn)

	disconnectWS(t, match.aliceConn)
	disconnected := readWSEventType(t, match.bobConn, wscontroller.EventOpponentDisconnected)
	require.Equal(t, match.alice.ID, decodeOpponentDisconnected(t, disconnected).PlayerID)

	finished := readWSEventType(t, match.bobConn, wscontroller.EventDuelFinished)
	require.Equal(t, uuid.Nil, decodeWinnerID(t, finished))
	got, err := f.duels.GetByID(context.Background(), match.duelID)
	require.NoError(t, err)
	require.Equal(t, domain.DuelStatusFinished, got.Status)
	require.Nil(t, got.WinnerID)

	scores, err := f.boardStore.WinScores(context.Background())
	require.NoError(t, err)
	require.Empty(t, scores)
}

func TestWebSocketController_FlagSubmitPausedDuringReconnect(t *testing.T) {
	f := newWebSocketFixtureWithReconnectWindow(t, 500*time.Millisecond)
	ctx := context.Background()
	match := f.matchPlayers(t, 5)
	defer closeWSSilent(match.bobConn)

	bobTask, err := f.duels.GetPlayerTask(ctx, match.duelID, match.bob.ID)
	require.NoError(t, err)

	disconnectWS(t, match.aliceConn)
	disconnected := readWSEventType(t, match.bobConn, wscontroller.EventOpponentDisconnected)
	require.Equal(t, match.alice.ID, decodeOpponentDisconnected(t, disconnected).PlayerID)

	writeWSEvent(t, match.bobConn, wscontroller.EventFlagSubmit, map[string]any{
		"duel_id": match.duelID,
		"flag":    bobTask.Flag,
	})
	paused := readWSEventType(t, match.bobConn, wscontroller.EventError)
	require.Equal(t, wscontroller.ErrorDuelPaused, paused.Code)
	require.Equal(t, "duel is paused while a player reconnects", paused.Message)

	active, err := f.duels.GetByID(ctx, match.duelID)
	require.NoError(t, err)
	require.Equal(t, domain.DuelStatusActive, active.Status)
	require.Nil(t, active.WinnerID)
	scores, err := f.boardStore.WinScores(ctx)
	require.NoError(t, err)
	require.Empty(t, scores)

	aliceReconnect := f.connect(t, *match.alice.SessionToken)
	defer closeWSSilent(aliceReconnect)
	resume := decodeDuelResume(t, readWSEventType(t, aliceReconnect, wscontroller.EventDuelResume))
	reconnected := decodeOpponentReconnected(t, readWSEventType(t, match.bobConn, wscontroller.EventOpponentReconnected))
	require.Equal(t, match.duelID, resume.DuelID)
	require.Equal(t, match.alice.ID, reconnected.PlayerID)

	writeWSEvent(t, match.bobConn, wscontroller.EventFlagSubmit, map[string]any{
		"duel_id": match.duelID,
		"flag":    bobTask.Flag,
	})
	require.Equal(t, match.bob.ID, decodeWinnerID(t, readWSEventType(t, match.bobConn, wscontroller.EventDuelFinished)))
	require.Equal(t, match.bob.ID, decodeWinnerID(t, readWSEventType(t, aliceReconnect, wscontroller.EventDuelFinished)))
}

func TestWebSocketController_SurrenderFinishesDuelForOpponentWithoutLeaderboard(t *testing.T) {
	f := newWebSocketFixture(t)
	ctx := context.Background()
	match := f.matchPlayers(t, 30)
	defer closeWSSilent(match.aliceConn)
	defer closeWSSilent(match.bobConn)

	writeWSEvent(t, match.aliceConn, wscontroller.EventSurrender, nil)

	aliceFinished := decodeDuelFinished(t, readWSEventType(t, match.aliceConn, wscontroller.EventDuelFinished))
	bobFinished := decodeDuelFinished(t, readWSEventType(t, match.bobConn, wscontroller.EventDuelFinished))
	require.NotNil(t, aliceFinished.WinnerID)
	require.NotNil(t, bobFinished.WinnerID)
	require.Equal(t, match.bob.ID, *aliceFinished.WinnerID)
	require.Equal(t, match.bob.ID, *bobFinished.WinnerID)
	require.False(t, aliceFinished.YourSolved)
	require.False(t, bobFinished.YourSolved)

	got, err := f.duels.GetByID(ctx, match.duelID)
	require.NoError(t, err)
	require.Equal(t, domain.DuelStatusFinished, got.Status)
	require.NotNil(t, got.WinnerID)
	require.Equal(t, match.bob.ID, *got.WinnerID)

	aliceActive, err := f.duels.GetActiveByPlayerID(ctx, match.alice.ID)
	require.NoError(t, err)
	require.Nil(t, aliceActive, "surrendering player must not restore the finished duel")
	bobActive, err := f.duels.GetActiveByPlayerID(ctx, match.bob.ID)
	require.NoError(t, err)
	require.Nil(t, bobActive, "opponent must not restore the finished duel")

	alice, err := f.players.GetByID(ctx, match.alice.ID)
	require.NoError(t, err)
	require.Equal(t, domain.PlayerStatusIdle, alice.Status)
	bob, err := f.players.GetByID(ctx, match.bob.ID)
	require.NoError(t, err)
	require.Equal(t, domain.PlayerStatusIdle, bob.Status)

	scores, err := f.boardStore.WinScores(ctx)
	require.NoError(t, err)
	require.Empty(t, scores)

	aliceHistory, err := f.history.ListSolvedTaskIDs(ctx, match.alice.ID)
	require.NoError(t, err)
	require.Empty(t, aliceHistory)
	bobHistory, err := f.history.ListSolvedTaskIDs(ctx, match.bob.ID)
	require.NoError(t, err)
	require.Empty(t, bobHistory)

	aliceTask, err := f.duels.GetDuelPlayerTask(ctx, match.duelID, match.alice.ID)
	require.NoError(t, err)
	require.False(t, aliceTask.Solved)
	bobTask, err := f.duels.GetDuelPlayerTask(ctx, match.duelID, match.bob.ID)
	require.NoError(t, err)
	require.False(t, bobTask.Solved)

	writeWSEvent(t, match.aliceConn, wscontroller.EventSurrender, map[string]any{
		"duel_id": match.duelID,
	})
	time.Sleep(50 * time.Millisecond)
	scores, err = f.boardStore.WinScores(ctx)
	require.NoError(t, err)
	require.Empty(t, scores, "replayed surrender after finish must not create a leaderboard bump")
}

func TestWebSocketController_ThirdDisconnectDrawsImmediately(t *testing.T) {
	f := newWebSocketFixtureWithReconnectWindow(t, 500*time.Millisecond)
	match := f.matchPlayers(t, 5)
	defer closeWSSilent(match.bobConn)

	aliceConn := match.aliceConn
	for i := 0; i < 2; i++ {
		disconnectWS(t, aliceConn)
		require.Equal(t, match.alice.ID, decodeOpponentDisconnected(t,
			readWSEventType(t, match.bobConn, wscontroller.EventOpponentDisconnected)).PlayerID)

		aliceConn = f.connect(t, *match.alice.SessionToken)
		require.Equal(t, match.duelID, decodeDuelResume(t,
			readWSEventType(t, aliceConn, wscontroller.EventDuelResume)).DuelID)
		require.Equal(t, match.alice.ID, decodeOpponentReconnected(t,
			readWSEventType(t, match.bobConn, wscontroller.EventOpponentReconnected)).PlayerID)
	}

	disconnectWS(t, aliceConn)
	finished := readWSEventType(t, match.bobConn, wscontroller.EventDuelFinished)
	require.Equal(t, uuid.Nil, decodeWinnerID(t, finished))
}

func TestWebSocketController_DoubleDisconnectDraw(t *testing.T) {
	f := newWebSocketFixtureWithReconnectWindow(t, 80*time.Millisecond)
	match := f.matchPlayers(t, 3)

	disconnectWS(t, match.aliceConn)
	require.Equal(t, match.alice.ID, decodeOpponentDisconnected(t,
		readWSEventType(t, match.bobConn, wscontroller.EventOpponentDisconnected)).PlayerID)
	disconnectWS(t, match.bobConn)

	require.Eventually(t, func() bool {
		got, err := f.duels.GetByID(context.Background(), match.duelID)
		return err == nil &&
			got.Status == domain.DuelStatusFinished &&
			got.WinnerID == nil
	}, time.Second, 10*time.Millisecond)
}

func scoreUsernames(scores []usecase.LeaderboardScore) []string {
	usernames := make([]string, 0, len(scores))
	for _, score := range scores {
		usernames = append(usernames, score.Username)
	}
	return usernames
}
