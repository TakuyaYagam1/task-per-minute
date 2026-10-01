package v1_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
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
	ranking := leaderboardusecase.NewRanking(repo)
	pageRepository := &leaderboardPageRepositoryStub{rows: leaderboardusecase.PageRows{Entries: []leaderboardusecase.Entry{}, Total: 0}}
	pages := leaderboardusecase.NewPageUseCase(pageRepository)
	server := restv1.New(restv1.Dependencies{
		Leaderboard:        leaderboardusecase.NewCache(ranking, clock, pages),
		LeaderboardLimiter: limiter,
	})

	first := httptest.NewRecorder()
	firstRequest := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboard", nil)
	firstRequest.RemoteAddr = net.JoinHostPort("203.0.113.10", "12345")
	server.GetLeaderboard(first, firstRequest, api.GetLeaderboardParams{})
	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, leaderboardusecase.PageQuery{
		Wins:    leaderboardusecase.WinsAll,
		Page:    1,
		PerPage: 100,
	}, pageRepository.query)
	var response api.LeaderboardResponse
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &response))
	require.EqualValues(t, 1, response.Page)
	require.EqualValues(t, 100, response.PerPage)
	require.Zero(t, response.Total)
	require.Zero(t, response.TotalPages)

	second := httptest.NewRecorder()
	secondRequest := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboard", nil)
	secondRequest.RemoteAddr = net.JoinHostPort("203.0.113.10", "54321")
	server.GetLeaderboard(second, secondRequest, api.GetLeaderboardParams{})
	require.Equal(t, http.StatusTooManyRequests, second.Code)
	require.Equal(t, "3600", second.Header().Get("Retry-After"))
}

func TestGetLeaderboardMapsQueryAndRejectsInvalidFiltersAndPaging(t *testing.T) {
	t.Parallel()

	limiter := middlewaremocks.NewMockRateLimiter(t)
	limiter.EXPECT().Allow(mock.Anything).Return(true).Times(5)
	repo := leaderboardmocks.NewMockStatsRepository(t)
	clock := leaderboardmocks.NewMockClock(t)
	pageRepository := &leaderboardPageRepositoryStub{rows: leaderboardusecase.PageRows{
		Entries: []leaderboardusecase.Entry{{Rank: 102, Username: "alice", Wins: 3, AverageSolveTimeMs: 42_100}},
		Total:   101,
	}}
	pages := leaderboardusecase.NewPageUseCase(pageRepository)
	server := restv1.New(restv1.Dependencies{
		Leaderboard:        leaderboardusecase.NewCache(leaderboardusecase.NewRanking(repo), clock, pages),
		LeaderboardLimiter: limiter,
	})
	wins := api.LeaderboardWinsFilter("withwins")
	search := " alice "
	requestedPage := int32(2)
	perPage := int32(100)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboard", nil)
	response := httptest.NewRecorder()
	server.GetLeaderboard(response, request, api.GetLeaderboardParams{
		Search:  &search,
		Wins:    &wins,
		Page:    &requestedPage,
		PerPage: &perPage,
	})
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, leaderboardusecase.PageQuery{
		Search:  "alice",
		Wins:    leaderboardusecase.WinsWithWins,
		Page:    2,
		PerPage: 100,
	}, pageRepository.query)
	var body api.LeaderboardResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.EqualValues(t, 102, body.Entries[0].Rank)
	require.EqualValues(t, 2, body.Page)
	require.EqualValues(t, 100, body.PerPage)
	require.EqualValues(t, 101, body.Total)
	require.EqualValues(t, 2, body.TotalPages)

	invalidPage := int32(0)
	invalidPerPage := int32(0)
	tooManyPerPage := int32(101)
	invalidWins := api.LeaderboardWinsFilter("sometimes")
	invalidQueries := []api.GetLeaderboardParams{
		{Page: &invalidPage},
		{PerPage: &invalidPerPage},
		{PerPage: &tooManyPerPage},
		{Wins: &invalidWins},
	}
	for _, params := range invalidQueries {
		badRequest := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboard", nil)
		badResponse := httptest.NewRecorder()
		server.GetLeaderboard(badResponse, badRequest, params)
		require.Equal(t, http.StatusBadRequest, badResponse.Code)
	}
	require.Equal(t, 1, pageRepository.calls)
}

type leaderboardPageRepositoryStub struct {
	query leaderboardusecase.PageQuery
	rows  leaderboardusecase.PageRows
	calls int
}

func (r *leaderboardPageRepositoryStub) LeaderboardPage(_ context.Context, query leaderboardusecase.PageQuery) (leaderboardusecase.PageRows, error) {
	r.query = query
	r.calls++
	return r.rows, nil
}
