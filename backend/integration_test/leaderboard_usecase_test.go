//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	redisrepo "github.com/TakuyaYagam1/task-per-minute/internal/repo/redis"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
)

func TestLeaderboardUsecase_TwoWinsRanksPlayerFirst(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	uc, f := newLeaderboardUsecaseFixture(t)
	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))
	charlie := f.makePlayer(t, uniq("charlie"))
	task1 := f.makeTask(t, uniq("t"), domain.DifficultyEasy)
	task2 := f.makeTask(t, uniq("t"), domain.DifficultyMedium)
	task3 := f.makeTask(t, uniq("t"), domain.DifficultyEasy)

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	createSolvedWin(t, alice.ID, bob.ID, alice.ID, task1.ID, now, 2*time.Second)
	createSolvedWin(t, alice.ID, charlie.ID, alice.ID, task2.ID, now.Add(time.Minute), 3*time.Second)
	createSolvedWin(t, bob.ID, charlie.ID, bob.ID, task3.ID, now.Add(2*time.Minute), 1*time.Second)

	require.NoError(t, uc.IncrementWin(ctx, alice.Username))
	require.NoError(t, uc.IncrementWin(ctx, alice.Username))
	require.NoError(t, uc.IncrementWin(ctx, bob.Username))

	entries, err := uc.Top50(ctx)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, alice.Username, entries[0].Username)
	require.Equal(t, 2, entries[0].TasksSolved)
	require.Equal(t, int64(5_000), entries[0].TotalSolveTimeMs)
	require.Equal(t, bob.Username, entries[1].Username)
	require.Equal(t, 1, entries[1].TasksSolved)
}

func TestLeaderboardUsecase_TiebreakUsesTotalSolveTime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	uc, f := newLeaderboardUsecaseFixture(t)
	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))
	task1 := f.makeTask(t, uniq("t"), domain.DifficultyEasy)
	task2 := f.makeTask(t, uniq("t"), domain.DifficultyEasy)

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	createSolvedWin(t, alice.ID, bob.ID, alice.ID, task1.ID, now, 5*time.Second)
	createSolvedWin(t, bob.ID, alice.ID, bob.ID, task2.ID, now.Add(time.Minute), 2*time.Second)

	require.NoError(t, uc.IncrementWin(ctx, alice.Username))
	require.NoError(t, uc.IncrementWin(ctx, bob.Username))

	entries, err := uc.Top50(ctx)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, bob.Username, entries[0].Username)
	require.Equal(t, int64(2_000), entries[0].TotalSolveTimeMs)
	require.Equal(t, alice.Username, entries[1].Username)
	require.Equal(t, int64(5_000), entries[1].TotalSolveTimeMs)
}

func newLeaderboardUsecaseFixture(t *testing.T) (*leaderboardusecase.LeaderboardUsecase, *duelFixture) {
	t.Helper()
	f := newDuelFixture()
	store := redisrepo.NewLeaderboardRedis(sharedRedis(t).client, "leaderboard:"+uniq("z"))
	uc := leaderboardusecase.NewLeaderboardUsecase(store, f.board, fixedIntegrationClock{
		now: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
	})
	return uc, f
}
