//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	redisrepo "github.com/TakuyaYagam1/task-per-minute/internal/repo/redis"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
)

type matchmakingFixture struct {
	*duelFixture
	uc    *duelusecase.MatchmakingUsecase
	queue *redisrepo.MatchmakingRedis
}

func newMatchmakingFixture(t *testing.T) *matchmakingFixture {
	t.Helper()

	f := newDuelFixture()
	queueKey := "matchmaking:" + uniq("q")
	queue := redisrepo.NewMatchmakingRedis(sharedRedis(t).client, queueKey)
	uc := duelusecase.NewMatchmakingUsecase(
		f.mgr,
		queue,
		f.players,
		f.tasks,
		f.history,
		f.duels,
		nil,
		fixedIntegrationClock{now: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)},
	)
	return &matchmakingFixture{duelFixture: f, uc: uc, queue: queue}
}

func TestDuelMatchmaking_TwoPlayersParallelCreatesOneDuel(t *testing.T) {
	f := newMatchmakingFixture(t)
	ctx := context.Background()

	f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 90)
	p1 := f.makePlayer(t, uniq("alice"))
	p2 := f.makePlayer(t, uniq("bob"))

	results := joinPlayersConcurrently(t, f.uc, p1.ID, p2.ID)
	require.Len(t, nonNilMatchResults(results), 1)

	result := nonNilMatchResults(results)[0]
	require.NotNil(t, result.Duel)
	require.NotNil(t, result.Player1Task)
	require.NotNil(t, result.Player2Task)

	got1, err := f.players.GetByID(ctx, p1.ID)
	require.NoError(t, err)
	require.Equal(t, domain.PlayerStatusInDuel, got1.Status)
	got2, err := f.players.GetByID(ctx, p2.ID)
	require.NoError(t, err)
	require.Equal(t, domain.PlayerStatusInDuel, got2.Status)

	_, err = f.duels.GetDuelPlayerTask(ctx, result.Duel.ID, p1.ID)
	require.NoError(t, err)
	_, err = f.duels.GetDuelPlayerTask(ctx, result.Duel.ID, p2.ID)
	require.NoError(t, err)
}

func TestDuelMatchmaking_ProgressionSelectsUnlockedDifficulty(t *testing.T) {
	f := newMatchmakingFixture(t)
	ctx := context.Background()

	f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 60)
	medium := f.makeTaskWithLimit(t, uniq("medium"), domain.DifficultyMedium, 120)
	playerA := f.makePlayer(t, uniq("alice"))
	playerB := f.makePlayer(t, uniq("bob"))

	easyTasks, err := f.tasks.ListByDifficulty(ctx, domain.DifficultyEasy)
	require.NoError(t, err)
	for _, task := range easyTasks {
		require.NoError(t, f.history.AddSolved(ctx, playerA.ID, task.ID, time.Now().UTC()))
	}

	first, err := f.uc.JoinQueue(ctx, playerA.ID)
	require.NoError(t, err)
	require.Nil(t, first)

	result, err := f.uc.JoinQueue(ctx, playerB.ID)
	require.NoError(t, err)
	require.NotNil(t, result)

	taskA := taskForPlayer(t, result, playerA.ID)
	taskB := taskForPlayer(t, result, playerB.ID)
	require.Equal(t, medium.ID, taskA.ID)
	require.Equal(t, domain.DifficultyEasy, taskB.Difficulty)
}

func TestDuelMatchmaking_RaceTenPlayersCreatesFiveDuels(t *testing.T) {
	f := newMatchmakingFixture(t)

	for i := 0; i < 5; i++ {
		f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 60+i)
	}

	playerIDs := make([]uuid.UUID, 0, 10)
	for i := 0; i < 10; i++ {
		playerIDs = append(playerIDs, f.makePlayer(t, uniq("p")).ID)
	}

	results := joinPlayersConcurrently(t, f.uc, playerIDs...)
	require.Len(t, nonNilMatchResults(results), 5)

	seenDuels := make(map[uuid.UUID]struct{}, 5)
	for _, result := range nonNilMatchResults(results) {
		require.NotNil(t, result.Duel)
		seenDuels[result.Duel.ID] = struct{}{}
	}
	require.Len(t, seenDuels, 5)
}

func TestDuelMatchmaking_LeaveQueueRemovesPlayerAndSetsIdle(t *testing.T) {
	f := newMatchmakingFixture(t)
	ctx := context.Background()
	player := f.makePlayer(t, uniq("alice"))

	result, err := f.uc.JoinQueue(ctx, player.ID)
	require.NoError(t, err)
	require.Nil(t, result)

	require.NoError(t, f.uc.LeaveQueue(ctx, player.ID))
	got, err := f.players.GetByID(ctx, player.ID)
	require.NoError(t, err)
	require.Equal(t, domain.PlayerStatusIdle, got.Status)

	next := f.makePlayer(t, uniq("bob"))
	result, err = f.uc.JoinQueue(ctx, next.ID)
	require.NoError(t, err)
	require.Nil(t, result, "leaving the queue must remove the previous player from Redis")
}

func TestDuelMatchmaking_StaleInDuelQueueEntryDoesNotResetActivePlayer(t *testing.T) {
	f := newMatchmakingFixture(t)
	ctx := context.Background()

	f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 60)
	active := f.makePlayer(t, uniq("active"))
	waiting := f.makePlayer(t, uniq("waiting"))
	next := f.makePlayer(t, uniq("next"))

	_, err := f.players.UpdateStatus(ctx, active.ID, domain.PlayerStatusInDuel)
	require.NoError(t, err)
	require.NoError(t, f.queue.Enqueue(ctx, active.ID), "simulate stale Redis queue entry from a previous process")

	result, err := f.uc.JoinQueue(ctx, waiting.ID)
	require.NoError(t, err)
	require.Nil(t, result, "stale in_duel entry must be skipped instead of creating a new duel")

	gotActive, err := f.players.GetByID(ctx, active.ID)
	require.NoError(t, err)
	require.Equal(t, domain.PlayerStatusInDuel, gotActive.Status, "foreign stale pair must not reset active player to idle")
	gotWaiting, err := f.players.GetByID(ctx, waiting.ID)
	require.NoError(t, err)
	require.Equal(t, domain.PlayerStatusQueued, gotWaiting.Status, "valid queued player should be requeued")

	result, err = f.uc.JoinQueue(ctx, next.ID)
	require.NoError(t, err)
	require.NotNil(t, result, "requeued valid player should still be matchable")
	require.Contains(t, []uuid.UUID{result.Duel.Player1ID, result.Duel.Player2ID}, waiting.ID)
	require.Contains(t, []uuid.UUID{result.Duel.Player1ID, result.Duel.Player2ID}, next.ID)

	gotActive, err = f.players.GetByID(ctx, active.ID)
	require.NoError(t, err)
	require.Equal(t, domain.PlayerStatusInDuel, gotActive.Status)
}
