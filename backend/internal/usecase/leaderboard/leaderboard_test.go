package leaderboard_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
)

func TestUsecase_IncrementWin(t *testing.T) {
	t.Parallel()

	store := newWinStoreMock(t)
	repo := newStatsRepositoryMock(t)
	store.On("IncrementWin", mock.Anything, "alice").Return(nil)

	err := leaderboardusecase.NewUseCase(store, repo, fixedClock{
		now: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
	}).IncrementWin(t.Context(), "alice")

	require.NoError(t, err)
}

func TestUsecase_Top50_SortsByWinsThenAverageSolveTime(t *testing.T) {
	t.Parallel()

	store := newWinStoreMock(t)
	repo := newStatsRepositoryMock(t)
	repo.On("TopStats", mock.Anything, int32(50)).Return([]leaderboardusecase.PlayerStats{
		playerStats("bob", 2, 2_000),
		playerStats("alice", 2, 1_000),
		playerStats("charlie", 1, 500),
	}, nil)

	got, err := leaderboardusecase.NewUseCase(store, repo, fixedClock{
		now: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
	}).Top50(t.Context())

	require.NoError(t, err)
	require.Equal(t, []leaderboardusecase.Entry{
		{Rank: 1, Username: "alice", Wins: 2, AverageSolveTimeMs: 1_000},
		{Rank: 2, Username: "bob", Wins: 2, AverageSolveTimeMs: 2_000},
		{Rank: 3, Username: "charlie", Wins: 1, AverageSolveTimeMs: 500},
	}, got)
}

func TestUsecase_Top50_LimitsToFifty(t *testing.T) {
	t.Parallel()

	stats := make([]leaderboardusecase.PlayerStats, 0, 55)
	for i := 0; i < 55; i++ {
		username := fmt.Sprintf("player_%02d", i)
		stats = append(stats, playerStats(username, 1, int64(i)))
	}

	store := newWinStoreMock(t)
	repo := newStatsRepositoryMock(t)
	repo.On("TopStats", mock.Anything, int32(50)).Return(stats, nil)

	got, err := leaderboardusecase.NewUseCase(store, repo, fixedClock{
		now: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
	}).Top50(t.Context())

	require.NoError(t, err)
	require.Len(t, got, 50)
	require.Equal(t, 1, got[0].Rank)
	require.Equal(t, 50, got[49].Rank)
}

func TestUsecase_Top50_EmptyStats(t *testing.T) {
	t.Parallel()

	store := newWinStoreMock(t)
	repo := newStatsRepositoryMock(t)
	repo.On("TopStats", mock.Anything, int32(50)).Return([]leaderboardusecase.PlayerStats{}, nil)

	got, err := leaderboardusecase.NewUseCase(store, repo, fixedClock{}).Top50(t.Context())

	require.NoError(t, err)
	require.Empty(t, got)
}

func TestUsecase_Top50_CachesFastCalls(t *testing.T) {
	t.Parallel()

	store := newWinStoreMock(t)
	repo := newStatsRepositoryMock(t)
	repo.On("TopStats", mock.Anything, int32(50)).Return([]leaderboardusecase.PlayerStats{
		playerStats("alice", 1, 1_000),
	}, nil).Once()

	uc := leaderboardusecase.NewUseCase(store, repo, fixedClock{
		now: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
	})

	for i := 0; i < 11; i++ {
		got, err := uc.Top50(t.Context())
		require.NoError(t, err)
		require.Equal(t, "alice", got[0].Username)
	}
}

func TestUsecase_IncrementWin_InvalidatesTop50Cache(t *testing.T) {
	t.Parallel()

	store := newWinStoreMock(t)
	repo := newStatsRepositoryMock(t)
	repo.On("TopStats", mock.Anything, int32(50)).Return([]leaderboardusecase.PlayerStats{
		playerStats("alice", 1, 1_000),
	}, nil).Once()
	store.On("IncrementWin", mock.Anything, "bob").Return(nil).Once()
	repo.On("TopStats", mock.Anything, int32(50)).Return([]leaderboardusecase.PlayerStats{
		playerStats("alice", 1, 1_000),
		playerStats("bob", 1, 500),
	}, nil).Once()

	uc := leaderboardusecase.NewUseCase(store, repo, fixedClock{
		now: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
	})

	got, err := uc.Top50(t.Context())
	require.NoError(t, err)
	require.Equal(t, []leaderboardusecase.Entry{
		{Rank: 1, Username: "alice", Wins: 1, AverageSolveTimeMs: 1_000},
	}, got)

	require.NoError(t, uc.IncrementWin(t.Context(), "bob"))

	got, err = uc.Top50(t.Context())
	require.NoError(t, err)
	require.Equal(t, []leaderboardusecase.Entry{
		{Rank: 1, Username: "bob", Wins: 1, AverageSolveTimeMs: 500},
		{Rank: 2, Username: "alice", Wins: 1, AverageSolveTimeMs: 1_000},
	}, got)
}

func TestUsecase_IncrementWin_InvalidatesTop50CacheOnRedisError(t *testing.T) {
	t.Parallel()

	redisErr := errors.New("redis down")
	store := newWinStoreMock(t)
	repo := newStatsRepositoryMock(t)
	repo.On("TopStats", mock.Anything, int32(50)).Return([]leaderboardusecase.PlayerStats{
		playerStats("alice", 1, 1_000),
	}, nil).Once()
	store.On("IncrementWin", mock.Anything, "bob").Return(redisErr).Once()
	repo.On("TopStats", mock.Anything, int32(50)).Return([]leaderboardusecase.PlayerStats{
		playerStats("alice", 1, 1_000),
		playerStats("bob", 1, 500),
	}, nil).Once()

	uc := leaderboardusecase.NewUseCase(store, repo, fixedClock{
		now: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
	})

	_, err := uc.Top50(t.Context())
	require.NoError(t, err)
	require.ErrorIs(t, uc.IncrementWin(t.Context(), "bob"), redisErr)

	got, err := uc.Top50(t.Context())
	require.NoError(t, err)
	require.Equal(t, "bob", got[0].Username)
}

func TestUsecase_Top50_CacheReturnsClones(t *testing.T) {
	t.Parallel()

	store := newWinStoreMock(t)
	repo := newStatsRepositoryMock(t)
	repo.On("TopStats", mock.Anything, int32(50)).Return([]leaderboardusecase.PlayerStats{
		playerStats("alice", 1, 1_000),
	}, nil).Once()

	uc := leaderboardusecase.NewUseCase(store, repo, fixedClock{
		now: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
	})

	got, err := uc.Top50(t.Context())
	require.NoError(t, err)
	got[0].Username = "mutated"

	got, err = uc.Top50(t.Context())
	require.NoError(t, err)
	require.Equal(t, "alice", got[0].Username)
}

func TestUsecase_Top50_ReloadsAfterCacheTTL(t *testing.T) {
	t.Parallel()

	clk := &mutableClock{now: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)}
	store := newWinStoreMock(t)
	repo := newStatsRepositoryMock(t)
	repo.On("TopStats", mock.Anything, int32(50)).Return([]leaderboardusecase.PlayerStats{
		playerStats("alice", 1, 1_000),
	}, nil).Twice()

	uc := leaderboardusecase.NewUseCase(store, repo, clk)
	_, err := uc.Top50(t.Context())
	require.NoError(t, err)

	clk.now = clk.now.Add(11 * time.Second)
	_, err = uc.Top50(t.Context())
	require.NoError(t, err)
}

func TestUsecase_Top50_RepoErrorIsWrapped(t *testing.T) {
	t.Parallel()

	lowLevelErr := errors.New("postgres down")
	store := newWinStoreMock(t)
	repo := newStatsRepositoryMock(t)
	repo.On("TopStats", mock.Anything, int32(50)).Return(nil, lowLevelErr)

	_, err := leaderboardusecase.NewUseCase(store, repo, fixedClock{}).Top50(t.Context())

	require.ErrorIs(t, err, lowLevelErr)
}

func playerStats(username string, wins int, average int64) leaderboardusecase.PlayerStats {
	return leaderboardusecase.PlayerStats{
		PlayerID:           uuid.New(),
		Username:           username,
		Wins:               wins,
		AverageSolveTimeMs: average,
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

type winStoreMock struct {
	mock.Mock
}

func newWinStoreMock(t *testing.T) *winStoreMock {
	t.Helper()
	store := &winStoreMock{}
	store.Test(t)
	t.Cleanup(func() { store.AssertExpectations(t) })
	return store
}

func (m *winStoreMock) IncrementWin(ctx context.Context, username string) error {
	return m.Called(ctx, username).Error(0)
}

type statsRepositoryMock struct {
	mock.Mock
}

func newStatsRepositoryMock(t *testing.T) *statsRepositoryMock {
	t.Helper()
	repo := &statsRepositoryMock{}
	repo.Test(t)
	t.Cleanup(func() { repo.AssertExpectations(t) })
	return repo
}

func (m *statsRepositoryMock) TopStats(ctx context.Context, limit int32) ([]leaderboardusecase.PlayerStats, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]leaderboardusecase.PlayerStats), args.Error(1)
}
