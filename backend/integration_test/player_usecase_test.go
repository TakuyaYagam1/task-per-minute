//go:build integration

package integration_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
)

type playerUsecaseFixture struct {
	*duelFixture
	uc *playerusecase.PlayerUsecase
}

func newPlayerUsecaseFixture() *playerUsecaseFixture {
	f := newDuelFixture()
	return &playerUsecaseFixture{
		duelFixture: f,
		uc:          playerusecase.NewPlayerUsecase(f.mgr, f.players, f.duels),
	}
}

func TestPlayerUsecase_Join_CreateAndRepeatUpdatesSessionToken(t *testing.T) {
	t.Parallel()

	f := newPlayerUsecaseFixture()
	ctx := context.Background()
	username := uniq("alice")

	first, err := f.uc.Join(ctx, username)
	require.NoError(t, err)
	require.Equal(t, username, first.Username)
	require.Equal(t, domain.PlayerStatusIdle, first.Status)
	require.NotNil(t, first.SessionToken)
	require.NotNil(t, first.SessionExpiresAt)
	require.True(t, first.SessionExpiresAt.After(time.Now().UTC()))
	firstToken := *first.SessionToken

	second, err := f.uc.Join(ctx, username)
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.NotNil(t, second.SessionToken)
	require.NotNil(t, second.SessionExpiresAt)
	require.NotEqual(t, firstToken, *second.SessionToken)

	_, err = f.players.GetBySessionToken(ctx, firstToken)
	require.ErrorIs(t, err, apperr.ErrPlayerNotFound)

	byNewToken, err := f.players.GetBySessionToken(ctx, *second.SessionToken)
	require.NoError(t, err)
	require.Equal(t, first.ID, byNewToken.ID)
}

func TestPlayerUsecase_Join_RejoinWhileQueuedRotatesTokenAndResetsIdle(t *testing.T) {
	t.Parallel()

	f := newPlayerUsecaseFixture()
	ctx := context.Background()
	username := uniq("alice")

	first, err := f.uc.Join(ctx, username)
	require.NoError(t, err)
	require.NotNil(t, first.SessionToken)
	firstToken := *first.SessionToken

	_, err = f.players.UpdateStatus(ctx, first.ID, domain.PlayerStatusQueued)
	require.NoError(t, err)

	second, err := f.uc.Join(ctx, username)
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.NotNil(t, second.SessionToken)
	require.NotEqual(t, firstToken, *second.SessionToken)
	require.Equal(t, domain.PlayerStatusIdle, second.Status,
		"rejoining while queued must make the old queue entry stale")

	_, err = f.players.GetBySessionToken(ctx, firstToken)
	require.ErrorIs(t, err, apperr.ErrPlayerNotFound)
}

func TestPlayerUsecase_Join_ConcurrentSameUsernameUsesSingleCurrentSessionToken(t *testing.T) {
	t.Parallel()

	f := newPlayerUsecaseFixture()
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
	for _, err := range errs {
		require.NoError(t, err)
	}
	for _, result := range results {
		require.NotNil(t, result)
		require.NotNil(t, result.SessionToken)
		require.Equal(t, results[0].ID, result.ID)
		require.Equal(t, username, result.Username)
	}

	current, err := f.players.GetByUsername(ctx, username)
	require.NoError(t, err)
	require.NotNil(t, current.SessionToken)
	for _, result := range results {
		token := *result.SessionToken
		byToken, err := f.players.GetBySessionToken(ctx, token)
		if token == *current.SessionToken {
			require.NoError(t, err)
			require.Equal(t, current.ID, byToken.ID)
			continue
		}
		require.ErrorIs(t, err, apperr.ErrPlayerNotFound)
	}
}

func TestPlayerUsecase_Join_PlayerInDuelRejected(t *testing.T) {
	t.Parallel()

	f := newPlayerUsecaseFixture()
	ctx := context.Background()
	username := uniq("alice")

	joined, err := f.uc.Join(ctx, username)
	require.NoError(t, err)

	_, err = f.players.UpdateStatus(ctx, joined.ID, domain.PlayerStatusInDuel)
	require.NoError(t, err)

	_, err = f.uc.Join(ctx, username)
	require.ErrorIs(t, err, apperr.ErrPlayerInDuel)
}

func TestPlayerUsecase_GetMe_WithoutAndWithActiveDuel(t *testing.T) {
	t.Parallel()

	f := newPlayerUsecaseFixture()
	ctx := context.Background()

	alice, err := f.uc.Join(ctx, uniq("alice"))
	require.NoError(t, err)
	require.NotNil(t, alice.SessionToken)

	me, err := f.uc.GetMe(ctx, *alice.SessionToken)
	require.NoError(t, err)
	require.Equal(t, alice.ID, me.Player.ID)
	require.Nil(t, me.ActiveDuel)

	bob, err := f.players.Create(ctx, uniq("bob"))
	require.NoError(t, err)
	active, err := f.duels.Create(ctx, alice.ID, bob.ID, time.Now().Add(5*time.Minute))
	require.NoError(t, err)

	me, err = f.uc.GetMe(ctx, *alice.SessionToken)
	require.NoError(t, err)
	require.NotNil(t, me.ActiveDuel)
	require.Equal(t, active.ID, me.ActiveDuel.ID)
}

func TestPlayerUsecase_GetMe_InvalidSession(t *testing.T) {
	t.Parallel()

	f := newPlayerUsecaseFixture()
	ctx := context.Background()

	player, err := f.uc.Join(ctx, uniq("alice"))
	require.NoError(t, err)
	require.NotNil(t, player.SessionToken)

	oldToken := *player.SessionToken
	_, err = f.uc.Join(ctx, player.Username)
	require.NoError(t, err)

	_, err = f.uc.GetMe(ctx, oldToken)
	require.ErrorIs(t, err, apperr.ErrInvalidSession)
}
