//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	wscontroller "github.com/TakuyaYagam1/task-per-minute/internal/controller/websocket"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	redisrepo "github.com/TakuyaYagam1/task-per-minute/internal/repo/redis"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
	"github.com/TakuyaYagam1/task-per-minute/pkg/clock"
)

type websocketFixture struct {
	*duelFixture
	playerUC   *playerusecase.PlayerUsecase
	boardStore *redisrepo.LeaderboardRedis
	hubs       *wscontroller.HubRegistry
	httpServer *httptest.Server
}

func newWebSocketFixture(t *testing.T) *websocketFixture {
	return newWebSocketFixtureWithReconnectWindow(t, duelusecase.DefaultReconnectWindow)
}

func newWebSocketFixtureWithReconnectWindow(t *testing.T, reconnectWindow time.Duration) *websocketFixture {
	t.Helper()
	return newWebSocketFixtureFromDuelFixture(t, newDuelFixture(), reconnectWindow)
}

func newIsolatedWebSocketFixtureWithReconnectWindow(t *testing.T, reconnectWindow time.Duration) *websocketFixture {
	t.Helper()
	return newWebSocketFixtureFromDuelFixture(t, newIsolatedDuelFixture(t), reconnectWindow)
}

func newWebSocketFixtureFromDuelFixture(
	t *testing.T,
	f *duelFixture,
	reconnectWindow time.Duration,
) *websocketFixture {
	t.Helper()

	redisClient := sharedRedis(t).client
	queue := redisrepo.NewMatchmakingRedis(redisClient, "matchmaking:"+uniq("q"))
	board := redisrepo.NewLeaderboardRedis(redisClient, "leaderboard:"+uniq("z"))
	playerUC := playerusecase.NewPlayerUsecase(f.mgr, f.players, f.duels)
	matchmaking := duelusecase.NewMatchmakingUsecase(
		f.mgr,
		queue,
		f.players,
		f.tasks,
		f.history,
		f.duels,
		nil,
		clock.Real{},
	)
	timers := duelusecase.NewTimerRegistry(f.mgr, f.duels, f.players, nil)
	hints := duelusecase.NewHintScheduler(clock.Real{}, nil)
	flags := duelusecase.NewFlagSubmitUsecase(
		f.mgr,
		f.duels,
		f.players,
		f.history,
		board,
		clock.Real{},
		timers,
	)
	hubs := wscontroller.NewHubRegistry()
	server := wscontroller.NewServer(
		f.players,
		matchmaking,
		flags,
		hubs,
		wscontroller.WithHubCloseDelay(20*time.Millisecond),
		wscontroller.WithHintScheduler(hints),
	)
	reconnect := duelusecase.NewReconnectManager(
		f.mgr,
		f.duels,
		f.players,
		wscontroller.NewPauseableDuelTimers(timers, hints),
		server.Broadcaster(),
		nil,
		duelusecase.WithReconnectWindow(reconnectWindow),
		duelusecase.WithLeaderboardStore(board),
	)
	wscontroller.WithReconnectManager(reconnect)(server)
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)

	return &websocketFixture{
		duelFixture: f,
		playerUC:    playerUC,
		boardStore:  board,
		hubs:        hubs,
		httpServer:  httpServer,
	}
}

type wsMatch struct {
	alice     *domain.Player
	bob       *domain.Player
	aliceConn *coderws.Conn
	bobConn   *coderws.Conn
	duelID    uuid.UUID
}

func (f *websocketFixture) matchPlayers(t *testing.T, taskTimeLimit int) wsMatch {
	t.Helper()
	f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, taskTimeLimit)
	alice := f.joinPlayer(t, uniq("alice"))
	bob := f.joinPlayer(t, uniq("bob"))

	aliceConn := f.connect(t, *alice.SessionToken)
	bobConn := f.connect(t, *bob.SessionToken)

	writeWSEvent(t, aliceConn, wscontroller.EventJoinQueue, nil)
	require.Equal(t, wscontroller.EventQueueJoined, readWSEventType(t, aliceConn, wscontroller.EventQueueJoined).Type)
	writeWSEvent(t, bobConn, wscontroller.EventJoinQueue, nil)
	require.Equal(t, wscontroller.EventQueueJoined, readWSEventType(t, bobConn, wscontroller.EventQueueJoined).Type)

	aliceMatch := readWSEventType(t, aliceConn, wscontroller.EventMatchFound)
	require.Equal(t, wscontroller.EventTaskAssigned, readWSEventType(t, aliceConn, wscontroller.EventTaskAssigned).Type)
	bobMatch := readWSEventType(t, bobConn, wscontroller.EventMatchFound)
	require.Equal(t, wscontroller.EventTaskAssigned, readWSEventType(t, bobConn, wscontroller.EventTaskAssigned).Type)

	duelID := decodeMatchDuelID(t, aliceMatch)
	require.Equal(t, duelID, decodeMatchDuelID(t, bobMatch))

	return wsMatch{
		alice:     alice,
		bob:       bob,
		aliceConn: aliceConn,
		bobConn:   bobConn,
		duelID:    duelID,
	}
}

func (f *websocketFixture) joinPlayer(t *testing.T, username string) *domain.Player {
	t.Helper()
	player, err := f.playerUC.Join(context.Background(), username)
	require.NoError(t, err)
	require.NotNil(t, player.SessionToken)
	return player
}

func (f *websocketFixture) connect(t *testing.T, token uuid.UUID) *coderws.Conn {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, wsURL(f.httpServer.URL, token), nil)
	require.NoError(t, err)
	return conn
}

type wsTestEvent struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Code    string          `json:"code,omitempty"`
	Message string          `json:"message,omitempty"`
}

func writeWSEvent(t *testing.T, conn *coderws.Conn, typ string, payload any) {
	t.Helper()

	data, err := json.Marshal(wscontroller.Event{Type: typ, Payload: payload})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, coderws.MessageText, data))
}

func readWSEventType(t *testing.T, conn *coderws.Conn, typ string) wsTestEvent {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for websocket event %q", typ)
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Until(deadline))
		msgType, data, err := conn.Read(ctx)
		cancel()
		require.NoError(t, err)
		require.Equal(t, coderws.MessageText, msgType)

		var event wsTestEvent
		require.NoError(t, json.Unmarshal(data, &event))
		if event.Type == typ {
			return event
		}
		t.Logf("skipping websocket event %q while waiting for %q: code=%q message=%q", event.Type, typ, event.Code, event.Message)
	}
}

func decodeMatchDuelID(t *testing.T, event wsTestEvent) uuid.UUID {
	t.Helper()
	var payload wscontroller.MatchFoundPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	return payload.Duel.ID
}

func decodeMatchFound(t *testing.T, event wsTestEvent) wscontroller.MatchFoundPayload {
	t.Helper()
	var payload wscontroller.MatchFoundPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	return payload
}

func decodeAssignmentDuelID(t *testing.T, event wsTestEvent) uuid.UUID {
	t.Helper()
	var payload wscontroller.TaskAssignedPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	return payload.DuelID
}

func decodeTaskAssigned(t *testing.T, event wsTestEvent) wscontroller.TaskAssignedPayload {
	t.Helper()
	var payload wscontroller.TaskAssignedPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	return payload
}

func decodeFlagResult(t *testing.T, event wsTestEvent) wscontroller.FlagResultPayload {
	t.Helper()
	var payload wscontroller.FlagResultPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	return payload
}

func decodeOpponentSolved(t *testing.T, event wsTestEvent) wscontroller.OpponentSolvedPayload {
	t.Helper()
	var payload wscontroller.OpponentSolvedPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	return payload
}

func decodeDuelFinished(t *testing.T, event wsTestEvent) wscontroller.DuelFinishedPayload {
	t.Helper()
	var payload wscontroller.DuelFinishedPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	return payload
}

func decodeHintUnlocked(t *testing.T, event wsTestEvent) wscontroller.HintUnlockedPayload {
	t.Helper()
	var payload wscontroller.HintUnlockedPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	return payload
}

func decodeWinnerID(t *testing.T, event wsTestEvent) uuid.UUID {
	t.Helper()
	var payload wscontroller.DuelFinishedPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	require.NotNil(t, payload.Duel.WinnerID)
	return *payload.Duel.WinnerID
}

func decodeOpponentDisconnected(t *testing.T, event wsTestEvent) wscontroller.OpponentDisconnectedPayload {
	t.Helper()
	var payload wscontroller.OpponentDisconnectedPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	return payload
}

func decodeOpponentReconnected(t *testing.T, event wsTestEvent) wscontroller.OpponentReconnectedPayload {
	t.Helper()
	var payload wscontroller.OpponentReconnectedPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	return payload
}

func decodeDuelResume(t *testing.T, event wsTestEvent) wscontroller.DuelResumePayload {
	t.Helper()
	var payload wscontroller.DuelResumePayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	return payload
}

func wsURL(base string, token uuid.UUID) string {
	return "ws" + strings.TrimPrefix(base, "http") + "/ws?token=" + token.String()
}

func disconnectWS(t *testing.T, conn *coderws.Conn) {
	t.Helper()
	require.NoError(t, conn.Close(coderws.StatusNormalClosure, ""))
}

func closeWS(t *testing.T, conn *coderws.Conn) {
	t.Helper()
	require.NoError(t, conn.Close(coderws.StatusNormalClosure, ""))
}

func closeWSSilent(conn *coderws.Conn) {
	if conn != nil {
		_ = conn.Close(coderws.StatusNormalClosure, "")
	}
}
