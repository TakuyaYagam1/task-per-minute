package leaderboard_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
	usecasemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/mocks"
)

func TestUsecase_IncrementWin(t *testing.T) {
	t.Parallel()

	store := usecasemocks.NewMockLeaderboardStore(t)
	repo := usecasemocks.NewMockLeaderboardRepo(t)
	store.EXPECT().IncrementWin(mock.Anything, "alice").Return(nil)

	err := leaderboardusecase.NewLeaderboardUsecase(store, repo, fixedClock{
		now: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
	}).IncrementWin(t.Context(), "alice")

	require.NoError(t, err)
}

func TestUsecase_Top50_SortsByWinsThenSolveTime(t *testing.T) {
	t.Parallel()

	store := usecasemocks.NewMockLeaderboardStore(t)
	repo := usecasemocks.NewMockLeaderboardRepo(t)
	store.EXPECT().WinScores(mock.Anything).Return([]usecase.LeaderboardScore{
		{Username: "bob", TasksSolved: 2},
		{Username: "alice", TasksSolved: 2},
		{Username: "charlie", TasksSolved: 1},
	}, nil)
	repo.EXPECT().TotalSolveTimeForPlayers(mock.Anything, []string{"bob", "alice", "charlie"}).Return([]usecase.LeaderboardPlayerTime{
		playerTime("alice", 1_000),
		playerTime("bob", 2_000),
		playerTime("charlie", 500),
	}, nil)

	got, err := leaderboardusecase.NewLeaderboardUsecase(store, repo, fixedClock{
		now: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
	}).Top50(t.Context())

	require.NoError(t, err)
	require.Equal(t, []leaderboardusecase.Entry{
		{Rank: 1, Username: "alice", TasksSolved: 2, TotalSolveTimeMs: 1_000},
		{Rank: 2, Username: "bob", TasksSolved: 2, TotalSolveTimeMs: 2_000},
		{Rank: 3, Username: "charlie", TasksSolved: 1, TotalSolveTimeMs: 500},
	}, got)
}

func TestUsecase_Top50_LimitsToFifty(t *testing.T) {
	t.Parallel()

	scores := make([]usecase.LeaderboardScore, 0, 55)
	times := make([]usecase.LeaderboardPlayerTime, 0, 55)
	for i := 0; i < 55; i++ {
		username := fmt.Sprintf("player_%02d", i)
		scores = append(scores, usecase.LeaderboardScore{Username: username, TasksSolved: 1})
		times = append(times, playerTime(username, int64(i)))
	}

	store := usecasemocks.NewMockLeaderboardStore(t)
	repo := usecasemocks.NewMockLeaderboardRepo(t)
	store.EXPECT().WinScores(mock.Anything).Return(scores, nil)
	repo.EXPECT().TotalSolveTimeForPlayers(mock.Anything, mock.Anything).Return(times, nil)

	got, err := leaderboardusecase.NewLeaderboardUsecase(store, repo, fixedClock{
		now: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
	}).Top50(t.Context())

	require.NoError(t, err)
	require.Len(t, got, 50)
	require.Equal(t, 1, got[0].Rank)
	require.Equal(t, 50, got[49].Rank)
}

func TestUsecase_Top50_CachesFastCalls(t *testing.T) {
	t.Parallel()

	store := usecasemocks.NewMockLeaderboardStore(t)
	repo := usecasemocks.NewMockLeaderboardRepo(t)
	store.EXPECT().WinScores(mock.Anything).Return([]usecase.LeaderboardScore{
		{Username: "alice", TasksSolved: 1},
	}, nil).Once()
	repo.EXPECT().TotalSolveTimeForPlayers(mock.Anything, []string{"alice"}).Return([]usecase.LeaderboardPlayerTime{
		playerTime("alice", 1_000),
	}, nil).Once()

	uc := leaderboardusecase.NewLeaderboardUsecase(store, repo, fixedClock{
		now: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
	})

	for i := 0; i < 11; i++ {
		got, err := uc.Top50(t.Context())
		require.NoError(t, err)
		require.Equal(t, "alice", got[0].Username)
	}
}

func TestUsecase_Top50_ReloadsAfterCacheTTL(t *testing.T) {
	t.Parallel()

	clk := &mutableClock{now: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)}
	store := usecasemocks.NewMockLeaderboardStore(t)
	repo := usecasemocks.NewMockLeaderboardRepo(t)
	store.EXPECT().WinScores(mock.Anything).Return([]usecase.LeaderboardScore{
		{Username: "alice", TasksSolved: 1},
	}, nil).Twice()
	repo.EXPECT().TotalSolveTimeForPlayers(mock.Anything, []string{"alice"}).Return([]usecase.LeaderboardPlayerTime{
		playerTime("alice", 1_000),
	}, nil).Twice()

	uc := leaderboardusecase.NewLeaderboardUsecase(store, repo, clk)
	_, err := uc.Top50(t.Context())
	require.NoError(t, err)

	clk.now = clk.now.Add(11 * time.Second)
	_, err = uc.Top50(t.Context())
	require.NoError(t, err)
}

func TestUsecase_Top50_StoreErrorIsWrapped(t *testing.T) {
	t.Parallel()

	lowLevelErr := errors.New("redis down")
	store := usecasemocks.NewMockLeaderboardStore(t)
	repo := usecasemocks.NewMockLeaderboardRepo(t)
	store.EXPECT().WinScores(mock.Anything).Return(nil, lowLevelErr)

	_, err := leaderboardusecase.NewLeaderboardUsecase(store, repo, fixedClock{}).Top50(t.Context())

	require.ErrorIs(t, err, lowLevelErr)
}

func playerTime(username string, total int64) usecase.LeaderboardPlayerTime {
	return usecase.LeaderboardPlayerTime{
		PlayerID:         uuid.New(),
		Username:         username,
		TotalSolveTimeMs: total,
	}
}

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}

type mutableClock struct {
	now time.Time
}

func (c *mutableClock) Now() time.Time {
	return c.now
}
