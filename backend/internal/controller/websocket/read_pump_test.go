package websocket

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
)

func TestServerPublishMatchMarksMissingParticipantDisconnected(t *testing.T) {
	t.Parallel()

	player1 := &domain.Player{ID: uuid.New(), Username: "alice"}
	player2 := &domain.Player{ID: uuid.New(), Username: "bob"}
	duel := &domain.Duel{
		ID:        uuid.New(),
		Player1ID: player1.ID,
		Player2ID: player2.ID,
		Status:    domain.DuelStatusActive,
		StartedAt: time.Now().UTC(),
		Deadline:  time.Now().Add(time.Minute).UTC(),
	}
	reconnect := &recordingReconnectManager{}
	server := NewServer(
		&publishMatchPlayerRepo{players: map[uuid.UUID]*domain.Player{
			player1.ID: player1,
			player2.ID: player2,
		}},
		nil,
		nil,
		NewHubRegistry(),
		WithReconnectManager(reconnect),
		WithHubCloseDelay(0),
	)
	present := &client{
		player: player1,
		send:   make(chan []byte, 4),
		done:   make(chan struct{}),
	}
	server.clients.Store(player1.ID, present)

	server.publishMatch(context.Background(), &duelusecase.MatchResult{
		Duel: duel,
		Player1Task: &domain.Task{
			ID:         uuid.New(),
			Title:      "web",
			Category:   domain.CategoryWeb,
			Difficulty: domain.DifficultyEasy,
			TimeLimit:  60,
			Hints:      []string{"one", "two", "three"},
		},
		Player2Task: &domain.Task{
			ID:         uuid.New(),
			Title:      "pwn",
			Category:   domain.CategoryPwn,
			Difficulty: domain.DifficultyEasy,
			TimeLimit:  60,
			Hints:      []string{"one", "two", "three"},
		},
	})

	duelID, inDuel := present.currentDuel()
	require.True(t, inDuel)
	require.Equal(t, duel.ID, duelID)
	require.Equal(t, []*domain.Duel{duel}, reconnect.started)
	require.Equal(t, []recordedDisconnect{{duelID: duel.ID, playerID: player2.ID}}, reconnect.disconnects)
}

func TestDecodeIncomingEventRejectsUnknownTopLevelField(t *testing.T) {
	t.Parallel()

	_, err := decodeIncomingEvent(strings.NewReader(`{"type":"ping","data":{}}`))

	require.Error(t, err)
}

func TestDecodeIncomingEventRejectsTrailingJSON(t *testing.T) {
	t.Parallel()

	_, err := decodeIncomingEvent(strings.NewReader(`{"type":"ping"} {"type":"ping"}`))

	require.Error(t, err)
}

func TestServerFlagSubmitRejectsMismatchedDuelID(t *testing.T) {
	t.Parallel()

	activeDuelID := uuid.New()
	wireDuelID := uuid.New()
	playerID := uuid.New()
	flags := &countingFlagSubmitter{}
	server := NewServer(nil, nil, flags, NewHubRegistry(), WithHubCloseDelay(0))
	c := &client{
		player: &domain.Player{ID: playerID},
		send:   make(chan []byte, 1),
		done:   make(chan struct{}),
	}
	c.setDuel(activeDuelID)
	rawPayload, err := json.Marshal(map[string]any{
		"duel_id": wireDuelID,
		"flag":    "ctf{wrong-duel}",
	})
	require.NoError(t, err)

	server.routeEvent(context.Background(), c, IncomingEvent{Type: EventFlagSubmit, Payload: rawPayload})

	event := readBufferedClientEvent(t, c)
	require.Equal(t, EventError, event.Type)
	require.Equal(t, ErrorInvalidPayload, event.Code)
	require.Zero(t, flags.submitCalls)
}

func TestServerNoPayloadEventRejectsPayload(t *testing.T) {
	t.Parallel()

	server := NewServer(nil, nil, nil, NewHubRegistry(), WithHubCloseDelay(0))
	c := &client{
		player: &domain.Player{ID: uuid.New()},
		send:   make(chan []byte, 1),
		done:   make(chan struct{}),
	}
	rawPayload, err := json.Marshal(map[string]any{"unexpected": true})
	require.NoError(t, err)

	server.routeEvent(context.Background(), c, IncomingEvent{Type: EventPing, Payload: rawPayload})

	event := readBufferedClientEvent(t, c)
	require.Equal(t, EventError, event.Type)
	require.Equal(t, ErrorInvalidPayload, event.Code)
}

func TestServerNoPayloadEventAllowsNullPayload(t *testing.T) {
	t.Parallel()

	server := NewServer(nil, nil, nil, NewHubRegistry(), WithHubCloseDelay(0))
	c := &client{
		player: &domain.Player{ID: uuid.New()},
		send:   make(chan []byte, 1),
		done:   make(chan struct{}),
	}

	server.routeEvent(context.Background(), c, IncomingEvent{Type: EventPing, Payload: json.RawMessage("null")})

	event := readBufferedClientEvent(t, c)
	require.Equal(t, EventPong, event.Type)
}

func TestServerFlagSubmitAcceptsMatchingDuelID(t *testing.T) {
	t.Parallel()

	duelID := uuid.New()
	playerID := uuid.New()
	flags := &countingFlagSubmitter{}
	server := NewServer(nil, nil, flags, NewHubRegistry(), WithHubCloseDelay(0))
	c := &client{
		player: &domain.Player{ID: playerID},
		send:   make(chan []byte, 1),
		done:   make(chan struct{}),
	}
	c.setDuel(duelID)
	rawPayload, err := json.Marshal(map[string]any{
		"duel_id": duelID,
		"flag":    " ctf{candidate} ",
	})
	require.NoError(t, err)

	server.routeEvent(context.Background(), c, IncomingEvent{Type: EventFlagSubmit, Payload: rawPayload})

	require.Equal(t, 1, flags.submitCalls)
}

func TestServerFlagSubmitRejectsUnknownPayloadField(t *testing.T) {
	t.Parallel()

	duelID := uuid.New()
	playerID := uuid.New()
	flags := &countingFlagSubmitter{}
	server := NewServer(nil, nil, flags, NewHubRegistry(), WithHubCloseDelay(0))
	c := &client{
		player: &domain.Player{ID: playerID},
		send:   make(chan []byte, 1),
		done:   make(chan struct{}),
	}
	c.setDuel(duelID)
	rawPayload, err := json.Marshal(map[string]any{
		"duel_id": duelID,
		"flag":    "ctf{candidate}",
		"extra":   true,
	})
	require.NoError(t, err)

	server.routeEvent(context.Background(), c, IncomingEvent{Type: EventFlagSubmit, Payload: rawPayload})

	event := readBufferedClientEvent(t, c)
	require.Equal(t, EventError, event.Type)
	require.Equal(t, ErrorInvalidPayload, event.Code)
	require.Zero(t, flags.submitCalls)
}

func TestServerSurrenderRejectsMismatchedDuelID(t *testing.T) {
	t.Parallel()

	activeDuelID := uuid.New()
	wireDuelID := uuid.New()
	playerID := uuid.New()
	reconnect := &noopReconnectManager{}
	server := NewServer(nil, nil, nil, NewHubRegistry(), WithReconnectManager(reconnect), WithHubCloseDelay(0))
	c := &client{
		player: &domain.Player{ID: playerID},
		send:   make(chan []byte, 1),
		done:   make(chan struct{}),
	}
	c.setDuel(activeDuelID)
	rawPayload, err := json.Marshal(map[string]any{"duel_id": wireDuelID})
	require.NoError(t, err)

	server.routeEvent(context.Background(), c, IncomingEvent{Type: EventSurrender, Payload: rawPayload})

	event := readBufferedClientEvent(t, c)
	require.Equal(t, EventError, event.Type)
	require.Equal(t, ErrorInvalidPayload, event.Code)
	require.Zero(t, reconnect.forfeitCalls)
}

func readBufferedClientEvent(t *testing.T, c *client) Event {
	t.Helper()
	select {
	case data := <-c.send:
		var event Event
		require.NoError(t, json.Unmarshal(data, &event))
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for client event")
		return Event{}
	}
}

type publishMatchPlayerRepo struct {
	players map[uuid.UUID]*domain.Player
}

var _ usecase.PlayerRepo = (*publishMatchPlayerRepo)(nil)

func (r *publishMatchPlayerRepo) Create(context.Context, string) (*domain.Player, error) {
	panic("unused")
}

func (r *publishMatchPlayerRepo) JoinByUsername(context.Context, string, uuid.UUID) (*domain.Player, error) {
	panic("unused")
}

func (r *publishMatchPlayerRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.Player, error) {
	player := r.players[id]
	if player == nil {
		return nil, nil
	}
	snapshot := *player
	return &snapshot, nil
}

func (r *publishMatchPlayerRepo) GetByUsername(context.Context, string) (*domain.Player, error) {
	panic("unused")
}

func (r *publishMatchPlayerRepo) GetBySessionToken(context.Context, uuid.UUID) (*domain.Player, error) {
	panic("unused")
}

func (r *publishMatchPlayerRepo) UpdateSessionToken(context.Context, uuid.UUID, *uuid.UUID) (*domain.Player, error) {
	panic("unused")
}

func (r *publishMatchPlayerRepo) UpdateStatus(context.Context, uuid.UUID, domain.PlayerStatus) (*domain.Player, error) {
	panic("unused")
}

func (r *publishMatchPlayerRepo) UpdateStatusIfCurrent(
	context.Context,
	uuid.UUID,
	domain.PlayerStatus,
	domain.PlayerStatus,
) (*domain.Player, bool, error) {
	panic("unused")
}

type recordingReconnectManager struct {
	started     []*domain.Duel
	disconnects []recordedDisconnect
	stopAll     int
}

type recordedDisconnect struct {
	duelID   uuid.UUID
	playerID uuid.UUID
}

var _ ReconnectManager = (*recordingReconnectManager)(nil)

func (m *recordingReconnectManager) StartDuelTimer(duel *domain.Duel) {
	m.started = append(m.started, duel)
}

func (m *recordingReconnectManager) HandleDisconnect(_ context.Context, duelID, playerID uuid.UUID) {
	m.disconnects = append(m.disconnects, recordedDisconnect{duelID: duelID, playerID: playerID})
}

func (m *recordingReconnectManager) ConsumeReconnect(
	context.Context,
	uuid.UUID,
) (*duelusecase.ReconnectDecision, error) {
	panic("unused")
}

func (m *recordingReconnectManager) ActiveDuel(
	context.Context,
	uuid.UUID,
) (*duelusecase.ReconnectDecision, error) {
	panic("unused")
}

func (m *recordingReconnectManager) DuelPaused(uuid.UUID) bool {
	return false
}

func (m *recordingReconnectManager) FinalizeDraw(context.Context, uuid.UUID) (*domain.Duel, error) {
	panic("unused")
}

func (m *recordingReconnectManager) FinalizePlayerForfeit(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (*domain.Duel, error) {
	panic("unused")
}

func (m *recordingReconnectManager) CloseDuel(uuid.UUID) {}

func (m *recordingReconnectManager) StopAll() {
	m.stopAll++
}
