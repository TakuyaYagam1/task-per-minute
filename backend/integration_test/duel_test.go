//go:build integration

package integration_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	redisrepo "github.com/TakuyaYagam1/task-per-minute/internal/repo/redis"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
)

type duelScenarioFixture struct {
	*duelFixture
	boardStore  *redisrepo.LeaderboardRedis
	matchmaking *duelusecase.MatchmakingUsecase
	flags       *duelusecase.FlagSubmitUsecase
	leaderboard *leaderboardusecase.LeaderboardUsecase
	now         time.Time
}

type duelScenarioTimer struct {
	mu             sync.Mutex
	started        map[uuid.UUID]func()
	frozen         []uuid.UUID
	stopped        []uuid.UUID
	resumeDeadline time.Time
}

func (t *duelScenarioTimer) Start(duelID uuid.UUID, _ time.Time, onExpire func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.started == nil {
		t.started = make(map[uuid.UUID]func())
	}
	t.started[duelID] = onExpire
}

func (t *duelScenarioTimer) Stop(duelID uuid.UUID) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopped = append(t.stopped, duelID)
	return true
}

func (t *duelScenarioTimer) Freeze(duelID uuid.UUID, _ time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.frozen = append(t.frozen, duelID)
	return true
}

func (t *duelScenarioTimer) Resume(_ uuid.UUID, resumedAt time.Time) (time.Time, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.resumeDeadline.IsZero() {
		return resumedAt.Add(30 * time.Second), true
	}
	return t.resumeDeadline, true
}

func (t *duelScenarioTimer) hasFrozen(duelID uuid.UUID) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, id := range t.frozen {
		if id == duelID {
			return true
		}
	}
	return false
}

func (t *duelScenarioTimer) fire(duelID uuid.UUID) {
	t.mu.Lock()
	onExpire := t.started[duelID]
	t.mu.Unlock()
	if onExpire != nil {
		onExpire()
	}
}

type duelScenarioBroadcaster struct {
	mu       sync.Mutex
	finished []*domain.Duel
	expired  []uuid.UUID
}

func (b *duelScenarioBroadcaster) BroadcastOpponentDisconnected(context.Context, uuid.UUID, uuid.UUID, time.Time) {
}

func (b *duelScenarioBroadcaster) BroadcastDuelExpired(_ context.Context, duelID uuid.UUID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.expired = append(b.expired, duelID)
}

func (b *duelScenarioBroadcaster) BroadcastDuelFinished(_ context.Context, duel *domain.Duel) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.finished = append(b.finished, duel)
}

func (b *duelScenarioBroadcaster) finishedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.finished)
}

func (b *duelScenarioBroadcaster) expiredCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.expired)
}

func newDuelScenarioFixture(t *testing.T) *duelScenarioFixture {
	t.Helper()

	base := newIsolatedDuelFixture(t)
	boardStore := redisrepo.NewLeaderboardRedis(sharedRedis(t).client, "leaderboard:duel:"+uniq("z"))
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	f := &duelScenarioFixture{
		duelFixture: base,
		boardStore:  boardStore,
		now:         now,
	}
	f.matchmaking = duelusecase.NewMatchmakingUsecase(
		base.mgr,
		redisrepo.NewMatchmakingRedis(sharedRedis(t).client, "matchmaking:duel:"+uniq("q")),
		base.players,
		base.tasks,
		base.history,
		base.duels,
		nil,
		fixedIntegrationClock{now: now},
	)
	f.flags = duelusecase.NewFlagSubmitUsecase(
		base.mgr,
		base.duels,
		base.players,
		base.history,
		boardStore,
		fixedIntegrationClock{now: now.Add(10 * time.Second)},
	)
	f.leaderboard = leaderboardusecase.NewLeaderboardUsecase(
		boardStore,
		base.board,
		fixedIntegrationClock{now: now.Add(time.Minute)},
	)
	return f
}

func (f *duelScenarioFixture) matchPlayers(t *testing.T, first, second uuid.UUID) *duelusecase.MatchResult {
	t.Helper()
	ctx := context.Background()

	result, err := f.matchmaking.JoinQueue(ctx, first)
	require.NoError(t, err)
	require.Nil(t, result)

	result, err = f.matchmaking.JoinQueue(ctx, second)
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}

func (f *duelScenarioFixture) markSolved(t *testing.T, playerID uuid.UUID, tasks ...*domain.Task) {
	t.Helper()
	f.duelFixture.markSolvedAt(t, playerID, f.now, tasks...)
}

func (f *duelScenarioFixture) submitWinningFlag(t *testing.T, result *duelusecase.MatchResult, playerID uuid.UUID) {
	t.Helper()
	task := taskForPlayer(t, result, playerID)
	got, err := f.flags.SubmitFlag(context.Background(), result.Duel.ID, playerID, task.Flag)
	require.NoError(t, err)
	require.True(t, got.Correct)
	require.Equal(t, playerID, got.Winner.ID)
}

func TestDuel_ProgressionUnlocksMediumThenHard(t *testing.T) {
	f := newDuelScenarioFixture(t)

	easy := f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 60)
	medium := f.makeTaskWithLimit(t, uniq("medium"), domain.DifficultyMedium, 90)
	hard := f.makeTaskWithLimit(t, uniq("hard"), domain.DifficultyHard, 120)
	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))
	charlie := f.makePlayer(t, uniq("charlie"))

	f.markSolved(t, alice.ID, easy)
	firstDuel := f.matchPlayers(t, alice.ID, bob.ID)
	require.Equal(t, medium.ID, taskForPlayer(t, firstDuel, alice.ID).ID)
	require.Equal(t, domain.DifficultyEasy, taskForPlayer(t, firstDuel, bob.ID).Difficulty)

	f.submitWinningFlag(t, firstDuel, alice.ID)
	secondDuel := f.matchPlayers(t, alice.ID, charlie.ID)
	require.Equal(t, hard.ID, taskForPlayer(t, secondDuel, alice.ID).ID)
	require.Equal(t, domain.DifficultyEasy, taskForPlayer(t, secondDuel, charlie.ID).Difficulty)
}

func TestDuel_IndividualPoolsCanAssignDifferentDifficulties(t *testing.T) {
	f := newDuelScenarioFixture(t)

	easy := f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 60)
	medium := f.makeTaskWithLimit(t, uniq("medium"), domain.DifficultyMedium, 90)
	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))

	f.markSolved(t, alice.ID, easy)
	result := f.matchPlayers(t, alice.ID, bob.ID)

	require.Equal(t, medium.ID, taskForPlayer(t, result, alice.ID).ID)
	require.Equal(t, easy.ID, taskForPlayer(t, result, bob.ID).ID)
}

func TestDuel_IndividualPoolsCoverMixedDifficultyPairs(t *testing.T) {
	f := newDuelScenarioFixture(t)

	easy := f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 60)
	medium := f.makeTaskWithLimit(t, uniq("medium"), domain.DifficultyMedium, 90)
	hard := f.makeTaskWithLimit(t, uniq("hard"), domain.DifficultyHard, 120)

	mediumPlayer := f.makePlayer(t, uniq("medium"))
	easyPlayer := f.makePlayer(t, uniq("easy"))
	f.markSolved(t, mediumPlayer.ID, easy)
	easyMedium := f.matchPlayers(t, mediumPlayer.ID, easyPlayer.ID)
	require.Equal(t, medium.ID, taskForPlayer(t, easyMedium, mediumPlayer.ID).ID)
	require.Equal(t, easy.ID, taskForPlayer(t, easyMedium, easyPlayer.ID).ID)

	hardPlayer := f.makePlayer(t, uniq("hard"))
	newPlayer := f.makePlayer(t, uniq("new"))
	f.markSolved(t, hardPlayer.ID, easy, medium)
	easyHard := f.matchPlayers(t, hardPlayer.ID, newPlayer.ID)
	require.Equal(t, hard.ID, taskForPlayer(t, easyHard, hardPlayer.ID).ID)
	require.Equal(t, easy.ID, taskForPlayer(t, easyHard, newPlayer.ID).ID)

	anotherHardPlayer := f.makePlayer(t, uniq("hard"))
	anotherMediumPlayer := f.makePlayer(t, uniq("medium"))
	f.markSolved(t, anotherHardPlayer.ID, easy, medium)
	f.markSolved(t, anotherMediumPlayer.ID, easy)
	mediumHard := f.matchPlayers(t, anotherHardPlayer.ID, anotherMediumPlayer.ID)
	require.Equal(t, hard.ID, taskForPlayer(t, mediumHard, anotherHardPlayer.ID).ID)
	require.Equal(t, medium.ID, taskForPlayer(t, mediumHard, anotherMediumPlayer.ID).ID)
}

func TestDuel_LeaderboardTiebreakerUsesAverageSolveTime(t *testing.T) {
	f := newDuelScenarioFixture(t)
	ctx := context.Background()

	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))
	opponent := f.makePlayer(t, uniq("opponent"))
	tasks := make([]*domain.Task, 0, 6)
	for i := 0; i < 6; i++ {
		tasks = append(tasks, f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 60))
	}

	startedAt := f.now.Add(-time.Hour)
	for i, solveTime := range []time.Duration{5 * time.Second, 6 * time.Second, 7 * time.Second} {
		f.createSolvedWin(t, alice.ID, opponent.ID, alice.ID, tasks[i].ID, startedAt.Add(time.Duration(i)*time.Minute), solveTime)
		require.NoError(t, f.leaderboard.IncrementWin(ctx, alice.Username))
	}
	for i, solveTime := range []time.Duration{time.Second, 2 * time.Second, 3 * time.Second} {
		f.createSolvedWin(t, bob.ID, opponent.ID, bob.ID, tasks[i+3].ID, startedAt.Add(time.Duration(i+3)*time.Minute), solveTime)
		require.NoError(t, f.leaderboard.IncrementWin(ctx, bob.Username))
	}

	entries, err := f.leaderboard.Top50(ctx)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, bob.Username, entries[0].Username)
	require.Equal(t, 3, entries[0].Wins)
	require.Equal(t, int64(2_000), entries[0].AverageSolveTimeMs)
	require.Equal(t, alice.Username, entries[1].Username)
	require.Equal(t, 3, entries[1].Wins)
	require.Equal(t, int64(6_000), entries[1].AverageSolveTimeMs)
}

func TestDuel_AntiRepeatSelectsAnotherTaskWhenAvailable(t *testing.T) {
	f := newDuelScenarioFixture(t)

	solved := f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 60)
	unsolved := f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 60)
	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))

	f.markSolved(t, alice.ID, solved)
	result := f.matchPlayers(t, alice.ID, bob.ID)

	require.Equal(t, unsolved.ID, taskForPlayer(t, result, alice.ID).ID)
	require.Equal(t, unsolved.ID, taskForPlayer(t, result, bob.ID).ID)
}

func TestDuel_SamePoolAssignsDistinctTasksWhenAvailable(t *testing.T) {
	f := newDuelScenarioFixture(t)

	first := f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 60)
	second := f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 60)
	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))

	result := f.matchPlayers(t, alice.ID, bob.ID)

	aliceTask := taskForPlayer(t, result, alice.ID)
	bobTask := taskForPlayer(t, result, bob.ID)
	require.NotEqual(t, aliceTask.ID, bobTask.ID)
	require.Contains(t, []uuid.UUID{first.ID, second.ID}, aliceTask.ID)
	require.Contains(t, []uuid.UUID{first.ID, second.ID}, bobTask.ID)
}

func TestDuel_SamePoolSwapsPlayerSolvedTasks(t *testing.T) {
	f := newDuelScenarioFixture(t)

	sun := f.makeTaskWithLimit(t, uniq("sun"), domain.DifficultyEasy, 60)
	moon := f.makeTaskWithLimit(t, uniq("moon"), domain.DifficultyEasy, 60)
	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))

	f.markSolved(t, alice.ID, sun)
	f.markSolved(t, bob.ID, moon)
	result := f.matchPlayers(t, alice.ID, bob.ID)

	require.Equal(t, moon.ID, taskForPlayer(t, result, alice.ID).ID)
	require.Equal(t, sun.ID, taskForPlayer(t, result, bob.ID).ID)
}

func TestDuel_FallbackReusesSolvedTaskWhenPoolIsExhausted(t *testing.T) {
	f := newDuelScenarioFixture(t)

	onlyEasy := f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 60)
	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))

	f.markSolved(t, alice.ID, onlyEasy)
	result := f.matchPlayers(t, alice.ID, bob.ID)

	require.Equal(t, onlyEasy.ID, taskForPlayer(t, result, alice.ID).ID)
}

func TestDuel_ReadUsecaseReturnsParticipantDetail(t *testing.T) {
	f := newDuelScenarioFixture(t)
	ctx := context.Background()

	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))
	stranger := f.makePlayer(t, uniq("stranger"))
	aliceTask := f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 60)
	bobTask := f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 60)
	duel := f.makeActiveDuel(t, alice.ID, bob.ID, f.now.Add(time.Minute))
	require.NoError(t, f.duels.CreateDuelPlayerTask(ctx, duel.ID, alice.ID, aliceTask.ID))
	require.NoError(t, f.duels.CreateDuelPlayerTask(ctx, duel.ID, bob.ID, bobTask.ID))

	detail, err := duelusecase.NewReadUsecase(f.duels).GetDuel(ctx, duel.ID, alice.ID)
	require.NoError(t, err)
	require.Equal(t, duel.ID, detail.Duel.ID)
	require.Len(t, detail.PlayerTasks, 2)

	_, err = duelusecase.NewReadUsecase(f.duels).GetDuel(ctx, duel.ID, stranger.ID)
	require.ErrorIs(t, err, apperr.ErrNotDuelParticipant)
}

func TestDuel_ReconnectResumeUpdatesDeadline(t *testing.T) {
	f := newDuelScenarioFixture(t)
	ctx := context.Background()

	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))
	duel := f.makeActiveDuel(t, alice.ID, bob.ID, f.now.Add(time.Minute))
	timer := &duelScenarioTimer{resumeDeadline: f.now.Add(45 * time.Second)}
	mgr := duelusecase.NewReconnectManager(
		f.mgr,
		f.duels,
		f.players,
		timer,
		&duelScenarioBroadcaster{},
		fixedIntegrationClock{now: f.now},
		duelusecase.WithReconnectWindow(time.Second),
	)

	mgr.HandleDisconnect(ctx, duel.ID, alice.ID)
	require.True(t, timer.hasFrozen(duel.ID))

	decision, err := mgr.ConsumeReconnect(ctx, alice.ID)
	require.NoError(t, err)
	require.NotNil(t, decision)
	require.True(t, decision.Resume)
	require.Equal(t, bob.ID, decision.OpponentID)
	require.WithinDuration(t, timer.resumeDeadline, decision.NewDeadline, time.Second)

	updated, err := f.duels.GetByID(ctx, duel.ID)
	require.NoError(t, err)
	require.WithinDuration(t, timer.resumeDeadline, updated.Deadline, time.Second)
}

func TestDuel_ReconnectDisconnectLimitFinalizesDraw(t *testing.T) {
	f := newDuelScenarioFixture(t)
	ctx := context.Background()

	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))
	duel := f.makeActiveDuel(t, alice.ID, bob.ID, f.now.Add(time.Minute))
	broadcaster := &duelScenarioBroadcaster{}
	mgr := duelusecase.NewReconnectManager(
		f.mgr,
		f.duels,
		f.players,
		&duelScenarioTimer{},
		broadcaster,
		fixedIntegrationClock{now: f.now},
		duelusecase.WithReconnectDisconnectLimit(0),
	)

	mgr.HandleDisconnect(ctx, duel.ID, alice.ID)

	got, err := f.duels.GetByID(ctx, duel.ID)
	require.NoError(t, err)
	require.Equal(t, domain.DuelStatusFinished, got.Status)
	require.Nil(t, got.WinnerID)
	require.Equal(t, 1, broadcaster.finishedCount())
}

func TestDuel_ReconnectWindowExpiryFinalizesDraw(t *testing.T) {
	f := newDuelScenarioFixture(t)
	ctx := context.Background()

	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))
	duel := f.makeActiveDuel(t, alice.ID, bob.ID, f.now.Add(time.Minute))
	mgr := duelusecase.NewReconnectManager(
		f.mgr,
		f.duels,
		f.players,
		&duelScenarioTimer{},
		&duelScenarioBroadcaster{},
		fixedIntegrationClock{now: f.now},
		duelusecase.WithReconnectWindow(10*time.Millisecond),
	)

	mgr.HandleDisconnect(ctx, duel.ID, alice.ID)

	require.Eventually(t, func() bool {
		got, err := f.duels.GetByID(ctx, duel.ID)
		return err == nil &&
			got.Status == domain.DuelStatusFinished &&
			got.WinnerID == nil
	}, time.Second, 10*time.Millisecond)
}

func TestDuel_ReconnectFinalizeDraw(t *testing.T) {
	f := newDuelScenarioFixture(t)
	ctx := context.Background()

	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))
	duel := f.makeActiveDuel(t, alice.ID, bob.ID, f.now.Add(time.Minute))
	mgr := duelusecase.NewReconnectManager(
		f.mgr,
		f.duels,
		f.players,
		&duelScenarioTimer{},
		&duelScenarioBroadcaster{},
		fixedIntegrationClock{now: f.now},
	)

	finished, err := mgr.FinalizeDraw(ctx, duel.ID)
	require.NoError(t, err)
	require.NotNil(t, finished)

	got, err := f.duels.GetByID(ctx, duel.ID)
	require.NoError(t, err)
	require.Equal(t, domain.DuelStatusFinished, got.Status)
	require.Nil(t, got.WinnerID)
}

func TestDuel_ReconnectFinalizeDraw_DoesNotBumpLeaderboard(t *testing.T) {
	f := newDuelScenarioFixture(t)
	ctx := context.Background()

	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))
	duel := f.makeActiveDuel(t, alice.ID, bob.ID, f.now.Add(time.Minute))
	mgr := duelusecase.NewReconnectManager(
		f.mgr,
		f.duels,
		f.players,
		&duelScenarioTimer{},
		&duelScenarioBroadcaster{},
		fixedIntegrationClock{now: f.now},
		duelusecase.WithLeaderboardStore(f.boardStore),
	)

	_, err := mgr.FinalizeDraw(ctx, duel.ID)
	require.NoError(t, err)

	scores, err := f.boardStore.WinScores(ctx)
	require.NoError(t, err)
	require.Empty(t, scores, "draw must not add any entry to the leaderboard ZSET")
}

func TestDuel_ReconnectDuelTimerExpiryBroadcastsFinished(t *testing.T) {
	f := newDuelScenarioFixture(t)

	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))
	duel := f.makeActiveDuel(t, alice.ID, bob.ID, f.now.Add(time.Minute))
	timer := &duelScenarioTimer{}
	broadcaster := &duelScenarioBroadcaster{}
	mgr := duelusecase.NewReconnectManager(
		f.mgr,
		f.duels,
		f.players,
		timer,
		broadcaster,
		fixedIntegrationClock{now: f.now},
	)

	mgr.StartDuelTimer(duel)
	timer.fire(duel.ID)

	require.Equal(t, 1, broadcaster.expiredCount())
	require.Equal(t, 1, broadcaster.finishedCount())
}
