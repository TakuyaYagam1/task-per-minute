package player_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
	playermocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player/mocks"
)

func TestUseCaseJoinCreatesSession(t *testing.T) {
	t.Parallel()

	tx, players := newFixture(t)
	runTxInline(tx)
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	ttl := 90 * time.Minute
	player := &domain.Player{ID: uuid.New(), Username: "alice", CreatedAt: now}
	players.EXPECT().
		JoinByUsername(mock.Anything, "alice", mock.MatchedBy(nonNilUUID), now.Add(ttl)).
		RunAndReturn(func(_ context.Context, _ string, token uuid.UUID, expiresAt time.Time) (*domain.Player, error) {
			joined := *player
			joined.SessionToken = &token
			joined.SessionExpiresAt = &expiresAt
			return &joined, nil
		})

	got, err := playerusecase.SessionNewUseCase(
		tx,
		players,
		newPlayerClock(t, now),
		playerusecase.WithSessionTTL(ttl),
	).Join(t.Context(), "alice")
	require.NoError(t, err)
	require.Equal(t, player.ID, got.ID)
	require.NotNil(t, got.SessionToken)
	require.NotEqual(t, uuid.Nil, *got.SessionToken)
	require.Equal(t, now.Add(ttl), *got.SessionExpiresAt)
}

func TestUseCaseJoinRejectsInvalidUsername(t *testing.T) {
	t.Parallel()

	for _, username := range []string{"", "a", "has space", "привет", "name!", strings.Repeat("a", 51)} {
		t.Run(username, func(t *testing.T) {
			t.Parallel()
			tx, players := newFixture(t)
			_, err := newPlayerUseCase(t, tx, players).Join(t.Context(), username)
			require.ErrorIs(t, err, domain.ErrUsernameInvalid)
		})
	}
}

func TestUseCaseJoinRejectsActiveUsernameWithoutReturningSession(t *testing.T) {
	t.Parallel()

	tx, players := newFixture(t)
	runTxInline(tx)
	players.EXPECT().
		JoinByUsername(mock.Anything, "alice", mock.MatchedBy(nonNilUUID), mock.AnythingOfType("time.Time")).
		Return(nil, domain.ErrUsernameTaken)

	joined, err := newPlayerUseCase(t, tx, players).Join(t.Context(), "alice")

	require.ErrorIs(t, err, domain.ErrUsernameTaken)
	require.Nil(t, joined)
}

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
	players.EXPECT().UpdateSessionToken(mock.Anything, player.ID, (*uuid.UUID)(nil), (*time.Time)(nil)).Return(player, nil)

	require.NoError(t, newPlayerUseCase(t, tx, players).Logout(t.Context(), token))
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

func nonNilUUID(token uuid.UUID) bool { return token != uuid.Nil }

func newPlayerClock(t *testing.T, now time.Time) *playermocks.MockSessionClock {
	t.Helper()
	clock := playermocks.NewMockSessionClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}
