package websocket

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

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
)

func TestServerShutdownLeavesQueuedClient(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	player := &domain.Player{
		ID:           uuid.New(),
		Username:     "alice",
		SessionToken: &token,
		Status:       domain.PlayerStatusIdle,
	}
	matchmaking := &shutdownMatchmaking{left: make(chan uuid.UUID, 1)}
	server := NewServer(
		&shutdownPlayerRepo{player: player},
		matchmaking,
		nil,
		NewHubRegistry(),
		WithHubCloseDelay(0),
	)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, resp, err := coderws.Dial(t.Context(), "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/ws?token="+token.String(), nil)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	defer func() { _ = conn.Close(coderws.StatusNormalClosure, "") }()

	writeTestEvent(t, conn, EventJoinQueue, nil)
	require.Equal(t, EventQueueJoined, readTestEvent(t, conn).Type)

	server.Shutdown(context.Background())

	select {
	case playerID := <-matchmaking.left:
		require.Equal(t, player.ID, playerID)
	case <-time.After(time.Second):
		t.Fatal("queued client was not removed from matchmaking during shutdown")
	}
}

func TestServerSendDuelResumeIncludesOpponentID(t *testing.T) {
	t.Parallel()

	playerID := uuid.New()
	opponentID := uuid.New()
	duelID := uuid.New()
	deadline := time.Now().Add(time.Minute).UTC()
	c := &client{
		player: &domain.Player{
			ID: playerID,
		},
		send: make(chan []byte, 1),
		done: make(chan struct{}),
	}
	server := &Server{}

	server.sendDuelResume(context.Background(), c, &duelusecase.ReconnectDecision{
		Duel: &domain.Duel{
			ID:        duelID,
			Player1ID: playerID,
			Player2ID: opponentID,
			Status:    domain.DuelStatusActive,
			Deadline:  deadline,
		},
		OpponentID:  opponentID,
		NewDeadline: deadline,
	}, false)

	select {
	case data := <-c.send:
		var got struct {
			Type    string            `json:"type"`
			Payload DuelResumePayload `json:"payload"`
		}
		require.NoError(t, json.Unmarshal(data, &got))
		require.Equal(t, EventDuelResume, got.Type)
		require.Equal(t, duelID, got.Payload.DuelID)
		require.Equal(t, opponentID, got.Payload.OpponentID)
		require.Equal(t, deadline, got.Payload.Deadline)
	case <-time.After(time.Second):
		t.Fatal("duel_resume event was not sent")
	}
}

// TestSubprotocolBearerAuth verifies the C4 hardening: sessions can be
// authenticated via Sec-WebSocket-Protocol so the token never appears in
// the URL query string. The legacy ?token= path remains as a fallback
// during rollout — that one is exercised by every other test in this file.
func TestSubprotocolBearerAuth(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	player := &domain.Player{
		ID:           uuid.New(),
		Username:     "alice",
		SessionToken: &token,
		Status:       domain.PlayerStatusIdle,
	}
	server := NewServer(
		&shutdownPlayerRepo{player: player},
		&shutdownMatchmaking{left: make(chan uuid.UUID, 1)},
		nil,
		NewHubRegistry(),
		WithHubCloseDelay(0),
	)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	bearer := SubprotocolBearerPrefix + token.String()
	conn, resp, err := coderws.Dial(t.Context(), "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/ws", &coderws.DialOptions{
		Subprotocols: []string{bearer},
	})
	require.NoError(t, err, "subprotocol-only handshake must succeed without ?token=")
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	defer func() { _ = conn.Close(coderws.StatusNormalClosure, "") }()

	// The chosen subprotocol must round-trip so the browser does not abort
	// the connection.
	require.Equal(t, bearer, conn.Subprotocol())

	writeTestEvent(t, conn, EventPing, nil)
	require.Equal(t, EventPong, readTestEvent(t, conn).Type)
}

// TestSubprotocolBearerAuthRejectsBadToken proves that random or malformed
// subprotocol values are not silently accepted.
func TestSubprotocolBearerAuthRejectsBadToken(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	player := &domain.Player{
		ID:           uuid.New(),
		Username:     "alice",
		SessionToken: &token,
		Status:       domain.PlayerStatusIdle,
	}
	server := NewServer(
		&shutdownPlayerRepo{player: player},
		&shutdownMatchmaking{left: make(chan uuid.UUID, 1)},
		nil,
		NewHubRegistry(),
		WithHubCloseDelay(0),
	)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	_, resp, err := coderws.Dial(t.Context(), "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/ws", &coderws.DialOptions{
		Subprotocols: []string{SubprotocolBearerPrefix + "not-a-uuid"},
	})
	require.Error(t, err)
	if resp != nil {
		require.Equal(t, 401, resp.StatusCode)
		if resp.Body != nil {
			resp.Body.Close()
		}
	}
}

// TestServerFlagSubmitIgnoresClientSuppliedDuelID verifies the C2 fix: a
// client cannot drive flag_submit at an arbitrary duel via the wire payload.
// We connect a player that has NO active duel bound to the connection
// (c.currentDuel() returns false) and submit a payload that includes a duel
// id plus a flag. The server must drop the wire duel_id, fall back to the
// per-connection state, and reject with invalid_payload — never reach the
// usecase layer.
func TestServerFlagSubmitIgnoresClientSuppliedDuelID(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	player := &domain.Player{
		ID:           uuid.New(),
		Username:     "alice",
		SessionToken: &token,
		Status:       domain.PlayerStatusIdle,
	}
	flags := &countingFlagSubmitter{}
	server := NewServer(
		&shutdownPlayerRepo{player: player},
		&shutdownMatchmaking{left: make(chan uuid.UUID, 1)},
		flags,
		NewHubRegistry(),
		WithHubCloseDelay(0),
	)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, resp, err := coderws.Dial(t.Context(), "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/ws?token="+token.String(), nil)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	defer func() { _ = conn.Close(coderws.StatusNormalClosure, "") }()

	foreignDuelID := uuid.New()
	writeTestEvent(t, conn, EventFlagSubmit, map[string]any{
		"duel_id": foreignDuelID,
		"flag":    "ctf{maliciously-guessed}",
	})

	event := readTestEvent(t, conn)
	require.Equal(t, EventError, event.Type)
	require.Equal(t, ErrorInvalidPayload, event.Code)
	require.Zero(t, flags.submitCalls, "usecase must NOT receive a submission for a duel the client merely named")
}

// TestServerSurrenderIgnoresClientSuppliedDuelID is the surrender-side mirror
// of the C2 hardening test above. A connection with no bound duel must not
// be able to surrender a foreign duel via the wire payload.
func TestServerSurrenderIgnoresClientSuppliedDuelID(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	player := &domain.Player{
		ID:           uuid.New(),
		Username:     "bob",
		SessionToken: &token,
		Status:       domain.PlayerStatusIdle,
	}
	reconnect := &noopReconnectManager{}
	server := NewServer(
		&shutdownPlayerRepo{player: player},
		&shutdownMatchmaking{left: make(chan uuid.UUID, 1)},
		nil,
		NewHubRegistry(),
		WithReconnectManager(reconnect),
		WithHubCloseDelay(0),
	)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, resp, err := coderws.Dial(t.Context(), "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/ws?token="+token.String(), nil)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	defer func() { _ = conn.Close(coderws.StatusNormalClosure, "") }()

	foreignDuelID := uuid.New()
	writeTestEvent(t, conn, EventSurrender, map[string]any{
		"duel_id": foreignDuelID,
	})

	event := readTestEvent(t, conn)
	require.Equal(t, EventError, event.Type)
	require.Equal(t, ErrorInvalidPayload, event.Code)
	require.Zero(t, reconnect.forfeitCalls, "FinalizePlayerForfeit must NOT be invoked for a duel the client merely named")
}

type countingFlagSubmitter struct {
	submitCalls int
}

func (f *countingFlagSubmitter) SubmitFlag(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ string) (duelusecase.Result, error) {
	f.submitCalls++
	return duelusecase.Result{}, nil
}

// noopReconnectManager is a benign stub for tests that exercise ServeHTTP
// without needing real reconnect semantics: every probe returns "no decision"
// so the handshake bypasses the reconnect/restore branches and lands in
// readPump, while we still get to assert that mutating calls (forfeit) were
// or were not invoked.
type noopReconnectManager struct {
	forfeitCalls int
}

var _ ReconnectManager = (*noopReconnectManager)(nil)

func (m *noopReconnectManager) StartDuelTimer(*domain.Duel) {}

func (m *noopReconnectManager) HandleDisconnect(context.Context, uuid.UUID, uuid.UUID) {}

func (m *noopReconnectManager) ConsumeReconnect(
	context.Context,
	uuid.UUID,
) (*duelusecase.ReconnectDecision, error) {
	return nil, nil
}

func (m *noopReconnectManager) ActiveDuel(
	context.Context,
	uuid.UUID,
) (*duelusecase.ReconnectDecision, error) {
	return nil, nil
}

func (m *noopReconnectManager) DuelPaused(uuid.UUID) bool { return false }

func (m *noopReconnectManager) FinalizeOpponentForfeit(context.Context, uuid.UUID, uuid.UUID) {}

func (m *noopReconnectManager) FinalizePlayerForfeit(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (*domain.Duel, error) {
	m.forfeitCalls++
	return nil, nil
}

func (m *noopReconnectManager) CloseDuel(uuid.UUID) {}

func TestServerCleanupClientSkipsDisconnectForFastReplacement(t *testing.T) {
	t.Parallel()

	playerID := uuid.New()
	duelID := uuid.New()
	reconnect := &recordingReconnectManager{}
	server := NewServer(
		nil,
		nil,
		nil,
		NewHubRegistry(),
		WithReconnectManager(reconnect),
		WithDisconnectGrace(20*time.Millisecond),
	)
	oldClient := newCleanupTestClient(playerID, duelID)
	replacement := newCleanupTestClient(playerID, duelID)
	server.clients.Store(playerID, oldClient)

	done := make(chan struct{})
	go func() {
		server.cleanupClient(context.Background(), oldClient)
		close(done)
	}()

	time.Sleep(5 * time.Millisecond)
	server.clients.Store(playerID, replacement)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not finish after disconnect grace")
	}
	require.Empty(t, reconnect.disconnects)
	require.True(t, oldClient.closed.Load())

	current, ok := server.clientByPlayer(playerID)
	require.True(t, ok)
	require.Same(t, replacement, current)
}

func TestServerCleanupClientDisconnectsAfterGraceWithoutReplacement(t *testing.T) {
	t.Parallel()

	playerID := uuid.New()
	duelID := uuid.New()
	reconnect := &recordingReconnectManager{}
	server := NewServer(
		nil,
		nil,
		nil,
		NewHubRegistry(),
		WithReconnectManager(reconnect),
		WithDisconnectGrace(10*time.Millisecond),
	)
	c := newCleanupTestClient(playerID, duelID)
	server.clients.Store(playerID, c)

	server.cleanupClient(context.Background(), c)

	require.Equal(t, []recordedDisconnect{{duelID: duelID, playerID: playerID}}, reconnect.disconnects)
	require.True(t, c.closed.Load())
	_, ok := server.clientByPlayer(playerID)
	require.False(t, ok)
}

func newCleanupTestClient(playerID, duelID uuid.UUID) *client {
	c := &client{
		player: &domain.Player{ID: playerID},
		send:   make(chan []byte, 1),
		done:   make(chan struct{}),
	}
	c.setDuel(duelID)
	return c
}

type shutdownPlayerRepo struct {
	player *domain.Player
}

func (r *shutdownPlayerRepo) Create(context.Context, string) (*domain.Player, error) {
	panic("unused")
}

func (r *shutdownPlayerRepo) JoinByUsername(context.Context, string, uuid.UUID) (*domain.Player, error) {
	panic("unused")
}

func (r *shutdownPlayerRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.Player, error) {
	if r.player == nil || r.player.ID != id {
		return nil, nil
	}
	out := *r.player
	return &out, nil
}

func (r *shutdownPlayerRepo) GetByUsername(context.Context, string) (*domain.Player, error) {
	panic("unused")
}

func (r *shutdownPlayerRepo) GetBySessionToken(_ context.Context, token uuid.UUID) (*domain.Player, error) {
	if r.player == nil || r.player.SessionToken == nil || *r.player.SessionToken != token {
		return nil, nil
	}
	out := *r.player
	return &out, nil
}

func (r *shutdownPlayerRepo) UpdateSessionToken(context.Context, uuid.UUID, *uuid.UUID) (*domain.Player, error) {
	panic("unused")
}

func (r *shutdownPlayerRepo) UpdateStatus(context.Context, uuid.UUID, domain.PlayerStatus) (*domain.Player, error) {
	panic("unused")
}

func (r *shutdownPlayerRepo) UpdateStatusIfCurrent(
	context.Context,
	uuid.UUID,
	domain.PlayerStatus,
	domain.PlayerStatus,
) (*domain.Player, bool, error) {
	panic("unused")
}

type shutdownMatchmaking struct {
	left chan uuid.UUID
}

func (m *shutdownMatchmaking) JoinQueue(context.Context, uuid.UUID) (*duelusecase.MatchResult, error) {
	return nil, nil
}

func (m *shutdownMatchmaking) LeaveQueue(_ context.Context, playerID uuid.UUID) error {
	m.left <- playerID
	return nil
}

func writeTestEvent(t *testing.T, conn *coderws.Conn, typ string, payload any) {
	t.Helper()

	data, err := json.Marshal(Event{Type: typ, Payload: payload})
	require.NoError(t, err)
	writeCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.NoError(t, conn.Write(writeCtx, coderws.MessageText, data))
}

func readTestEvent(t *testing.T, conn *coderws.Conn) Event {
	t.Helper()

	readCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	msgType, data, err := conn.Read(readCtx)
	require.NoError(t, err)
	require.Equal(t, coderws.MessageText, msgType)

	var event Event
	require.NoError(t, json.Unmarshal(data, &event))
	return event
}
