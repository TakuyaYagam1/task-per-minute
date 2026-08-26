package player_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
	playermocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player/mocks"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestUsecase_Join_CreatesNewPlayerWithSessionToken(t *testing.T) {
	t.Parallel()

	tx, players, duels := newFixture(t)
	runTxInline(tx)

	created := &domain.Player{
		ID:        uuid.New(),
		Username:  "alice",
		Status:    domain.PlayerStatusIdle,
		CreatedAt: time.Now().UTC(),
	}
	players.EXPECT().
		JoinByUsername(mock.Anything, "alice", mock.MatchedBy(nonNilUUID), mock.MatchedBy(futureTime)).
		RunAndReturn(func(_ context.Context, _ string, token uuid.UUID, expiresAt time.Time) (*domain.Player, error) {
			updated := *created
			updated.SessionToken = &token
			updated.SessionExpiresAt = &expiresAt
			return &updated, nil
		})

	got, err := newPlayerUseCase(tx, players, duels).Join(t.Context(), "alice")
	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)
	require.NotNil(t, got.SessionToken)
	require.NotEqual(t, uuid.Nil, *got.SessionToken)
}

func TestUsecase_Join_UpdatesExistingIdlePlayerSessionToken(t *testing.T) {
	t.Parallel()

	tx, players, duels := newFixture(t)
	runTxInline(tx)

	oldToken := uuid.New()
	existing := &domain.Player{
		ID:           uuid.New(),
		Username:     "alice",
		SessionToken: &oldToken,
		Status:       domain.PlayerStatusIdle,
		CreatedAt:    time.Now().UTC(),
	}
	players.EXPECT().
		JoinByUsername(mock.Anything, "alice", mock.MatchedBy(nonNilUUID), mock.MatchedBy(futureTime)).
		RunAndReturn(func(_ context.Context, _ string, token uuid.UUID, expiresAt time.Time) (*domain.Player, error) {
			updated := *existing
			updated.SessionToken = &token
			updated.SessionExpiresAt = &expiresAt
			return &updated, nil
		})

	got, err := newPlayerUseCase(tx, players, duels).Join(t.Context(), "alice")
	require.NoError(t, err)
	require.NotNil(t, got.SessionToken)
	require.NotEqual(t, oldToken, *got.SessionToken)
}

func TestUsecase_Join_UsesInjectedClockForSessionExpiry(t *testing.T) {
	t.Parallel()

	tx, players, duels := newFixture(t)
	runTxInline(tx)

	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	ttl := 90 * time.Minute
	wantExpiresAt := now.Add(ttl)
	created := &domain.Player{
		ID:        uuid.New(),
		Username:  "alice",
		Status:    domain.PlayerStatusIdle,
		CreatedAt: now,
	}
	players.EXPECT().
		JoinByUsername(mock.Anything, "alice", mock.MatchedBy(nonNilUUID), wantExpiresAt).
		RunAndReturn(func(_ context.Context, _ string, token uuid.UUID, expiresAt time.Time) (*domain.Player, error) {
			updated := *created
			updated.SessionToken = &token
			updated.SessionExpiresAt = &expiresAt
			return &updated, nil
		})

	got, err := playerusecase.NewUseCase(
		tx,
		players,
		duels,
		fixedPlayerClock{now: now},
		playerusecase.WithSessionTTL(ttl),
	).Join(t.Context(), "alice")
	require.NoError(t, err)
	require.NotNil(t, got.SessionExpiresAt)
	require.Equal(t, wantExpiresAt, *got.SessionExpiresAt)
}

func TestUsecase_Join_RejectsPlayerInDuel(t *testing.T) {
	t.Parallel()

	tx, players, duels := newFixture(t)
	runTxInline(tx)

	players.EXPECT().
		JoinByUsername(mock.Anything, "alice", mock.MatchedBy(nonNilUUID), mock.MatchedBy(futureTime)).
		Return(nil, domain.ErrPlayerInDuel)

	_, err := newPlayerUseCase(tx, players, duels).Join(t.Context(), "alice")
	require.ErrorIs(t, err, domain.ErrPlayerInDuel)
}

func TestUsecase_Join_RejectsInvalidUsername(t *testing.T) {
	t.Parallel()

	tests := []string{"", "a", "has space", "привет", "name!", strings.Repeat("a", 51)}
	for _, username := range tests {
		t.Run(username, func(t *testing.T) {
			t.Parallel()
			tx, players, duels := newFixture(t)
			_, err := newPlayerUseCase(tx, players, duels).Join(t.Context(), username)
			require.ErrorIs(t, err, domain.ErrUsernameInvalid)
		})
	}
}

func TestUsecase_GetMe_ReturnsPlayerWithoutActiveDuel(t *testing.T) {
	t.Parallel()

	tx, players, duels := newFixture(t)
	sessionToken := uuid.New()
	player := &domain.Player{ID: uuid.New(), Username: "alice", Status: domain.PlayerStatusIdle}

	players.EXPECT().GetBySessionToken(mock.Anything, sessionToken).Return(player, nil)
	duels.EXPECT().GetActiveByPlayerID(mock.Anything, player.ID).Return(nil, nil)

	got, err := newPlayerUseCase(tx, players, duels).GetMe(t.Context(), sessionToken)
	require.NoError(t, err)
	require.Same(t, player, got.Player)
	require.Nil(t, got.ActiveDuel)
}

func TestUsecase_GetMe_ReturnsActiveDuel(t *testing.T) {
	t.Parallel()

	tx, players, duels := newFixture(t)
	sessionToken := uuid.New()
	player := &domain.Player{ID: uuid.New(), Username: "alice", Status: domain.PlayerStatusInDuel}
	activeDuel := &domain.Duel{
		ID:        uuid.New(),
		Player1ID: player.ID,
		Player2ID: uuid.New(),
		Status:    domain.DuelStatusActive,
		Deadline:  time.Now().Add(time.Minute).UTC(),
	}

	players.EXPECT().GetBySessionToken(mock.Anything, sessionToken).Return(player, nil)
	duels.EXPECT().GetActiveByPlayerID(mock.Anything, player.ID).Return(activeDuel, nil)

	got, err := newPlayerUseCase(tx, players, duels).GetMe(t.Context(), sessionToken)
	require.NoError(t, err)
	require.Same(t, activeDuel, got.ActiveDuel)
}

func TestUsecase_GetMe_InvalidSessionMapsToInvalidSession(t *testing.T) {
	t.Parallel()

	tx, players, duels := newFixture(t)
	sessionToken := uuid.New()

	players.EXPECT().GetBySessionToken(mock.Anything, sessionToken).Return(nil, domain.ErrPlayerNotFound)

	_, err := newPlayerUseCase(tx, players, duels).GetMe(t.Context(), sessionToken)
	require.ErrorIs(t, err, domain.ErrInvalidSession)
}

func TestUsecase_GetMe_RepoErrorIsWrapped(t *testing.T) {
	t.Parallel()

	tx, players, duels := newFixture(t)
	sessionToken := uuid.New()
	lowLevelErr := errors.New("db down")

	players.EXPECT().GetBySessionToken(mock.Anything, sessionToken).Return(nil, lowLevelErr)

	_, err := newPlayerUseCase(tx, players, duels).GetMe(t.Context(), sessionToken)
	require.ErrorIs(t, err, lowLevelErr)
}

func TestUsecase_Logout_ClearsSessionToken(t *testing.T) {
	t.Parallel()

	tx, players, duels := newFixture(t)
	runTxInline(tx)
	sessionToken := uuid.New()
	player := &domain.Player{ID: uuid.New(), Username: "alice", Status: domain.PlayerStatusIdle}

	players.EXPECT().GetBySessionToken(mock.Anything, sessionToken).Return(player, nil)
	players.EXPECT().UpdateSessionToken(mock.Anything, player.ID, (*uuid.UUID)(nil), (*time.Time)(nil)).Return(player, nil)

	err := newPlayerUseCase(tx, players, duels).Logout(t.Context(), sessionToken)
	require.NoError(t, err)
}

func TestUsecase_Logout_IgnoresMissingSession(t *testing.T) {
	t.Parallel()

	tx, players, duels := newFixture(t)
	runTxInline(tx)
	sessionToken := uuid.New()

	players.EXPECT().GetBySessionToken(mock.Anything, sessionToken).Return(nil, domain.ErrPlayerNotFound)

	err := newPlayerUseCase(tx, players, duels).Logout(t.Context(), sessionToken)
	require.NoError(t, err)
}

func TestUsecase_Logout_RepoErrorIsWrapped(t *testing.T) {
	t.Parallel()

	tx, players, duels := newFixture(t)
	runTxInline(tx)
	sessionToken := uuid.New()
	lowLevelErr := errors.New("db down")

	players.EXPECT().GetBySessionToken(mock.Anything, sessionToken).Return(nil, lowLevelErr)

	err := newPlayerUseCase(tx, players, duels).Logout(t.Context(), sessionToken)
	require.ErrorIs(t, err, lowLevelErr)
}

func newFixture(t *testing.T) (*playermocks.MockTransactionManager, *playermocks.MockRepository, *playermocks.MockActiveDuelReader) {
	t.Helper()
	return playermocks.NewMockTransactionManager(t), playermocks.NewMockRepository(t), playermocks.NewMockActiveDuelReader(t)
}

func newPlayerUseCase(
	tx playerusecase.TransactionManager,
	players playerusecase.Repository,
	duels playerusecase.ActiveDuelReader,
) *playerusecase.UseCase {
	return playerusecase.NewUseCase(tx, players, duels, wallPlayerClock{})
}

func runTxInline(tx *playermocks.MockTransactionManager) {
	tx.EXPECT().
		Do(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		})
}

func nonNilUUID(token uuid.UUID) bool {
	return token != uuid.Nil
}

func futureTime(t time.Time) bool {
	return t.After(time.Now().UTC())
}

type fixedPlayerClock struct {
	now time.Time
}

type wallPlayerClock struct{}

func (wallPlayerClock) Now() time.Time {
	return time.Now().UTC()
}

func (c fixedPlayerClock) Now() time.Time {
	return c.now
}
