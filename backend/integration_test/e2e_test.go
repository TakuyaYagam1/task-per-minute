//go:build integration

package integration_test

import (
	"io"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	wscontroller "github.com/TakuyaYagam1/task-per-minute/internal/controller/websocket"
	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
)

// POST /api/v1/players/join + WS join_queue/flag_submit + GET /api/v1/leaderboard: player duel flow from queue to leaderboard win.
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
	require.Equal(t, wscontroller.EventQueueJoined, readWSEventType(t, bobConn, wscontroller.EventQueueJoined).Type)

	aliceMatch := decodeMatchFound(t, readWSEventType(t, aliceConn, wscontroller.EventMatchFound))
	aliceAssigned := decodeTaskAssigned(t, readWSEventType(t, aliceConn, wscontroller.EventTaskAssigned))
	bobMatch := decodeMatchFound(t, readWSEventType(t, bobConn, wscontroller.EventMatchFound))
	bobAssigned := decodeTaskAssigned(t, readWSEventType(t, bobConn, wscontroller.EventTaskAssigned))

	duelID := aliceMatch.DuelID
	require.Equal(t, aliceMatch.Duel.ID, aliceMatch.DuelID)
	require.Equal(t, duelID, bobMatch.DuelID)
	require.Equal(t, bobMatch.Duel.ID, bobMatch.DuelID)
	require.Equal(t, bob.Username, aliceMatch.OpponentUsername)
	require.Equal(t, alice.Username, bobMatch.OpponentUsername)
	require.Equal(t, duelID, aliceAssigned.DuelID)
	require.Equal(t, duelID, bobAssigned.DuelID)
	require.False(t, aliceAssigned.Deadline.IsZero())
	require.Equal(t, int(aliceAssigned.Task.TimeLimit), aliceAssigned.TimeLimitSeconds)
	require.Equal(t, int(aliceAssigned.Task.TimeLimit), aliceAssigned.Task.TimeLimitSec)
	require.False(t, bobAssigned.Deadline.IsZero())
	require.Equal(t, int(bobAssigned.Task.TimeLimit), bobAssigned.TimeLimitSeconds)
	require.Equal(t, int(bobAssigned.Task.TimeLimit), bobAssigned.Task.TimeLimitSec)

	writeWSEvent(t, aliceConn, wscontroller.EventFlagSubmit, map[string]any{
		"duel_id": duelID,
		"flag":    flag,
	})

	flagResult := decodeFlagResult(t, readWSEventType(t, aliceConn, wscontroller.EventFlagResult))
	require.Equal(t, duelID, flagResult.DuelID)
	require.True(t, flagResult.Correct)

	opponentSolved := decodeOpponentSolved(t, readWSEventType(t, bobConn, wscontroller.EventOpponentSolved))
	require.Equal(t, duelID, opponentSolved.DuelID)
	require.Equal(t, alice.PlayerID, opponentSolved.PlayerID)

	aliceFinished := decodeDuelFinished(t, readWSEventType(t, aliceConn, wscontroller.EventDuelFinished))
	bobFinished := decodeDuelFinished(t, readWSEventType(t, bobConn, wscontroller.EventDuelFinished))
	require.Equal(t, alice.PlayerID, *aliceFinished.WinnerID)
	require.Equal(t, alice.PlayerID, *bobFinished.WinnerID)
	require.Equal(t, duelID, aliceFinished.DuelID)
	require.Equal(t, duelID, bobFinished.DuelID)
	require.NotNil(t, aliceFinished.WinnerUsername)
	require.Equal(t, alice.Username, *aliceFinished.WinnerUsername)
	require.True(t, aliceFinished.YourSolved)
	require.False(t, aliceFinished.OpponentSolved)
	require.False(t, bobFinished.YourSolved)
	require.True(t, bobFinished.OpponentSolved)

	board := app.getLeaderboard(t)
	require.NotEmpty(t, board.Entries)
	require.Equal(t, alice.Username, board.Entries[0].Username)
	require.Equal(t, int32(1), board.Entries[0].TasksSolved)
}

// POST /api/v1/admin/login + admin task CRUD + source upload: admin creates, uploads, lists, and deletes a task.
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

// POST /api/v1/admin/tasks/:id/source + WS task_assigned: uploaded source ZIP is delivered to players as a presigned URL.
func TestE2ESourceFileFlow_TaskAssignedUsesPresignedURL(t *testing.T) {
	app := startE2EApp(t)
	token := app.adminLogin(t)
	flag := "FLAG{" + uniq("source") + "}"
	payload := []byte{'P', 'K', 0x03, 0x04, 's', 'r', 'c'}
	created := app.createTask(t, token, openapi.CreateTaskRequest{
		Title:       uniq("e2e_source_task"),
		Description: "download the archive",
		Category:    openapi.Forensics,
		Difficulty:  openapi.Easy,
		TimeLimit:   90,
		Flag:        flag,
		Hints:       defaultTaskHints("e2e source"),
	})
	upload := app.uploadTaskSource(t, token, created.Id, payload)
	require.Contains(t, upload.SourceFileUrl, "X-Amz-Signature")

	alice := app.joinPlayer(t, uniq("alice"))
	bob := app.joinPlayer(t, uniq("bob"))

	aliceConn := app.connectWS(t, alice.SessionToken)
	defer closeWSSilent(aliceConn)
	writeWSEvent(t, aliceConn, wscontroller.EventJoinQueue, nil)
	require.Equal(t, wscontroller.EventQueueJoined, readWSEventType(t, aliceConn, wscontroller.EventQueueJoined).Type)

	bobConn := app.connectWS(t, bob.SessionToken)
	defer closeWSSilent(bobConn)
	writeWSEvent(t, bobConn, wscontroller.EventJoinQueue, nil)

	require.Equal(t, wscontroller.EventMatchFound, readWSEventType(t, aliceConn, wscontroller.EventMatchFound).Type)
	assigned := decodeTaskAssigned(t, readWSEventType(t, aliceConn, wscontroller.EventTaskAssigned))
	require.NotNil(t, assigned.Task.SourceFileURL)
	require.Contains(t, *assigned.Task.SourceFileURL, "X-Amz-Signature")
	assignedURL, err := url.Parse(*assigned.Task.SourceFileURL)
	require.NoError(t, err)
	require.Equal(t, sharedSeaweed(t).endpoint, assignedURL.Host)

	resp := httpGetWithTimeout(t, *assigned.Task.SourceFileURL)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, payload, body)
}

// WS duel_resume + source ZIP: reconnect emits a freshly presigned source URL instead of reusing an expired assignment URL.
func TestE2ESourceFileFlow_DuelResumeRefreshesPresignedURL(t *testing.T) {
	app := startE2EApp(t)
	token := app.adminLogin(t)
	payload := []byte{'P', 'K', 0x03, 0x04, 'r', 'e', 's', 'u', 'm', 'e'}
	created := app.createTask(t, token, openapi.CreateTaskRequest{
		Title:       uniq("e2e_resume_source"),
		Description: "download the archive after reconnect",
		Category:    openapi.Forensics,
		Difficulty:  openapi.Easy,
		TimeLimit:   3,
		Flag:        "FLAG{" + uniq("resume_source") + "}",
		Hints:       defaultTaskHints("e2e resume source"),
	})
	upload := app.uploadTaskSource(t, token, created.Id, payload)
	require.Contains(t, upload.SourceFileUrl, "X-Amz-Signature")

	alice := app.joinPlayer(t, uniq("alice"))
	bob := app.joinPlayer(t, uniq("bob"))

	aliceConn := app.connectWS(t, alice.SessionToken)
	writeWSEvent(t, aliceConn, wscontroller.EventJoinQueue, nil)
	require.Equal(t, wscontroller.EventQueueJoined, readWSEventType(t, aliceConn, wscontroller.EventQueueJoined).Type)

	bobConn := app.connectWS(t, bob.SessionToken)
	defer closeWSSilent(bobConn)
	writeWSEvent(t, bobConn, wscontroller.EventJoinQueue, nil)

	require.Equal(t, wscontroller.EventMatchFound, readWSEventType(t, aliceConn, wscontroller.EventMatchFound).Type)
	assigned := decodeTaskAssigned(t, readWSEventType(t, aliceConn, wscontroller.EventTaskAssigned))
	require.NotNil(t, assigned.Task.SourceFileURL)

	disconnectWS(t, aliceConn)
	require.Equal(t, alice.PlayerID, decodeOpponentDisconnected(t,
		readWSEventType(t, bobConn, wscontroller.EventOpponentDisconnected)).PlayerID)

	time.Sleep(4 * time.Second)

	aliceReconnect := app.connectWS(t, alice.SessionToken)
	defer closeWSSilent(aliceReconnect)

	resume := decodeDuelResume(t, readWSEventType(t, aliceReconnect, wscontroller.EventDuelResume))
	require.NotNil(t, resume.Task)
	require.NotNil(t, resume.Task.SourceFileURL)
	require.Contains(t, *resume.Task.SourceFileURL, "X-Amz-Signature")
	require.NotEqual(t, *assigned.Task.SourceFileURL, *resume.Task.SourceFileURL)

	resp := httpGetWithTimeout(t, *resume.Task.SourceFileURL)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, payload, body)
}

// WS opponent_disconnected + duel_resume: player disconnect freezes an active duel and reconnect resumes it.
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

// POST /api/v1/admin/tasks + WS task_assigned/hint_unlocked: task hints auto-unlock at 25/50/75% for both duel players.
func TestE2EHintFlow_AutoUnlocksAt25_50_75(t *testing.T) {
	app := startE2EApp(t)

	const hintTimeLimit = 4
	flag := "FLAG{" + uniq("hint") + "}"
	token := app.adminLogin(t)
	hints := defaultTaskHints("e2e hint flow")
	created := app.createTask(t, token, openapi.CreateTaskRequest{
		Title:       uniq("e2e_hint_easy"),
		Description: "duel with auto hints at 25/50/75%",
		Category:    openapi.Web,
		Difficulty:  openapi.Easy,
		TimeLimit:   hintTimeLimit,
		Flag:        flag,
		Hints:       hints,
	})
	require.Equal(t, hintTimeLimit, int(created.TimeLimit))

	alice := app.joinPlayer(t, uniq("alice"))
	bob := app.joinPlayer(t, uniq("bob"))

	aliceConn := app.connectWS(t, alice.SessionToken)
	defer closeWSSilent(aliceConn)
	writeWSEvent(t, aliceConn, wscontroller.EventJoinQueue, nil)
	require.Equal(t, wscontroller.EventQueueJoined, readWSEventType(t, aliceConn, wscontroller.EventQueueJoined).Type)

	bobConn := app.connectWS(t, bob.SessionToken)
	defer closeWSSilent(bobConn)
	writeWSEvent(t, bobConn, wscontroller.EventJoinQueue, nil)

	require.Equal(t, wscontroller.EventMatchFound, readWSEventType(t, aliceConn, wscontroller.EventMatchFound).Type)
	aliceAssigned := readWSEventType(t, aliceConn, wscontroller.EventTaskAssigned)
	require.Equal(t, wscontroller.EventMatchFound, readWSEventType(t, bobConn, wscontroller.EventMatchFound).Type)
	bobAssigned := readWSEventType(t, bobConn, wscontroller.EventTaskAssigned)

	aliceTask := decodeTaskAssigned(t, aliceAssigned)
	bobTask := decodeTaskAssigned(t, bobAssigned)
	require.Len(t, aliceTask.Task.HintSchedule, 3, "task_assigned must advertise 3 scheduled hints")
	require.Len(t, bobTask.Task.HintSchedule, 3, "task_assigned must advertise 3 scheduled hints")

	for index := 1; index <= 3; index++ {
		aliceHint := decodeHintUnlocked(t, readWSEventType(t, aliceConn, wscontroller.EventHintUnlocked))
		bobHint := decodeHintUnlocked(t, readWSEventType(t, bobConn, wscontroller.EventHintUnlocked))
		require.Equal(t, index, aliceHint.HintIndex, "alice hint order must be sequential")
		require.Equal(t, index, bobHint.HintIndex, "bob hint order must be sequential")
		require.Equal(t, hints[index-1], aliceHint.Hint, "alice hint text must match the configured hint")
		require.Equal(t, hints[index-1], bobHint.Hint, "bob hint text must match the configured hint")
	}
}

// WS flag_submit race + GET /api/v1/leaderboard: simultaneous correct flags produce one winner and one leaderboard bump.
func TestE2EConcurrentFlagSubmit_SingleWinnerAndSingleLeaderboardBump(t *testing.T) {
	app := startE2EApp(t)
	flag := "FLAG{" + uniq("race") + "}"
	app.createAdminTask(t, uniq("e2e_race"), flag)

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
	require.Equal(t, wscontroller.EventTaskAssigned, readWSEventType(t, aliceConn, wscontroller.EventTaskAssigned).Type)
	bobMatch := readWSEventType(t, bobConn, wscontroller.EventMatchFound)
	require.Equal(t, wscontroller.EventTaskAssigned, readWSEventType(t, bobConn, wscontroller.EventTaskAssigned).Type)
	duelID := decodeMatchDuelID(t, aliceMatch)
	require.Equal(t, duelID, decodeMatchDuelID(t, bobMatch))

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		writeWSEvent(t, aliceConn, wscontroller.EventFlagSubmit, map[string]any{
			"duel_id": duelID,
			"flag":    flag,
		})
	}()
	go func() {
		defer wg.Done()
		<-start
		writeWSEvent(t, bobConn, wscontroller.EventFlagSubmit, map[string]any{
			"duel_id": duelID,
			"flag":    flag,
		})
	}()
	close(start)
	wg.Wait()

	aliceFinished := readWSEventType(t, aliceConn, wscontroller.EventDuelFinished)
	bobFinished := readWSEventType(t, bobConn, wscontroller.EventDuelFinished)

	aliceWinner := decodeWinnerID(t, aliceFinished)
	bobWinner := decodeWinnerID(t, bobFinished)
	require.Equal(t, aliceWinner, bobWinner, "both clients must observe the same winner_id")
	require.Contains(t, []uuid.UUID{alice.PlayerID, bob.PlayerID}, aliceWinner,
		"winner must be one of the two participants")

	board := app.getLeaderboard(t)
	require.Len(t, board.Entries, 1, "exactly one leaderboard entry is expected after a single duel")
	require.Equal(t, int32(1), board.Entries[0].TasksSolved,
		"leaderboard must be bumped exactly once even though two correct flags raced")

	winnerUsername := alice.Username
	if aliceWinner == bob.PlayerID {
		winnerUsername = bob.Username
	}
	require.Equal(t, winnerUsername, board.Entries[0].Username)
}
