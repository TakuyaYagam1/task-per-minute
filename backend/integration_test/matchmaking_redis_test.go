//go:build integration

package integration_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	redisrepo "github.com/TakuyaYagam1/task-per-minute/internal/repo/redis"
)

func newMatchmakingRedis(t *testing.T) *redisrepo.MatchmakingRedis {
	t.Helper()
	return redisrepo.NewMatchmakingRedis(sharedRedis(t).client, "matchmaking:"+uniq("q"))
}

func TestMatchmakingRedis_PopPair_EmptyQueueReturnsFalse(t *testing.T) {
	t.Parallel()
	q := newMatchmakingRedis(t)

	first, second, ok, err := q.PopPair(context.Background())
	require.NoError(t, err)
	require.False(t, ok, "empty queue must report ok=false")
	require.Equal(t, uuid.Nil, first)
	require.Equal(t, uuid.Nil, second)
}

func TestMatchmakingRedis_PopPair_SinglePlayerWaits(t *testing.T) {
	t.Parallel()
	q := newMatchmakingRedis(t)
	ctx := context.Background()

	require.NoError(t, q.Enqueue(ctx, uuid.New()))

	_, _, ok, err := q.PopPair(ctx)
	require.NoError(t, err)
	require.False(t, ok, "queue with one entry must NOT pop a pair (atomic Lua)")
}

func TestMatchmakingRedis_EnqueueAndPopPair_FIFOOrder(t *testing.T) {
	t.Parallel()
	q := newMatchmakingRedis(t)
	ctx := context.Background()

	first := uuid.New()
	second := uuid.New()
	require.NoError(t, q.Enqueue(ctx, first))
	require.NoError(t, q.Enqueue(ctx, second))

	got1, got2, ok, err := q.PopPair(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, first, got1, "the player who joined first must be popped first (FIFO)")
	require.Equal(t, second, got2)
}

func TestMatchmakingRedis_Enqueue_DeduplicatesSamePlayer(t *testing.T) {
	t.Parallel()
	q := newMatchmakingRedis(t)
	ctx := context.Background()

	id := uuid.New()
	require.NoError(t, q.Enqueue(ctx, id))
	require.NoError(t, q.Enqueue(ctx, id), "re-enqueue must not produce a duplicate (LREM-then-LPUSH)")

	other := uuid.New()
	require.NoError(t, q.Enqueue(ctx, other))

	got1, got2, ok, err := q.PopPair(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotEqual(t, got1, got2, "same player must not appear twice in the popped pair")
}

func TestMatchmakingRedis_Remove_TakesPlayerOutOfQueue(t *testing.T) {
	t.Parallel()
	q := newMatchmakingRedis(t)
	ctx := context.Background()

	leaving := uuid.New()
	staying := uuid.New()
	require.NoError(t, q.Enqueue(ctx, leaving))
	require.NoError(t, q.Enqueue(ctx, staying))

	require.NoError(t, q.Remove(ctx, leaving))

	_, _, ok, err := q.PopPair(ctx)
	require.NoError(t, err)
	require.False(t, ok, "after removing one of two enqueued players the pair pop must miss")
}

func TestMatchmakingRedis_Clear_RemovesQueueAndIsIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	key := "matchmaking:" + uniq("clear")
	redis := sharedRedis(t).client
	q := redisrepo.NewMatchmakingRedis(redis, key)

	require.NoError(t, q.Enqueue(ctx, uuid.New()))
	require.NoError(t, q.Enqueue(ctx, uuid.New()))
	require.NoError(t, q.Clear(ctx))

	size, err := redis.LLen(ctx, key).Result()
	require.NoError(t, err)
	require.Zero(t, size)

	require.NoError(t, q.Clear(ctx), "clearing an absent queue should be a no-op")
	size, err = redis.LLen(ctx, key).Result()
	require.NoError(t, err)
	require.Zero(t, size)
}

func TestMatchmakingRedis_PopPair_ConcurrentAtomicity(t *testing.T) {
	t.Parallel()
	q := newMatchmakingRedis(t)
	ctx := context.Background()

	const players = 10
	for i := 0; i < players; i++ {
		require.NoError(t, q.Enqueue(ctx, uuid.New()))
	}

	const workers = 8
	type popResult struct {
		first, second uuid.UUID
		ok            bool
	}
	results := make([]popResult, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(idx int) {
			defer wg.Done()
			a, b, ok, err := q.PopPair(ctx)
			require.NoError(t, err)
			results[idx] = popResult{first: a, second: b, ok: ok}
		}(i)
	}
	wg.Wait()

	seen := make(map[uuid.UUID]int)
	pairs := 0
	for _, r := range results {
		if !r.ok {
			continue
		}
		pairs++
		seen[r.first]++
		seen[r.second]++
	}
	require.Equal(t, players/2, pairs, "10 players must yield 5 pairs across all workers")
	for id, count := range seen {
		require.Equalf(t, 1, count, "player %s appeared %d times across pairs (must be exactly 1)", id, count)
	}
}

func TestMatchmakingRedis_NilClient_ReturnsError(t *testing.T) {
	t.Parallel()
	q := redisrepo.NewMatchmakingRedis(nil, "matchmaking:nil")

	require.ErrorIs(t, q.Enqueue(context.Background(), uuid.New()), redisrepo.ErrNilClient)
	_, _, _, err := q.PopPair(context.Background())
	require.ErrorIs(t, err, redisrepo.ErrNilClient)
	require.ErrorIs(t, q.Remove(context.Background(), uuid.New()), redisrepo.ErrNilClient)
	require.ErrorIs(t, q.Clear(context.Background()), redisrepo.ErrNilClient)
}
