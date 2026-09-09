package leaderboard_test

import (
	"errors"
	"fmt"
	"testing"

	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
	leaderboardmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard/mocks"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRankingSortsByWinsThenAverageSolveTime(t *testing.T) {
	t.Parallel()

	repository := leaderboardmocks.NewMockStatsRepository(t)
	repository.On("TopStats", mock.Anything, int32(50)).Return([]leaderboardusecase.PlayerStats{
		playerStats("bob", 2, 2_000),
		playerStats("alice", 2, 1_000),
		playerStats("charlie", 1, 500),
	}, nil)

	got, err := leaderboardusecase.NewRanking(repository).Top50(t.Context())

	require.NoError(t, err)
	require.Equal(t, []leaderboardusecase.Entry{
		{Rank: 1, Username: "alice", Wins: 2, AverageSolveTimeMs: 1_000},
		{Rank: 2, Username: "bob", Wins: 2, AverageSolveTimeMs: 2_000},
		{Rank: 3, Username: "charlie", Wins: 1, AverageSolveTimeMs: 500},
	}, got)
}

func TestRankingLimitsToFifty(t *testing.T) {
	t.Parallel()

	stats := make([]leaderboardusecase.PlayerStats, 0, 55)
	for index := 0; index < 55; index++ {
		stats = append(stats, playerStats(fmt.Sprintf("player_%02d", index), 1, int64(index)))
	}

	repository := leaderboardmocks.NewMockStatsRepository(t)
	repository.On("TopStats", mock.Anything, int32(50)).Return(stats, nil)

	got, err := leaderboardusecase.NewRanking(repository).Top50(t.Context())

	require.NoError(t, err)
	require.Len(t, got, 50)
	require.Equal(t, 1, got[0].Rank)
	require.Equal(t, 50, got[49].Rank)
}

func TestRankingReturnsEmptyList(t *testing.T) {
	t.Parallel()

	repository := leaderboardmocks.NewMockStatsRepository(t)
	repository.On("TopStats", mock.Anything, int32(50)).Return([]leaderboardusecase.PlayerStats{}, nil)

	got, err := leaderboardusecase.NewRanking(repository).Top50(t.Context())

	require.NoError(t, err)
	require.Empty(t, got)
}

func TestRankingWrapsRepositoryError(t *testing.T) {
	t.Parallel()

	repositoryError := errors.New("postgres down")
	repository := leaderboardmocks.NewMockStatsRepository(t)
	repository.On("TopStats", mock.Anything, int32(50)).Return(nil, repositoryError)

	_, err := leaderboardusecase.NewRanking(repository).Top50(t.Context())

	require.ErrorIs(t, err, repositoryError)
}

func playerStats(username string, wins int, average int64) leaderboardusecase.PlayerStats {
	return leaderboardusecase.PlayerStats{
		PlayerID:           uuid.New(),
		Username:           username,
		Wins:               wins,
		AverageSolveTimeMs: average,
	}
}
