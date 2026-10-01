package leaderboard_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
	"github.com/stretchr/testify/require"
)

func TestPageNormalizesQueryAndPreservesGlobalRanks(t *testing.T) {
	t.Parallel()

	repository := &pageRepositoryStub{result: leaderboardusecase.PageRows{
		Entries: []leaderboardusecase.Entry{
			{Rank: 101, Username: "alice", Wins: 3, AverageSolveTimeMs: 42_100},
			{Rank: 103, Username: "alice2", Wins: 2, AverageSolveTimeMs: 55_000},
		},
		Total: 250,
	}}
	service := leaderboardusecase.NewPageUseCase(repository)

	got, err := service.Page(t.Context(), leaderboardusecase.PageQuery{
		Search:  "  alice ",
		Wins:    leaderboardusecase.WinsWithWins,
		Page:    2,
		PerPage: 100,
	})

	require.NoError(t, err)
	require.Equal(t, "alice", repository.query.Search)
	require.Equal(t, leaderboardusecase.WinsWithWins, repository.query.Wins)
	require.EqualValues(t, 2, repository.query.Page)
	require.EqualValues(t, 100, repository.query.PerPage)
	require.Equal(t, []leaderboardusecase.Entry{
		{Rank: 101, Username: "alice", Wins: 3, AverageSolveTimeMs: 42_100},
		{Rank: 103, Username: "alice2", Wins: 2, AverageSolveTimeMs: 55_000},
	}, got.Entries)
	require.EqualValues(t, 2, got.Page)
	require.EqualValues(t, 100, got.PerPage)
	require.EqualValues(t, 250, got.Total)
	require.EqualValues(t, 3, got.TotalPages)
}

func TestPageUsesDefaults(t *testing.T) {
	t.Parallel()

	repository := &pageRepositoryStub{result: leaderboardusecase.PageRows{Entries: []leaderboardusecase.Entry{}, Total: 0}}
	got, err := leaderboardusecase.NewPageUseCase(repository).Page(t.Context(), leaderboardusecase.PageQuery{})

	require.NoError(t, err)
	require.Equal(t, leaderboardusecase.PageQuery{
		Wins:    leaderboardusecase.WinsAll,
		Page:    1,
		PerPage: leaderboardusecase.DefaultPageSize,
	}, repository.query)
	require.Empty(t, got.Entries)
	require.Zero(t, got.TotalPages)
}

func TestPageRejectsInvalidQueriesBeforeRepositoryCall(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		query leaderboardusecase.PageQuery
	}{
		{name: "search too long", query: leaderboardusecase.PageQuery{Search: strings.Repeat("x", 51)}},
		{name: "unsupported wins filter", query: leaderboardusecase.PageQuery{Wins: "sometimes"}},
		{name: "page below one", query: leaderboardusecase.PageQuery{Page: -1}},
		{name: "page size above limit", query: leaderboardusecase.PageQuery{PerPage: leaderboardusecase.MaxPageSize + 1}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			repository := &pageRepositoryStub{}
			_, err := leaderboardusecase.NewPageUseCase(repository).Page(t.Context(), testCase.query)
			require.ErrorIs(t, err, leaderboardusecase.ErrInvalidPageQuery)
			require.Zero(t, repository.calls)
		})
	}
}

func TestPageWrapsRepositoryError(t *testing.T) {
	t.Parallel()

	repositoryError := errors.New("database unavailable")
	_, err := leaderboardusecase.NewPageUseCase(&pageRepositoryStub{err: repositoryError}).Page(
		t.Context(),
		leaderboardusecase.PageQuery{},
	)

	require.ErrorIs(t, err, repositoryError)
}

type pageRepositoryStub struct {
	query  leaderboardusecase.PageQuery
	result leaderboardusecase.PageRows
	err    error
	calls  int
}

func (r *pageRepositoryStub) LeaderboardPage(_ context.Context, query leaderboardusecase.PageQuery) (leaderboardusecase.PageRows, error) {
	r.query = query
	r.calls++
	return r.result, r.err
}
