//go:build integration

package player_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
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

func TestPlayerUsecase_GetCurrentPlayer_ReturnsCurrentPlayer(t *testing.T) {
	t.Parallel()

	pool := newParallelTestDB(t)
	f := newPlayerUsecaseFixture(pool)
	ctx := context.Background()

	alice, token := createVerifiedAccountSession(ctx, t, pool, f.mgr, f.players, uniq("alice"), time.Now().UTC().Add(time.Hour))

	me, err := f.uc.GetCurrentPlayer(ctx, token)
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

	_, token := createVerifiedAccountSession(ctx, t, pool, f.mgr, f.players, uniq("alice"), time.Now().UTC().Add(time.Hour))

	err := f.uc.Logout(ctx, token)
	require.NoError(t, err)

	_, err = f.uc.GetCurrentPlayer(ctx, token)
	require.ErrorIs(t, err, domain.ErrInvalidSession)
}

func TestPlayerUsecase_RejectsLegacySessionToken(t *testing.T) {
	t.Parallel()

	pool := newParallelTestDB(t)
	f := newPlayerUsecaseFixture(pool)
	legacy, err := f.players.Create(context.Background(), uniq("legacy"))
	require.NoError(t, err)
	token := uuid.New()
	_, err = pool.Exec(context.Background(), `
		UPDATE players
		SET session_token = $2, session_expires_at = $3
		WHERE id = $1`, legacy.ID, token, time.Now().UTC().Add(time.Hour))
	require.NoError(t, err)
	_, err = f.uc.GetCurrentPlayer(context.Background(), token)
	require.ErrorIs(t, err, domain.ErrInvalidSession)
}
