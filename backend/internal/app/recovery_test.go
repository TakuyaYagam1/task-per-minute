package app

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestStartupRecoverer_RearmsFutureActiveDuel(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	duel := recoveryDuel(now.Add(time.Minute))
	player1Task := recoveryTask("p1")
	player2Task := recoveryTask("p2")
	duels := &recoveryDuelRepo{
		active: []*domain.Duel{duel},
		tasks: map[uuid.UUID]map[uuid.UUID]*domain.Task{
			duel.ID: {
				duel.Player1ID: player1Task,
				duel.Player2ID: player2Task,
			},
		},
	}
	players := &recoveryPlayerRepo{statuses: make(map[uuid.UUID]domain.PlayerStatus)}
	timers := &recoveryTimerStarter{}
	hints := &recoveryHintStarter{}

	recoverer := NewStartupRecoverer(
		recoveryTx{},
		duels,
		duels,
		players,
		nil,
		nil,
		nil,
		timers,
		hints,
		recoveryClock{now: now},
		nil,
	)

	require.NoError(t, recoverer.Recover(t.Context()))
	require.Empty(t, duels.finished)
	require.Equal(t, domain.PlayerStatusInDuel, players.statuses[duel.Player1ID])
	require.Equal(t, domain.PlayerStatusInDuel, players.statuses[duel.Player2ID])
	require.Equal(t, []*domain.Duel{duel}, timers.started)
	require.Equal(t, map[uuid.UUID]*domain.Task{
		duel.Player1ID: player1Task,
		duel.Player2ID: player2Task,
	}, hints.assignments[duel.ID])
}

func TestStartupRecoverer_FinishesExpiredActiveDuel(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	duel := recoveryDuel(now.Add(-time.Second))
	duels := &recoveryDuelRepo{active: []*domain.Duel{duel}}
	players := &recoveryPlayerRepo{statuses: make(map[uuid.UUID]domain.PlayerStatus)}
	broadcaster := &recoveryBroadcaster{}
	timers := &recoveryTimerStarter{}
	hints := &recoveryHintStarter{}

	recoverer := NewStartupRecoverer(
		recoveryTx{},
		duels,
		duels,
		players,
		nil,
		nil,
		broadcaster,
		timers,
		hints,
		recoveryClock{now: now},
		nil,
	)

	require.NoError(t, recoverer.Recover(t.Context()))
	require.Len(t, duels.finished, 1)
	require.Nil(t, duels.finished[0].WinnerID)
	require.Equal(t, domain.DuelStatusFinished, duels.finished[0].Status)
	require.Equal(t, now, *duels.finished[0].FinishedAt)
	require.Equal(t, domain.PlayerStatusIdle, players.statuses[duel.Player1ID])
	require.Equal(t, domain.PlayerStatusIdle, players.statuses[duel.Player2ID])
	require.Equal(t, []*domain.Duel{duels.finished[0]}, broadcaster.finished)
	require.Empty(t, timers.started)
	require.Empty(t, hints.assignments)
}

func TestStartupRecoverer_FinishesInconsistentFutureActiveDuel(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	duel := recoveryDuel(now.Add(time.Minute))
	duels := &recoveryDuelRepo{active: []*domain.Duel{duel}, tasks: make(map[uuid.UUID]map[uuid.UUID]*domain.Task)}
	players := &recoveryPlayerRepo{statuses: make(map[uuid.UUID]domain.PlayerStatus)}
	broadcaster := &recoveryBroadcaster{}
	timers := &recoveryTimerStarter{}
	hints := &recoveryHintStarter{}

	recoverer := NewStartupRecoverer(
		recoveryTx{},
		duels,
		duels,
		players,
		nil,
		nil,
		broadcaster,
		timers,
		hints,
		recoveryClock{now: now},
		nil,
	)

	require.NoError(t, recoverer.Recover(t.Context()))
	require.Len(t, duels.finished, 1)
	require.Nil(t, duels.finished[0].WinnerID)
	require.Equal(t, domain.DuelStatusFinished, duels.finished[0].Status)
	require.Equal(t, now, *duels.finished[0].FinishedAt)
	require.Equal(t, domain.PlayerStatusIdle, players.statuses[duel.Player1ID])
	require.Equal(t, domain.PlayerStatusIdle, players.statuses[duel.Player2ID])
	require.Equal(t, []*domain.Duel{duels.finished[0]}, broadcaster.finished)
	require.Empty(t, timers.started)
	require.Empty(t, hints.assignments)
}

type recoveryClock struct {
	now time.Time
}

func (c recoveryClock) Now() time.Time {
	return c.now
}

type recoveryTx struct{}

func (recoveryTx) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

type recoveryDuelRepo struct {
	active   []*domain.Duel
	finished []*domain.Duel
	tasks    map[uuid.UUID]map[uuid.UUID]*domain.Task
}

func (r *recoveryDuelRepo) ListActive(context.Context) ([]*domain.Duel, error) {
	return r.active, nil
}

func (r *recoveryDuelRepo) Finish(_ context.Context, id uuid.UUID, winnerID *uuid.UUID, finishedAt time.Time, status domain.DuelStatus) (*domain.Duel, error) {
	for _, duel := range r.active {
		if duel.ID != id {
			continue
		}
		finished := *duel
		finished.Status = status
		finished.WinnerID = winnerID
		finished.FinishedAt = &finishedAt
		r.finished = append(r.finished, &finished)
		return &finished, nil
	}
	return nil, nil
}

func (r *recoveryDuelRepo) GetPlayerTask(_ context.Context, duelID, playerID uuid.UUID) (*domain.Task, error) {
	return r.tasks[duelID][playerID], nil
}

func (*recoveryDuelRepo) Create(context.Context, uuid.UUID, uuid.UUID, time.Time) (*domain.Duel, error) {
	return nil, nil
}

func (*recoveryDuelRepo) GetByID(context.Context, uuid.UUID) (*domain.Duel, error) {
	return nil, nil
}

func (*recoveryDuelRepo) GetActiveByPlayerID(context.Context, uuid.UUID) (*domain.Duel, error) {
	return nil, nil
}

func (*recoveryDuelRepo) UpdateDeadline(context.Context, uuid.UUID, time.Time) (*domain.Duel, error) {
	return nil, nil
}

func (*recoveryDuelRepo) CreateDuelPlayerTask(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return nil
}

func (*recoveryDuelRepo) GetDuelPlayerTask(context.Context, uuid.UUID, uuid.UUID) (*domain.DuelPlayerTask, error) {
	return nil, nil
}

func (*recoveryDuelRepo) MarkSolved(context.Context, uuid.UUID, uuid.UUID, time.Time) error {
	return nil
}

type recoveryPlayerRepo struct {
	statuses map[uuid.UUID]domain.PlayerStatus
}

func (r *recoveryPlayerRepo) UpdateStatus(_ context.Context, id uuid.UUID, status domain.PlayerStatus) (*domain.Player, error) {
	r.statuses[id] = status
	return &domain.Player{ID: id, Status: status}, nil
}

type recoveryTimerStarter struct {
	started []*domain.Duel
}

func (s *recoveryTimerStarter) StartDuelTimer(duel *domain.Duel) {
	s.started = append(s.started, duel)
}

type recoveryHintStarter struct {
	assignments map[uuid.UUID]map[uuid.UUID]*domain.Task
}

func (s *recoveryHintStarter) StartDuel(duel *domain.Duel, assignments map[uuid.UUID]*domain.Task) {
	if s.assignments == nil {
		s.assignments = make(map[uuid.UUID]map[uuid.UUID]*domain.Task)
	}
	s.assignments[duel.ID] = assignments
}

type recoveryBroadcaster struct {
	finished []*domain.Duel
}

func (b *recoveryBroadcaster) BroadcastDuelFinished(_ context.Context, duel *domain.Duel) {
	b.finished = append(b.finished, duel)
}

func (*recoveryBroadcaster) BroadcastOpponentDisconnected(context.Context, uuid.UUID, uuid.UUID, time.Time) {
}

func (*recoveryBroadcaster) BroadcastOpponentReconnected(context.Context, uuid.UUID, uuid.UUID, time.Time) {
}

func (*recoveryBroadcaster) BroadcastDuelExpired(context.Context, uuid.UUID) {}

func recoveryDuel(deadline time.Time) *domain.Duel {
	return &domain.Duel{
		ID:        uuid.New(),
		Player1ID: uuid.New(),
		Player2ID: uuid.New(),
		Status:    domain.DuelStatusActive,
		StartedAt: deadline.Add(-time.Minute),
		Deadline:  deadline,
	}
}

func recoveryTask(title string) *domain.Task {
	return &domain.Task{
		ID:          uuid.New(),
		Title:       title,
		Description: "description",
		Category:    domain.CategoryWeb,
		Difficulty:  domain.DifficultyEasy,
		TimeLimit:   60,
		Flag:        "FLAG{" + title + "}",
		Hints:       []string{"", "", ""},
	}
}
