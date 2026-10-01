//go:build integration

package player_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	leaderboardrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/leaderboard"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
)

func TestLeaderboardPaginationRanksAndFiltersAgainstPostgres(t *testing.T) {
	t.Parallel()

	pool := newParallelTestDB(t)
	ctx := context.Background()
	fixture := newDatabaseFixture(pool)

	for index := 0; index < 100; index++ {
		username := fmt.Sprintf("pageuser%03d", index)
		player, err := fixture.players.Create(ctx, username)
		require.NoError(t, err)

		_, err = pool.Exec(ctx, `
			INSERT INTO player_leaderboard_overrides (player_id, wins, average_solve_time_ms)
			VALUES ($1, $2, $3)`, player.ID, 100-index, int64(1_000+index))
		require.NoError(t, err)
	}

	for _, username := range []string{"zerowin_a", "zerowin_b", "zerowin_c"} {
		player, err := fixture.players.Create(ctx, username)
		require.NoError(t, err)

		_, err = pool.Exec(ctx, `
			INSERT INTO player_leaderboard_overrides (player_id, wins, average_solve_time_ms)
			VALUES ($1, 0, 0)`, player.ID)
		require.NoError(t, err)
	}

	reader := leaderboardusecase.NewPageUseCase(leaderboardrepo.NewLeaderboardPostgres(fixture.mgr))
	allPageOne, err := reader.Page(ctx, leaderboardusecase.PageQuery{
		Wins:    leaderboardusecase.WinsAll,
		Page:    1,
		PerPage: leaderboardusecase.MaxPageSize,
	})
	require.NoError(t, err)
	require.Len(t, allPageOne.Entries, 100)
	require.EqualValues(t, 103, allPageOne.Total)
	require.EqualValues(t, 2, allPageOne.TotalPages)
	require.Equal(t, "pageuser000", allPageOne.Entries[0].Username)
	require.Equal(t, 1, allPageOne.Entries[0].Rank)
	require.Equal(t, "pageuser099", allPageOne.Entries[99].Username)
	require.Equal(t, 100, allPageOne.Entries[99].Rank)

	allPageTwo, err := reader.Page(ctx, leaderboardusecase.PageQuery{
		Wins:    leaderboardusecase.WinsAll,
		Page:    2,
		PerPage: leaderboardusecase.MaxPageSize,
	})
	require.NoError(t, err)
	require.Len(t, allPageTwo.Entries, 3)
	require.EqualValues(t, 103, allPageTwo.Total)
	require.EqualValues(t, 101, allPageTwo.Entries[0].Rank)
	require.EqualValues(t, 102, allPageTwo.Entries[1].Rank)
	require.EqualValues(t, 103, allPageTwo.Entries[2].Rank)

	withoutWins, err := reader.Page(ctx, leaderboardusecase.PageQuery{
		Wins:    leaderboardusecase.WinsWithoutWins,
		Page:    1,
		PerPage: leaderboardusecase.MaxPageSize,
	})
	require.NoError(t, err)
	require.EqualValues(t, 3, withoutWins.Total)
	require.Equal(t, []string{"zerowin_a", "zerowin_b", "zerowin_c"}, leaderboardEntryUsernames(withoutWins.Entries))
	require.Equal(t, []int{101, 102, 103}, leaderboardEntryRanks(withoutWins.Entries))

	withWins, err := reader.Page(ctx, leaderboardusecase.PageQuery{
		Wins:    leaderboardusecase.WinsWithWins,
		Page:    1,
		PerPage: leaderboardusecase.MaxPageSize,
	})
	require.NoError(t, err)
	require.EqualValues(t, 100, withWins.Total)
	require.Len(t, withWins.Entries, 100)
	require.EqualValues(t, 100, withWins.Entries[99].Rank)

	underscoreSearch, err := reader.Page(ctx, leaderboardusecase.PageQuery{
		Search:  "_",
		Wins:    leaderboardusecase.WinsAll,
		Page:    1,
		PerPage: leaderboardusecase.MaxPageSize,
	})
	require.NoError(t, err)
	require.EqualValues(t, 3, underscoreSearch.Total)
	require.Equal(t, []string{"zerowin_a", "zerowin_b", "zerowin_c"}, leaderboardEntryUsernames(underscoreSearch.Entries))
	require.Equal(t, []int{101, 102, 103}, leaderboardEntryRanks(underscoreSearch.Entries))

	percentSearch, err := reader.Page(ctx, leaderboardusecase.PageQuery{
		Search:  "%",
		Wins:    leaderboardusecase.WinsAll,
		Page:    1,
		PerPage: leaderboardusecase.MaxPageSize,
	})
	require.NoError(t, err)
	require.Empty(t, percentSearch.Entries)
	require.Zero(t, percentSearch.Total)
	require.Zero(t, percentSearch.TotalPages)

	emptySearch, err := reader.Page(ctx, leaderboardusecase.PageQuery{
		Search:  "missing-player",
		Wins:    leaderboardusecase.WinsAll,
		Page:    1,
		PerPage: leaderboardusecase.MaxPageSize,
	})
	require.NoError(t, err)
	require.Empty(t, emptySearch.Entries)
	require.Zero(t, emptySearch.Total)
	require.Zero(t, emptySearch.TotalPages)

	outOfRange, err := reader.Page(ctx, leaderboardusecase.PageQuery{
		Wins:    leaderboardusecase.WinsAll,
		Page:    3,
		PerPage: leaderboardusecase.MaxPageSize,
	})
	require.NoError(t, err)
	require.Empty(t, outOfRange.Entries)
	require.EqualValues(t, 103, outOfRange.Total)
	require.EqualValues(t, 2, outOfRange.TotalPages)
}

func leaderboardEntryUsernames(entries []leaderboardusecase.Entry) []string {
	usernames := make([]string, 0, len(entries))
	for _, entry := range entries {
		usernames = append(usernames, entry.Username)
	}
	return usernames
}

func leaderboardEntryRanks(entries []leaderboardusecase.Entry) []int {
	ranks := make([]int, 0, len(entries))
	for _, entry := range entries {
		ranks = append(ranks, entry.Rank)
	}
	return ranks
}
