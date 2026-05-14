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

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecasemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/mocks"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
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

	got, err := playerusecase.NewPlayerUsecase(tx, players, duels).Join(t.Context(), "alice")
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

	got, err := playerusecase.NewPlayerUsecase(tx, players, duels).Join(t.Context(), "alice")
	require.NoError(t, err)
	require.NotNil(t, got.SessionToken)
	require.NotEqual(t, oldToken, *got.SessionToken)
}

func TestUsecase_Join_RejectsPlayerInDuel(t *testing.T) {
	t.Parallel()

	tx, players, duels := newFixture(t)
	runTxInline(tx)

	players.EXPECT().
		JoinByUsername(mock.Anything, "alice", mock.MatchedBy(nonNilUUID), mock.MatchedBy(futureTime)).
		Return(nil, apperr.ErrPlayerInDuel)

	_, err := playerusecase.NewPlayerUsecase(tx, players, duels).Join(t.Context(), "alice")
	require.ErrorIs(t, err, apperr.ErrPlayerInDuel)
}

func TestUsecase_Join_RejectsInvalidUsername(t *testing.T) {
	t.Parallel()

	tests := []string{"", "a", "has space", "привет", "name!", strings.Repeat("a", 51)}
	for _, username := range tests {
		t.Run(username, func(t *testing.T) {
			t.Parallel()
			tx, players, duels := newFixture(t)
			_, err := playerusecase.NewPlayerUsecase(tx, players, duels).Join(t.Context(), username)
			require.ErrorIs(t, err, apperr.ErrUsernameInvalid)
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

	got, err := playerusecase.NewPlayerUsecase(tx, players, duels).GetMe(t.Context(), sessionToken)
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

	got, err := playerusecase.NewPlayerUsecase(tx, players, duels).GetMe(t.Context(), sessionToken)
	require.NoError(t, err)
	require.Same(t, activeDuel, got.ActiveDuel)
}

func TestUsecase_GetMe_InvalidSessionMapsToInvalidSession(t *testing.T) {
	t.Parallel()

	tx, players, duels := newFixture(t)
	sessionToken := uuid.New()

	players.EXPECT().GetBySessionToken(mock.Anything, sessionToken).Return(nil, apperr.ErrPlayerNotFound)

	_, err := playerusecase.NewPlayerUsecase(tx, players, duels).GetMe(t.Context(), sessionToken)
	require.ErrorIs(t, err, apperr.ErrInvalidSession)
}

func TestUsecase_GetMe_RepoErrorIsWrapped(t *testing.T) {
	t.Parallel()

	tx, players, duels := newFixture(t)
	sessionToken := uuid.New()
	lowLevelErr := errors.New("db down")

	players.EXPECT().GetBySessionToken(mock.Anything, sessionToken).Return(nil, lowLevelErr)

	_, err := playerusecase.NewPlayerUsecase(tx, players, duels).GetMe(t.Context(), sessionToken)
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

	err := playerusecase.NewPlayerUsecase(tx, players, duels).Logout(t.Context(), sessionToken)
	require.NoError(t, err)
}

func TestUsecase_Logout_IgnoresMissingSession(t *testing.T) {
	t.Parallel()

	tx, players, duels := newFixture(t)
	runTxInline(tx)
	sessionToken := uuid.New()

	players.EXPECT().GetBySessionToken(mock.Anything, sessionToken).Return(nil, apperr.ErrPlayerNotFound)

	err := playerusecase.NewPlayerUsecase(tx, players, duels).Logout(t.Context(), sessionToken)
	require.NoError(t, err)
}

func TestUsecase_Logout_RepoErrorIsWrapped(t *testing.T) {
	t.Parallel()

	tx, players, duels := newFixture(t)
	runTxInline(tx)
	sessionToken := uuid.New()
	lowLevelErr := errors.New("db down")

	players.EXPECT().GetBySessionToken(mock.Anything, sessionToken).Return(nil, lowLevelErr)

	err := playerusecase.NewPlayerUsecase(tx, players, duels).Logout(t.Context(), sessionToken)
	require.ErrorIs(t, err, lowLevelErr)
}

func newFixture(t *testing.T) (*usecasemocks.MockTxManager, *usecasemocks.MockPlayerRepo, *usecasemocks.MockDuelRepo) {
	t.Helper()
	return usecasemocks.NewMockTxManager(t), usecasemocks.NewMockPlayerRepo(t), usecasemocks.NewMockDuelRepo(t)
}

func runTxInline(tx *usecasemocks.MockTxManager) {
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
