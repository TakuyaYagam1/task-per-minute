//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	redisrepo "github.com/TakuyaYagam1/task-per-minute/internal/repo/redis"
)

func newLeaderboardRedis(t *testing.T) *redisrepo.LeaderboardRedis {
	t.Helper()
	return redisrepo.NewLeaderboardRedis(sharedRedis(t).client, "leaderboard:"+uniq("z"))
}

func TestLeaderboardRedis_IncrementWin_PersistsScore(t *testing.T) {
	t.Parallel()
	repo := newLeaderboardRedis(t)
	ctx := context.Background()

	alice := uniq("alice")
	require.NoError(t, repo.IncrementWin(ctx, alice))

	scores, err := repo.WinScores(ctx)
	require.NoError(t, err)
	require.Len(t, scores, 1)
	require.Equal(t, alice, scores[0].Username)
	require.Equal(t, 1, scores[0].Wins)
}

func TestLeaderboardRedis_IncrementWin_AccumulatesPerUser(t *testing.T) {
	t.Parallel()
	repo := newLeaderboardRedis(t)
	ctx := context.Background()

	alice := uniq("alice")
	bob := uniq("bob")

	require.NoError(t, repo.IncrementWin(ctx, alice))
	require.NoError(t, repo.IncrementWin(ctx, alice))
	require.NoError(t, repo.IncrementWin(ctx, alice))
	require.NoError(t, repo.IncrementWin(ctx, bob))

	scores, err := repo.WinScores(ctx)
	require.NoError(t, err)
	got := make(map[string]int, 2)
	for _, s := range scores {
		got[s.Username] = s.Wins
	}
	require.Equal(t, 3, got[alice])
	require.Equal(t, 1, got[bob])
}

func TestLeaderboardRedis_WinScores_OrderedByScoreDesc(t *testing.T) {
	t.Parallel()
	repo := newLeaderboardRedis(t)
	ctx := context.Background()

	a := uniq("a")
	b := uniq("b")
	c := uniq("c")
	for i := 0; i < 5; i++ {
		require.NoError(t, repo.IncrementWin(ctx, a))
	}
	for i := 0; i < 2; i++ {
		require.NoError(t, repo.IncrementWin(ctx, b))
	}
	require.NoError(t, repo.IncrementWin(ctx, c))

	scores, err := repo.WinScores(ctx)
	require.NoError(t, err)
	require.Len(t, scores, 3)
	require.Equal(t, a, scores[0].Username)
	require.Equal(t, 5, scores[0].Wins)
	require.Equal(t, b, scores[1].Username)
	require.Equal(t, 2, scores[1].Wins)
	require.Equal(t, c, scores[2].Username)
	require.Equal(t, 1, scores[2].Wins)
}

func TestLeaderboardRedis_WinScores_EmptyKey(t *testing.T) {
	t.Parallel()
	repo := newLeaderboardRedis(t)

	scores, err := repo.WinScores(context.Background())
	require.NoError(t, err)
	require.Empty(t, scores, "fresh leaderboard key returns no rows")
}

func TestLeaderboardRedis_NilClient_ReturnsError(t *testing.T) {
	t.Parallel()
	repo := redisrepo.NewLeaderboardRedis(nil, "leaderboard:nil")

	require.ErrorIs(t, repo.IncrementWin(context.Background(), uniq("x")), redisrepo.ErrNilLeaderboardClient)
	_, err := repo.WinScores(context.Background())
	require.ErrorIs(t, err, redisrepo.ErrNilLeaderboardClient)
}
