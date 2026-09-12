//go:build integration

package integration_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
)

type playerUsecaseFixture struct {
	*databaseFixture

	uc *playerusecase.SessionUseCase
}

func newPlayerUsecaseFixture(pools ...*pgxpool.Pool) *playerUsecaseFixture {
	f := newDatabaseFixture(pools...)
	return &playerUsecaseFixture{
		databaseFixture: f,
		uc:              playerusecase.SessionNewUseCase(f.mgr, f.players, realIntegrationClock()),
	}
}

func TestPlayerUsecase_Join_CreateAndRejectActiveSession(t *testing.T) {
	t.Parallel()

	pool := newParallelTestDB(t)
	f := newPlayerUsecaseFixture(pool)
	ctx := context.Background()
	username := uniq("alice")

	first, err := f.uc.Join(ctx, username)
	require.NoError(t, err)
	require.Equal(t, username, first.Username)
	require.NotNil(t, first.SessionToken)
	require.NotNil(t, first.SessionExpiresAt)
	require.True(t, first.SessionExpiresAt.After(time.Now().UTC()))
	firstToken := *first.SessionToken

	second, err := f.uc.Join(ctx, username)
	require.ErrorIs(t, err, domain.ErrUsernameTaken)
	require.Nil(t, second)

	retained, err := f.players.GetBySessionToken(ctx, firstToken)
	require.NoError(t, err)
	require.Equal(t, first.ID, retained.ID)
}

func TestPlayerUsecase_Join_ConcurrentSameUsernameUsesSingleCurrentSessionToken(t *testing.T) {
	t.Parallel()

	pool := newParallelTestDB(t)
	f := newPlayerUsecaseFixture(pool)
	ctx := context.Background()
	username := uniq("alice")

	const joins = 2
	results := make([]*domain.Player, joins)
	errs := make([]error, joins)
	var wg sync.WaitGroup
	wg.Add(joins)
	for i := range joins {
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = f.uc.Join(context.Background(), username)
		}(i)
	}
	wg.Wait()
	var succeeded int
	for index, err := range errs {
		if err == nil {
			succeeded++
			require.NotNil(t, results[index])
			require.NotNil(t, results[index].SessionToken)
			require.Equal(t, username, results[index].Username)
			continue
		}
		require.ErrorIs(t, err, domain.ErrUsernameTaken)
		require.Nil(t, results[index])
	}
	require.Equal(t, 1, succeeded)

	current, err := f.players.GetByUsername(ctx, username)
	require.NoError(t, err)
	require.NotNil(t, current.SessionToken)
	byToken, err := f.players.GetBySessionToken(ctx, *current.SessionToken)
	require.NoError(t, err)
	require.Equal(t, current.ID, byToken.ID)
}

func TestPlayerUsecase_GetCurrentPlayer_ReturnsCurrentPlayer(t *testing.T) {
	t.Parallel()

	pool := newParallelTestDB(t)
	f := newPlayerUsecaseFixture(pool)
	ctx := context.Background()

	alice, err := f.uc.Join(ctx, uniq("alice"))
	require.NoError(t, err)
	require.NotNil(t, alice.SessionToken)

	me, err := f.uc.GetCurrentPlayer(ctx, *alice.SessionToken)
	require.NoError(t, err)
	require.Equal(t, alice.ID, me.ID)
	require.Equal(t, alice.Username, me.Username)
	require.Equal(t, alice.SessionToken, me.SessionToken)
}

func TestPlayerUsecase_GetCurrentPlayer_InvalidSession(t *testing.T) {
	t.Parallel()

	pool := newParallelTestDB(t)
	f := newPlayerUsecaseFixture(pool)
	ctx := context.Background()

	player, err := f.uc.Join(ctx, uniq("alice"))
	require.NoError(t, err)
	require.NotNil(t, player.SessionToken)

	oldToken := *player.SessionToken
	err = f.uc.Logout(ctx, oldToken)
	require.NoError(t, err)

	_, err = f.uc.GetCurrentPlayer(ctx, oldToken)
	require.ErrorIs(t, err, domain.ErrInvalidSession)
}
