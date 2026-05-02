package duel_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
)

func TestTimerRegistry_ExpiresDuelAsDraw(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	duels := newTimerDuelRepo()
	players := newTimerPlayerRepo()
	registry := duelusecase.NewTimerRegistry(timerTx{}, duels, players, fixedClock{now: now})
	duel := activeDuel(now.Add(10 * time.Millisecond))
	duels.put(duel)

	expired := make(chan struct{})
	registry.Start(duel.ID, duel.Deadline, func() { close(expired) })

	require.Eventually(t, func() bool {
		select {
		case <-expired:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)

	got, err := duels.GetByID(t.Context(), duel.ID)
	require.NoError(t, err)
	require.Equal(t, domain.DuelStatusFinished, got.Status)
	require.Nil(t, got.WinnerID)
	require.Equal(t, domain.PlayerStatusIdle, players.status(duel.Player1ID))
	require.Equal(t, domain.PlayerStatusIdle, players.status(duel.Player2ID))
	require.Equal(t, 1, duels.finishCount(duel.ID))
}

func TestTimerRegistry_StopPreventsExpiration(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	duels := newTimerDuelRepo()
	players := newTimerPlayerRepo()
	registry := duelusecase.NewTimerRegistry(timerTx{}, duels, players, fixedClock{now: now})
	duel := activeDuel(now.Add(50 * time.Millisecond))
	duels.put(duel)

	expired := make(chan struct{})
	registry.Start(duel.ID, duel.Deadline, func() { close(expired) })
	require.True(t, registry.Stop(duel.ID))

	select {
	case <-expired:
		t.Fatal("stopped timer expired")
	case <-time.After(80 * time.Millisecond):
	}
	require.Equal(t, 0, duels.finishCount(duel.ID))
}

func TestTimerRegistry_FreezeResume(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	duels := newTimerDuelRepo()
	players := newTimerPlayerRepo()
	registry := duelusecase.NewTimerRegistry(timerTx{}, duels, players, fixedClock{now: now})
	duel := activeDuel(now.Add(50 * time.Millisecond))
	duels.put(duel)

	expired := make(chan struct{})
	registry.Start(duel.ID, duel.Deadline, func() { close(expired) })
	require.True(t, registry.Freeze(duel.ID, now.Add(20*time.Millisecond)))

	select {
	case <-expired:
		t.Fatal("frozen timer expired")
	case <-time.After(70 * time.Millisecond):
	}

	newDeadline, ok := registry.Resume(duel.ID, now.Add(100*time.Millisecond))
	require.True(t, ok)
	require.Equal(t, now.Add(130*time.Millisecond), newDeadline)
	require.Eventually(t, func() bool {
		select {
		case <-expired:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
}

func TestTimerRegistry_StopFireRace(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 1000; i++ {
		duels := newTimerDuelRepo()
		players := newTimerPlayerRepo()
		registry := duelusecase.NewTimerRegistry(timerTx{}, duels, players, fixedClock{now: now})
		duel := activeDuel(now)
		duels.put(duel)

		registry.Start(duel.ID, now, nil)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			registry.Stop(duel.ID)
		}()
		wg.Wait()
		time.Sleep(time.Microsecond)

		require.LessOrEqual(t, duels.finishCount(duel.ID), 1)
	}
}

type timerTx struct{}

func (timerTx) Do(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type timerDuelRepo struct {
	mu       sync.Mutex
	duels    map[uuid.UUID]*domain.Duel
	finishes map[uuid.UUID]int
}

func newTimerDuelRepo() *timerDuelRepo {
	return &timerDuelRepo{
		duels:    make(map[uuid.UUID]*domain.Duel),
		finishes: make(map[uuid.UUID]int),
	}
}

func (r *timerDuelRepo) put(duel *domain.Duel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot := *duel
	r.duels[duel.ID] = &snapshot
}

func (r *timerDuelRepo) finishCount(duelID uuid.UUID) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.finishes[duelID]
}

func (r *timerDuelRepo) Create(context.Context, uuid.UUID, uuid.UUID, time.Time) (*domain.Duel, error) {
	panic("unused")
}

func (r *timerDuelRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.Duel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	duel, ok := r.duels[id]
	if !ok {
		return nil, apperr.ErrDuelNotFound
	}
	snapshot := *duel
	return &snapshot, nil
}

func (r *timerDuelRepo) GetActiveByPlayerID(context.Context, uuid.UUID) (*domain.Duel, error) {
	panic("unused")
}

func (r *timerDuelRepo) UpdateDeadline(context.Context, uuid.UUID, time.Time) (*domain.Duel, error) {
	panic("unused")
}

func (r *timerDuelRepo) Finish(_ context.Context, id uuid.UUID, winnerID *uuid.UUID, finishedAt time.Time, status domain.DuelStatus) (*domain.Duel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	duel, ok := r.duels[id]
	if !ok {
		return nil, apperr.ErrDuelNotFound
	}
	if duel.Status == domain.DuelStatusFinished {
		return nil, apperr.ErrDuelFinished
	}
	duel.Status = status
	duel.WinnerID = winnerID
	duel.FinishedAt = &finishedAt
	r.finishes[id]++
	snapshot := *duel
	return &snapshot, nil
}

func (r *timerDuelRepo) CreateDuelPlayerTask(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	panic("unused")
}

func (r *timerDuelRepo) GetDuelPlayerTask(context.Context, uuid.UUID, uuid.UUID) (*domain.DuelPlayerTask, error) {
	panic("unused")
}

func (r *timerDuelRepo) GetPlayerTask(context.Context, uuid.UUID, uuid.UUID) (*domain.Task, error) {
	panic("unused")
}

func (r *timerDuelRepo) MarkSolved(context.Context, uuid.UUID, uuid.UUID, time.Time) error {
	panic("unused")
}

type timerPlayerRepo struct {
	mu       sync.Mutex
	statuses map[uuid.UUID]domain.PlayerStatus
	players  map[uuid.UUID]*domain.Player
}

func newTimerPlayerRepo() *timerPlayerRepo {
	return &timerPlayerRepo{
		statuses: make(map[uuid.UUID]domain.PlayerStatus),
		players:  make(map[uuid.UUID]*domain.Player),
	}
}

func (r *timerPlayerRepo) status(playerID uuid.UUID) domain.PlayerStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.statuses[playerID]
}

func (r *timerPlayerRepo) register(player *domain.Player) {
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot := *player
	r.players[player.ID] = &snapshot
}

func (r *timerPlayerRepo) Create(context.Context, string) (*domain.Player, error) {
	panic("unused")
}

func (r *timerPlayerRepo) JoinByUsername(context.Context, string, uuid.UUID) (*domain.Player, error) {
	panic("unused")
}

func (r *timerPlayerRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.Player, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	player, ok := r.players[id]
	if !ok {
		panic("timerPlayerRepo: GetByID for unregistered player " + id.String())
	}
	snapshot := *player
	return &snapshot, nil
}

func (r *timerPlayerRepo) GetByUsername(context.Context, string) (*domain.Player, error) {
	panic("unused")
}

func (r *timerPlayerRepo) GetBySessionToken(context.Context, uuid.UUID) (*domain.Player, error) {
	panic("unused")
}

func (r *timerPlayerRepo) UpdateSessionToken(context.Context, uuid.UUID, *uuid.UUID) (*domain.Player, error) {
	panic("unused")
}

func (r *timerPlayerRepo) UpdateStatus(_ context.Context, id uuid.UUID, status domain.PlayerStatus) (*domain.Player, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statuses[id] = status
	return &domain.Player{ID: id, Status: status}, nil
}

func (r *timerPlayerRepo) UpdateStatusIfCurrent(
	_ context.Context,
	id uuid.UUID,
	from domain.PlayerStatus,
	to domain.PlayerStatus,
) (*domain.Player, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.statuses[id] != from {
		return nil, false, nil
	}
	r.statuses[id] = to
	return &domain.Player{ID: id, Status: to}, true, nil
}
