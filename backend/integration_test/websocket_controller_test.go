//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	wscontroller "github.com/TakuyaYagam1/task-per-minute/internal/controller/websocket"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
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

func TestWebSocketController_ReconnectTimeoutGivesOpponentWin(t *testing.T) {
	f := newWebSocketFixtureWithReconnectWindow(t, 80*time.Millisecond)
	match := f.matchPlayers(t, 3)
	defer closeWSSilent(match.bobConn)

	disconnectWS(t, match.aliceConn)
	disconnected := readWSEventType(t, match.bobConn, wscontroller.EventOpponentDisconnected)
	require.Equal(t, match.alice.ID, decodeOpponentDisconnected(t, disconnected).PlayerID)

	finished := readWSEventType(t, match.bobConn, wscontroller.EventDuelFinished)
	require.Equal(t, match.bob.ID, decodeWinnerID(t, finished))
	got, err := f.duels.GetByID(context.Background(), match.duelID)
	require.NoError(t, err)
	require.Equal(t, domain.DuelStatusFinished, got.Status)
	require.Equal(t, match.bob.ID, *got.WinnerID)
}

func TestWebSocketController_ThirdDisconnectLosesImmediately(t *testing.T) {
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
	require.Equal(t, match.bob.ID, decodeWinnerID(t, finished))
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
