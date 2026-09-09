package v1_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	middlewaremocks "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware/mocks"
	restv1 "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
	leaderboardmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard/mocks"
)

func TestGetLeaderboardRateLimited(t *testing.T) {
	t.Parallel()

	limiter := middlewaremocks.NewMockRateLimiter(t)
	limiter.EXPECT().Allow(mock.Anything).Return(true).Once()
	limiter.EXPECT().Allow(mock.Anything).Return(false).Once()
	limiter.EXPECT().RetryAfter().Return("3600").Once()
	repo := leaderboardmocks.NewMockStatsRepository(t)
	clock := leaderboardmocks.NewMockClock(t)
	clock.EXPECT().Now().Return(time.Now()).Twice()
	repo.EXPECT().TopStats(mock.Anything, int32(50)).Return([]leaderboardusecase.PlayerStats{}, nil).Once()
	ranking := leaderboardusecase.NewRanking(repo)
	server := restv1.New(restv1.Dependencies{
		Leaderboard:        leaderboardusecase.NewCache(ranking, clock),
		LeaderboardLimiter: limiter,
	})

	first := httptest.NewRecorder()
	firstRequest := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboard", nil)
	firstRequest.RemoteAddr = net.JoinHostPort("203.0.113.10", "12345")
	server.GetLeaderboard(first, firstRequest)
	require.Equal(t, http.StatusOK, first.Code)

	second := httptest.NewRecorder()
	secondRequest := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboard", nil)
	secondRequest.RemoteAddr = net.JoinHostPort("203.0.113.10", "54321")
	server.GetLeaderboard(second, secondRequest)
	require.Equal(t, http.StatusTooManyRequests, second.Code)
	require.Equal(t, "3600", second.Header().Get("Retry-After"))
}
