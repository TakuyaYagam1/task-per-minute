//go:build integration

package integration_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	wscontroller "github.com/TakuyaYagam1/task-per-minute/internal/controller/websocket"
	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
)

func TestE2EPlayerFlow_HTTPJoinWSMatchSubmitAndLeaderboard(t *testing.T) {
	app := startE2EApp(t)
	flag := "FLAG{" + uniq("winner") + "}"
	app.createAdminTask(t, uniq("e2e_easy"), flag)

	alice := app.joinPlayer(t, uniq("alice"))
	bob := app.joinPlayer(t, uniq("bob"))

	aliceConn := app.connectWS(t, alice.SessionToken)
	defer closeWSSilent(aliceConn)
	writeWSEvent(t, aliceConn, wscontroller.EventJoinQueue, nil)
	require.Equal(t, wscontroller.EventQueueJoined, readWSEventType(t, aliceConn, wscontroller.EventQueueJoined).Type)

	bobConn := app.connectWS(t, bob.SessionToken)
	defer closeWSSilent(bobConn)
	writeWSEvent(t, bobConn, wscontroller.EventJoinQueue, nil)

	aliceMatch := readWSEventType(t, aliceConn, wscontroller.EventMatchFound)
	aliceAssigned := readWSEventType(t, aliceConn, wscontroller.EventTaskAssigned)
	bobMatch := readWSEventType(t, bobConn, wscontroller.EventMatchFound)
	bobAssigned := readWSEventType(t, bobConn, wscontroller.EventTaskAssigned)

	duelID := decodeMatchDuelID(t, aliceMatch)
	require.Equal(t, duelID, decodeMatchDuelID(t, bobMatch))
	require.Equal(t, duelID, decodeAssignmentDuelID(t, aliceAssigned))
	require.Equal(t, duelID, decodeAssignmentDuelID(t, bobAssigned))

	writeWSEvent(t, aliceConn, wscontroller.EventFlagSubmit, map[string]any{
		"duel_id": duelID,
		"flag":    flag,
	})

	aliceFinished := readWSEventType(t, aliceConn, wscontroller.EventDuelFinished)
	bobFinished := readWSEventType(t, bobConn, wscontroller.EventDuelFinished)
	require.Equal(t, alice.PlayerID, decodeWinnerID(t, aliceFinished))
	require.Equal(t, alice.PlayerID, decodeWinnerID(t, bobFinished))

	board := app.getLeaderboard(t)
	require.NotEmpty(t, board.Entries)
	require.Equal(t, alice.Username, board.Entries[0].Username)
	require.Equal(t, int32(1), board.Entries[0].TasksSolved)
}

func TestE2EAdminFlow_LoginCreateUploadListDelete(t *testing.T) {
	app := startE2EApp(t)
	token := app.adminLogin(t)

	created := app.createTask(t, token, openapi.CreateTaskRequest{
		Title:       uniq("e2e_admin_task"),
		Description: "created by e2e admin flow",
		Category:    openapi.Forensics,
		Difficulty:  openapi.Easy,
		TimeLimit:   90,
		Flag:        "FLAG{" + uniq("admin") + "}",
		Hints:       defaultTaskHints("e2e admin"),
	})

	upload := app.uploadTaskSource(t, token, created.Id, []byte{'P', 'K', 0x03, 0x04, 'z', 'i', 'p'})
	require.Contains(t, upload.SourceFileUrl, "X-Amz-Signature")

	tasks := app.listTasks(t, token)
	require.True(t, containsTaskID(tasks, created.Id), "admin task list must contain created task")

	app.deleteTask(t, token, created.Id)
	tasks = app.listTasks(t, token)
	require.False(t, containsTaskID(tasks, created.Id), "deleted task must disappear from admin list")
}

func TestE2EReconnectFlow_DisconnectAndResume(t *testing.T) {
	app := startE2EApp(t)
	app.createAdminTask(t, uniq("e2e_reconnect_easy"), "FLAG{"+uniq("reconnect")+"}")

	alice := app.joinPlayer(t, uniq("alice"))
	bob := app.joinPlayer(t, uniq("bob"))

	aliceConn := app.connectWS(t, alice.SessionToken)
	writeWSEvent(t, aliceConn, wscontroller.EventJoinQueue, nil)
	require.Equal(t, wscontroller.EventQueueJoined, readWSEventType(t, aliceConn, wscontroller.EventQueueJoined).Type)

	bobConn := app.connectWS(t, bob.SessionToken)
	defer closeWSSilent(bobConn)
	writeWSEvent(t, bobConn, wscontroller.EventJoinQueue, nil)

	aliceMatch := readWSEventType(t, aliceConn, wscontroller.EventMatchFound)
	require.Equal(t, wscontroller.EventTaskAssigned, readWSEventType(t, aliceConn, wscontroller.EventTaskAssigned).Type)
	bobMatch := readWSEventType(t, bobConn, wscontroller.EventMatchFound)
	require.Equal(t, wscontroller.EventTaskAssigned, readWSEventType(t, bobConn, wscontroller.EventTaskAssigned).Type)
	duelID := decodeMatchDuelID(t, aliceMatch)
	require.Equal(t, duelID, decodeMatchDuelID(t, bobMatch))

	disconnectWS(t, aliceConn)
	disconnected := readWSEventType(t, bobConn, wscontroller.EventOpponentDisconnected)
	require.Equal(t, alice.PlayerID, decodeOpponentDisconnected(t, disconnected).PlayerID)

	aliceReconnect := app.connectWS(t, alice.SessionToken)
	defer closeWSSilent(aliceReconnect)

	resume := readWSEventType(t, aliceReconnect, wscontroller.EventDuelResume)
	reconnected := readWSEventType(t, bobConn, wscontroller.EventOpponentReconnected)
	require.Equal(t, duelID, decodeDuelResume(t, resume).DuelID)
	require.Equal(t, alice.PlayerID, decodeOpponentReconnected(t, reconnected).PlayerID)
}
