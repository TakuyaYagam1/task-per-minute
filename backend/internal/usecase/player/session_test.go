package player_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
	playermocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player/mocks"
)

func TestUseCaseGetCurrentPlayerReturnsSessionPlayer(t *testing.T) {
	t.Parallel()

	tx, players := newFixture(t)
	token := uuid.New()
	want := &domain.Player{ID: uuid.New(), Username: "alice"}
	players.EXPECT().GetBySessionToken(mock.Anything, token).Return(want, nil)

	got, err := newPlayerUseCase(t, tx, players).GetCurrentPlayer(t.Context(), token)
	require.NoError(t, err)
	require.Same(t, want, got)
}

func TestUseCaseGetCurrentPlayerMapsMissingPlayerToInvalidSession(t *testing.T) {
	t.Parallel()

	tx, players := newFixture(t)
	token := uuid.New()
	players.EXPECT().GetBySessionToken(mock.Anything, token).Return(nil, domain.ErrPlayerNotFound)

	_, err := newPlayerUseCase(t, tx, players).GetCurrentPlayer(t.Context(), token)
	require.ErrorIs(t, err, domain.ErrInvalidSession)
}

func TestUseCaseGetCurrentPlayerWrapsRepositoryError(t *testing.T) {
	t.Parallel()

	tx, players := newFixture(t)
	token := uuid.New()
	lowLevelErr := errors.New("db down")
	players.EXPECT().GetBySessionToken(mock.Anything, token).Return(nil, lowLevelErr)

	_, err := newPlayerUseCase(t, tx, players).GetCurrentPlayer(t.Context(), token)
	require.ErrorIs(t, err, lowLevelErr)
}

func TestUseCaseLogoutClearsSession(t *testing.T) {
	t.Parallel()

	tx, players := newFixture(t)
	runTxInline(tx)
	token := uuid.New()
	player := &domain.Player{ID: uuid.New(), Username: "alice"}
	players.EXPECT().GetBySessionToken(mock.Anything, token).Return(player, nil)
	players.EXPECT().UpdateSessionToken(mock.Anything, player.ID, token, (*uuid.UUID)(nil), (*time.Time)(nil)).Return(player, nil)

	require.NoError(t, newPlayerUseCase(t, tx, players).Logout(t.Context(), token))
}

func TestUseCaseLogoutDoesNotClearReplacementSession(t *testing.T) {
	t.Parallel()

	tx, players := newFixture(t)
	runTxInline(tx)
	oldToken := uuid.New()
	player := &domain.Player{ID: uuid.New(), Username: "alice", SessionToken: &oldToken}
	players.EXPECT().GetBySessionToken(mock.Anything, oldToken).Return(player, nil)
	players.EXPECT().
		UpdateSessionToken(mock.Anything, player.ID, oldToken, (*uuid.UUID)(nil), (*time.Time)(nil)).
		Return(nil, domain.ErrPlayerNotFound)

	require.NoError(t, newPlayerUseCase(t, tx, players).Logout(t.Context(), oldToken))
}

func TestUseCaseLogoutIgnoresMissingSession(t *testing.T) {
	t.Parallel()

	tx, players := newFixture(t)
	runTxInline(tx)
	token := uuid.New()
	players.EXPECT().GetBySessionToken(mock.Anything, token).Return(nil, domain.ErrPlayerNotFound)

	require.NoError(t, newPlayerUseCase(t, tx, players).Logout(t.Context(), token))
}

func newFixture(t *testing.T) (*playermocks.MockSessionTransactionManager, *playermocks.MockRepository) {
	t.Helper()
	return playermocks.NewMockSessionTransactionManager(t), playermocks.NewMockRepository(t)
}

func newPlayerUseCase(
	t *testing.T,
	tx playerusecase.SessionTransactionManager,
	players playerusecase.Repository,
) *playerusecase.SessionUseCase {
	t.Helper()
	clock := playermocks.NewMockSessionClock(t)
	clock.EXPECT().Now().RunAndReturn(func() time.Time { return time.Now().UTC() }).Maybe()
	return playerusecase.SessionNewUseCase(tx, players, clock)
}

func runTxInline(tx *playermocks.MockSessionTransactionManager) {
	tx.EXPECT().
		Do(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		})
}
