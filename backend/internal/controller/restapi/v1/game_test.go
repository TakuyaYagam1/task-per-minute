package v1_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
	restv1 "github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/v1"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
)

func TestGetLeaderboardRateLimited(t *testing.T) {
	t.Parallel()

	limiter := middleware.NewLoginRateLimiter(t.Context(), 1, time.Hour, time.Hour)
	server := restv1.New(restv1.Dependencies{
		Leaderboard:        &leaderboardFake{},
		LeaderboardLimiter: limiter,
	})

	first := httptest.NewRecorder()
	firstReq := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboard", nil)
	firstReq.RemoteAddr = net.JoinHostPort("203.0.113.10", "12345")
	server.GetLeaderboard(first, firstReq)
	require.Equal(t, http.StatusOK, first.Code)

	second := httptest.NewRecorder()
	secondReq := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboard", nil)
	secondReq.RemoteAddr = net.JoinHostPort("203.0.113.10", "54321")
	server.GetLeaderboard(second, secondReq)
	require.Equal(t, http.StatusTooManyRequests, second.Code)
	require.Equal(t, "3600", second.Header().Get("Retry-After"))
}

type leaderboardFake struct{}

func (f *leaderboardFake) Top50(context.Context) ([]usecase.LeaderboardEntry, error) {
	return nil, nil
}
