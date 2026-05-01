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

// recordingBroadcaster is a usecase.DuelBroadcaster fake that captures every
// fan-out event the reconnect manager emits. Tests assert against the
// captured slices instead of touching real WebSocket transport.
type recordingBroadcaster struct {
	mu       sync.Mutex
	disconns []reconnDisconnectEvent
	expired  []uuid.UUID
	finished []*domain.Duel
}

type reconnDisconnectEvent struct {
	duelID            uuid.UUID
	playerID          uuid.UUID
	reconnectDeadline time.Time
}

func (b *recordingBroadcaster) BroadcastOpponentDisconnected(_ context.Context, duelID, playerID uuid.UUID, deadline time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.disconns = append(b.disconns, reconnDisconnectEvent{duelID: duelID, playerID: playerID, reconnectDeadline: deadline})
}

func (b *recordingBroadcaster) BroadcastDuelExpired(_ context.Context, duelID uuid.UUID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.expired = append(b.expired, duelID)
}

func (b *recordingBroadcaster) BroadcastDuelFinished(_ context.Context, duel *domain.Duel) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.finished = append(b.finished, duel)
}

func (b *recordingBroadcaster) snapshotDisconnects() []reconnDisconnectEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]reconnDisconnectEvent, len(b.disconns))
	copy(out, b.disconns)
	return out
}

func (b *recordingBroadcaster) finishedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.finished)
}

// fakeTimer satisfies duelusecase.DuelTimer with deterministic behaviour
// driven by the test (no real time.AfterFunc).
type fakeTimer struct {
	mu       sync.Mutex
	freezes  []uuid.UUID
	resumes  map[uuid.UUID]time.Time
	resumeAt map[uuid.UUID]time.Time
	stops    []uuid.UUID
}

func newFakeTimer() *fakeTimer {
	return &fakeTimer{
		resumes:  make(map[uuid.UUID]time.Time),
		resumeAt: make(map[uuid.UUID]time.Time),
	}
}

func (t *fakeTimer) Start(_ uuid.UUID, _ time.Time, _ func()) {}

func (t *fakeTimer) Stop(duelID uuid.UUID) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stops = append(t.stops, duelID)
	return true
}

func (t *fakeTimer) Freeze(duelID uuid.UUID, _ time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.freezes = append(t.freezes, duelID)
	return true
}

func (t *fakeTimer) Resume(duelID uuid.UUID, resumedAt time.Time) (time.Time, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resumes[duelID] = resumedAt
	deadline, ok := t.resumeAt[duelID]
	if !ok {
		// default: pretend the timer had 30s of remaining slack at freeze.
		deadline = resumedAt.Add(30 * time.Second)
	}
	return deadline, true
}

func (t *fakeTimer) wasFrozen(duelID uuid.UUID) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, id := range t.freezes {
		if id == duelID {
			return true
		}
	}
	return false
}

// reconnDuelRepo extends the timer test fake with UpdateDeadline + active-by-player support.
type reconnDuelRepo struct {
	*timerDuelRepo
	mu       sync.Mutex
	byPlayer map[uuid.UUID]uuid.UUID
}

func newReconnDuelRepo() *reconnDuelRepo {
	return &reconnDuelRepo{
		timerDuelRepo: newTimerDuelRepo(),
		byPlayer:      make(map[uuid.UUID]uuid.UUID),
	}
}

func (r *reconnDuelRepo) putWithPlayers(duel *domain.Duel) {
	r.put(duel)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byPlayer[duel.Player1ID] = duel.ID
	r.byPlayer[duel.Player2ID] = duel.ID
}

func (r *reconnDuelRepo) GetActiveByPlayerID(ctx context.Context, playerID uuid.UUID) (*domain.Duel, error) {
	r.mu.Lock()
	id, ok := r.byPlayer[playerID]
	r.mu.Unlock()
	if !ok {
		return nil, nil
	}
	return r.GetByID(ctx, id)
}

func (r *reconnDuelRepo) UpdateDeadline(_ context.Context, id uuid.UUID, deadline time.Time) (*domain.Duel, error) {
	r.timerDuelRepo.mu.Lock()
	defer r.timerDuelRepo.mu.Unlock()
	duel, ok := r.duels[id]
	if !ok {
		return nil, apperr.ErrDuelNotFound
	}
	duel.Deadline = deadline
	snapshot := *duel
	return &snapshot, nil
}

func newReconnectFixture(t *testing.T, options ...duelusecase.ReconnectOption) (
	*duelusecase.ReconnectManager,
	*reconnDuelRepo,
	*timerPlayerRepo,
	*recordingBroadcaster,
	*fakeTimer,
) {
	t.Helper()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	duels := newReconnDuelRepo()
	players := newTimerPlayerRepo()
	timer := newFakeTimer()
	broadcaster := &recordingBroadcaster{}
	mgr := duelusecase.NewReconnectManager(timerTx{}, duels, players, timer, broadcaster, fixedClock{now: now}, options...)
	return mgr, duels, players, broadcaster, timer
}

func TestReconnectManager_HandleDisconnect_FreezesTimerAndBroadcasts(t *testing.T) {
	t.Parallel()

	mgr, duels, _, broadcaster, timer := newReconnectFixture(t)
	duel := activeDuel(time.Now().Add(time.Hour))
	duels.putWithPlayers(duel)

	mgr.HandleDisconnect(context.Background(), duel.ID, duel.Player1ID)

	require.True(t, timer.wasFrozen(duel.ID), "timer must be frozen on first disconnect")
	events := broadcaster.snapshotDisconnects()
	require.Len(t, events, 1)
	require.Equal(t, duel.Player1ID, events[0].playerID)
	require.False(t, events[0].reconnectDeadline.IsZero())
}

func TestReconnectManager_HandleDisconnect_ExceedsLimit_FinalizesForOpponent(t *testing.T) {
	t.Parallel()

	// limit = 1 → second disconnect by the same player ends the duel.
	mgr, duels, players, broadcaster, _ := newReconnectFixture(t, duelusecase.WithReconnectDisconnectLimit(1))
	duel := activeDuel(time.Now().Add(time.Hour))
	duels.putWithPlayers(duel)

	mgr.HandleDisconnect(context.Background(), duel.ID, duel.Player1ID)
	require.NoError(t, simulateReconnect(mgr, duel.Player1ID))
	mgr.HandleDisconnect(context.Background(), duel.ID, duel.Player1ID)

	require.Equal(t, 1, broadcaster.finishedCount(), "exhausted limit must finalize the duel")
	got, err := duels.GetByID(context.Background(), duel.ID)
	require.NoError(t, err)
	require.Equal(t, domain.DuelStatusFinished, got.Status)
	require.NotNil(t, got.WinnerID)
	require.Equal(t, duel.Player2ID, *got.WinnerID, "opponent must be the winner")
	require.Equal(t, domain.PlayerStatusIdle, players.status(duel.Player1ID))
	require.Equal(t, domain.PlayerStatusIdle, players.status(duel.Player2ID))
}

func TestReconnectManager_ConsumeReconnect_ResumesAndUpdatesDeadline(t *testing.T) {
	t.Parallel()

	mgr, duels, _, _, _ := newReconnectFixture(t)
	duel := activeDuel(time.Now().Add(time.Hour))
	duels.putWithPlayers(duel)

	mgr.HandleDisconnect(context.Background(), duel.ID, duel.Player1ID)

	decision, err := mgr.ConsumeReconnect(context.Background(), duel.Player1ID)
	require.NoError(t, err)
	require.NotNil(t, decision)
	require.True(t, decision.Resume)
	require.False(t, decision.WindowExpired)
	require.False(t, decision.OpponentExpired)
	require.Equal(t, duel.Player2ID, decision.OpponentID)
	require.False(t, decision.NewDeadline.IsZero(), "Resume path must populate NewDeadline")

	updated, err := duels.GetByID(context.Background(), duel.ID)
	require.NoError(t, err)
	require.Equal(t, decision.NewDeadline, updated.Deadline, "DB deadline must match resumed deadline")
}

func TestReconnectManager_ConsumeReconnect_NoActiveDuelReturnsNil(t *testing.T) {
	t.Parallel()

	mgr, _, _, _, _ := newReconnectFixture(t)

	decision, err := mgr.ConsumeReconnect(context.Background(), uuid.New())
	require.NoError(t, err)
	require.Nil(t, decision, "player without active duel produces no decision")
}

func TestReconnectManager_FinalizeOpponentForfeit_FinishesDuelAndBroadcasts(t *testing.T) {
	t.Parallel()

	mgr, duels, players, broadcaster, _ := newReconnectFixture(t)
	duel := activeDuel(time.Now().Add(time.Hour))
	duels.putWithPlayers(duel)

	mgr.FinalizeOpponentForfeit(context.Background(), duel.ID, duel.Player1ID)

	require.Equal(t, 1, broadcaster.finishedCount())
	got, err := duels.GetByID(context.Background(), duel.ID)
	require.NoError(t, err)
	require.Equal(t, domain.DuelStatusFinished, got.Status)
	require.NotNil(t, got.WinnerID)
	require.Equal(t, duel.Player1ID, *got.WinnerID)
	require.Equal(t, domain.PlayerStatusIdle, players.status(duel.Player1ID))
	require.Equal(t, domain.PlayerStatusIdle, players.status(duel.Player2ID))
}

func TestReconnectManager_StartDuelTimer_ExpiryBroadcastsExpiredAndFinished(t *testing.T) {
	t.Parallel()

	// Use the real TimerRegistry (not the fake) with a short deadline so the
	// timer actually fires and exercises the broadcaster wiring.
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	duels := newReconnDuelRepo()
	players := newTimerPlayerRepo()
	timers := duelusecase.NewTimerRegistry(timerTx{}, duels, players, fixedClock{now: now})
	broadcaster := &recordingBroadcaster{}
	mgr := duelusecase.NewReconnectManager(timerTx{}, duels, players, timers, broadcaster, fixedClock{now: now})

	duel := activeDuel(now.Add(10 * time.Millisecond))
	duels.putWithPlayers(duel)

	mgr.StartDuelTimer(duel)

	require.Eventually(t, func() bool {
		return broadcaster.finishedCount() == 1
	}, time.Second, time.Millisecond, "duel must finalize when timer expires")

	require.Len(t, broadcaster.expired, 1, "expired event must be broadcast once")
	require.Equal(t, duel.ID, broadcaster.expired[0])

	got, err := duels.GetByID(context.Background(), duel.ID)
	require.NoError(t, err)
	require.Equal(t, domain.DuelStatusFinished, got.Status)
	require.Nil(t, got.WinnerID, "deadline expiry produces a draw")
}

// simulateReconnect mimics what the controller does on a fresh connection:
// it calls ConsumeReconnect and discards the decision (the test only cares
// that state is consumed for the next disconnect cycle).
func simulateReconnect(mgr *duelusecase.ReconnectManager, playerID uuid.UUID) error {
	_, err := mgr.ConsumeReconnect(context.Background(), playerID)
	return err
}
