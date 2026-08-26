//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	restmw "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	wsadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket"
	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
)

const wsTestTimeout = 15 * time.Second

type websocketFixture struct {
	*duelFixture
	playerUC   *playerusecase.UseCase
	boardStore *redisadapter.LeaderboardRedis
	hubs       *wsadapter.HubRegistry
	hints      *duelusecase.HintScheduler
	httpServer *httptest.Server
}

func newWebSocketFixture(t *testing.T) *websocketFixture {
	return newWebSocketFixtureWithReconnectWindow(t, duelusecase.DefaultReconnectWindow)
}

func newWebSocketFixtureWithReconnectWindow(t *testing.T, reconnectWindow time.Duration) *websocketFixture {
	t.Helper()
	return newWebSocketFixtureFromDuelFixture(t, newIsolatedDuelFixture(t), reconnectWindow)
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
	queue := redisadapter.NewMatchmakingRedis(redisClient, "matchmaking:"+uniq("q"))
	board := redisadapter.NewLeaderboardRedis(redisClient, "leaderboard:"+uniq("z"))
	clock := realIntegrationClock()
	playerUC := playerusecase.NewUseCase(f.mgr, f.players, f.duels, clock)
	matchmaking := duelusecase.NewMatchmakingUseCase(
		f.mgr,
		queue,
		f.players,
		f.tasks,
		f.history,
		f.duels,
		nil,
		clock,
	)
	timers := duelusecase.NewTimerRegistry(f.mgr, f.duels, f.players, clock)
	hints := duelusecase.NewHintScheduler(clock, nil)
	flags := duelusecase.NewFlagSubmitUseCase(
		f.mgr,
		f.duels,
		f.players,
		f.history,
		board,
		clock,
		timers,
	)
	hubs := wsadapter.NewHubRegistry()
	server := wsadapter.NewServer(
		f.players,
		matchmaking,
		flags,
		hubs,
		wsadapter.WithHubCloseDelay(20*time.Millisecond),
		wsadapter.WithDisconnectGrace(0),
		wsadapter.WithHintScheduler(hints),
		wsadapter.WithTimerStopper(timers),
		wsadapter.WithTaskResolver(f.duels, nil),
	)
	reconnect := duelusecase.NewReconnectManager(
		f.mgr,
		f.duels,
		f.players,
		wsadapter.NewPauseableDuelTimers(timers, hints),
		server.Broadcaster(),
		clock,
		duelusecase.WithReconnectWindow(reconnectWindow),
		duelusecase.WithLeaderboardStore(board),
	)
	wsadapter.WithReconnectManager(reconnect)(server)
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	// Drain leaked time.AfterFunc goroutines from this server before the next
	// test starts. Without StopAll these goroutines can outlive the test and
	// race with the next matchmaking flow.
	t.Cleanup(func() {
		timers.StopAll()
		hints.StopAll()
		reconnect.StopAll()
	})

	return &websocketFixture{
		duelFixture: f,
		playerUC:    playerUC,
		boardStore:  board,
		hubs:        hubs,
		hints:       hints,
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

	writeWSEvent(t, aliceConn, wsadapter.EventJoinQueue, nil)
	require.Equal(t, wsadapter.EventQueueJoined, readWSEventType(t, aliceConn, wsadapter.EventQueueJoined).Type)
	writeWSEvent(t, bobConn, wsadapter.EventJoinQueue, nil)
	require.Equal(t, wsadapter.EventQueueJoined, readWSEventType(t, bobConn, wsadapter.EventQueueJoined).Type)

	aliceMatch := readWSEventType(t, aliceConn, wsadapter.EventMatchFound)
	require.Equal(t, wsadapter.EventTaskAssigned, readWSEventType(t, aliceConn, wsadapter.EventTaskAssigned).Type)
	bobMatch := readWSEventType(t, bobConn, wsadapter.EventMatchFound)
	require.Equal(t, wsadapter.EventTaskAssigned, readWSEventType(t, bobConn, wsadapter.EventTaskAssigned).Type)

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

	ctx, cancel := context.WithTimeout(context.Background(), wsTestTimeout)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, wsEndpoint(f.httpServer.URL), wsDialOptions(token))
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

	data, err := json.Marshal(wsadapter.Event{Type: typ, Payload: payload})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), wsTestTimeout)
	defer cancel()
	require.NoError(t, conn.Write(ctx, coderws.MessageText, data))
}

func readWSEventType(t *testing.T, conn *coderws.Conn, typ string) wsTestEvent {
	t.Helper()

	deadline := time.Now().Add(wsTestTimeout)
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
	var payload wsadapter.MatchFoundPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	return payload.Duel.ID
}

func decodeMatchFound(t *testing.T, event wsTestEvent) wsadapter.MatchFoundPayload {
	t.Helper()
	var payload wsadapter.MatchFoundPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	return payload
}

func decodeAssignmentDuelID(t *testing.T, event wsTestEvent) uuid.UUID {
	t.Helper()
	var payload wsadapter.TaskAssignedPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	return payload.DuelID
}

func decodeTaskAssigned(t *testing.T, event wsTestEvent) wsadapter.TaskAssignedPayload {
	t.Helper()
	var payload wsadapter.TaskAssignedPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	return payload
}

func decodeFlagResult(t *testing.T, event wsTestEvent) wsadapter.FlagResultPayload {
	t.Helper()
	var payload wsadapter.FlagResultPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	return payload
}

func decodeOpponentSolved(t *testing.T, event wsTestEvent) wsadapter.OpponentSolvedPayload {
	t.Helper()
	var payload wsadapter.OpponentSolvedPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	return payload
}

func decodeDuelFinished(t *testing.T, event wsTestEvent) wsadapter.DuelFinishedPayload {
	t.Helper()
	var payload wsadapter.DuelFinishedPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	return payload
}

func decodeHintUnlocked(t *testing.T, event wsTestEvent) wsadapter.HintUnlockedPayload {
	t.Helper()
	var payload wsadapter.HintUnlockedPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	return payload
}

func decodeWinnerID(t *testing.T, event wsTestEvent) uuid.UUID {
	t.Helper()
	var payload wsadapter.DuelFinishedPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	if payload.Duel.WinnerID == nil {
		return uuid.Nil
	}
	return *payload.Duel.WinnerID
}

func decodeOpponentDisconnected(t *testing.T, event wsTestEvent) wsadapter.OpponentDisconnectedPayload {
	t.Helper()
	var payload wsadapter.OpponentDisconnectedPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	return payload
}

func decodeOpponentReconnected(t *testing.T, event wsTestEvent) wsadapter.OpponentReconnectedPayload {
	t.Helper()
	var payload wsadapter.OpponentReconnectedPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	return payload
}

func decodeDuelResume(t *testing.T, event wsTestEvent) wsadapter.DuelResumePayload {
	t.Helper()
	var payload wsadapter.DuelResumePayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	return payload
}

func wsEndpoint(base string) string {
	return "ws" + strings.TrimPrefix(base, "http") + "/ws"
}

func wsDialOptions(token uuid.UUID) *coderws.DialOptions {
	headers := http.Header{}
	headers.Add("Cookie", (&http.Cookie{Name: restmw.PlayerSessionCookieName, Value: token.String()}).String())
	return &coderws.DialOptions{
		HTTPHeader: headers,
	}
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
