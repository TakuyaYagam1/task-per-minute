package auth_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
	authmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth/mocks"
)

func TestLoginVerifiesPasswordAndIssuesPair(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	clock := authmocks.NewMockClock(t)
	passwords := authmocks.NewMockPasswordVerifier(t)
	tokens := authmocks.NewMockTokenCodec(t)
	revocations := authmocks.NewMockRevocationStore(t)
	clock.EXPECT().Now().Return(now).Once()
	passwords.EXPECT().Verify("correct").Return(true).Once()
	tokens.EXPECT().Issue("admin", authusecase.TokenKindAccess, now, now.Add(time.Minute)).Return("access", nil).Once()
	tokens.EXPECT().Issue("admin", authusecase.TokenKindRefresh, now, now.Add(time.Hour)).Return("refresh", nil).Once()

	pair, err := authusecase.NewUseCase(authusecase.Config{
		AccessTTL: time.Minute, RefreshTTL: time.Hour,
	}, clock, revocations, tokens, passwords).Login(t.Context(), "correct")

	require.NoError(t, err)
	require.Equal(t, "access", pair.AccessToken)
	require.Equal(t, "refresh", pair.RefreshToken)
	require.Equal(t, now.Add(time.Minute), pair.AccessExpiresAt)
	require.Equal(t, now.Add(time.Hour), pair.RefreshExpiresAt)
}

func TestLoginRejectsInvalidPasswordBeforeIssuingTokens(t *testing.T) {
	t.Parallel()

	passwords := authmocks.NewMockPasswordVerifier(t)
	passwords.EXPECT().Verify("wrong").Return(false).Once()

	_, err := authusecase.NewUseCase(
		authusecase.Config{},
		authmocks.NewMockClock(t),
		authmocks.NewMockRevocationStore(t),
		authmocks.NewMockTokenCodec(t),
		passwords,
	).Login(t.Context(), "wrong")

	require.ErrorIs(t, err, domain.ErrInvalidCredentials)
}

func TestVerifyAccessRejectsForeignSubjectBeforeRevocationLookup(t *testing.T) {
	t.Parallel()

	tokens := authmocks.NewMockTokenCodec(t)
	tokens.EXPECT().Parse("token").Return(&authusecase.Claims{
		JTI:       "jti",
		Subject:   "foreign",
		Kind:      authusecase.TokenKindAccess,
		IssuedAt:  time.Unix(100, 0),
		ExpiresAt: time.Unix(200, 0),
	}, nil).Once()

	_, err := authusecase.NewUseCase(
		authusecase.Config{},
		authmocks.NewMockClock(t),
		authmocks.NewMockRevocationStore(t),
		tokens,
		authmocks.NewMockPasswordVerifier(t),
	).VerifyAccess(t.Context(), "token")

	require.ErrorIs(t, err, domain.ErrInvalidCredentials)
}

func TestRefreshRevokesPresentedTokenBeforeIssuingReplacement(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	claims := &authusecase.Claims{
		JTI:       "refresh-jti",
		Subject:   "admin",
		Kind:      authusecase.TokenKindRefresh,
		IssuedAt:  now.Add(-time.Hour),
		ExpiresAt: now.Add(time.Hour),
	}
	clock := authmocks.NewMockClock(t)
	tokens := authmocks.NewMockTokenCodec(t)
	revocations := authmocks.NewMockRevocationStore(t)
	tokens.EXPECT().Parse("refresh").Return(claims, nil).Once()
	revocations.EXPECT().Revoke(
		mock.Anything,
		claims.JTI,
		claims.ExpiresAt.Add(authusecase.DefaultClockSkewLeeway),
	).Return(nil).Once()
	clock.EXPECT().Now().Return(now).Once()
	tokens.EXPECT().Issue("admin", authusecase.TokenKindAccess, now, now.Add(time.Minute)).Return("new-access", nil).Once()
	tokens.EXPECT().Issue("admin", authusecase.TokenKindRefresh, now, now.Add(time.Hour)).Return("new-refresh", nil).Once()

	pair, err := authusecase.NewUseCase(authusecase.Config{
		AccessTTL: time.Minute, RefreshTTL: time.Hour,
	}, clock, revocations, tokens, authmocks.NewMockPasswordVerifier(t)).Refresh(t.Context(), "refresh")

	require.NoError(t, err)
	require.Equal(t, "new-access", pair.AccessToken)
	require.Equal(t, "new-refresh", pair.RefreshToken)
}
