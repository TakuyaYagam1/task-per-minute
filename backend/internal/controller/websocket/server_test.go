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
