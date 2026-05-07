package websocket

import (
	"context"
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
