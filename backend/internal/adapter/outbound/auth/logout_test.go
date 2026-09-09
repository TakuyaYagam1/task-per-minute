package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth/mocks"
)

func TestAuthUseCase_Logout_ReusingRefreshTokenReturnsErrTokenRevoked(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk, _ := newMutableClock(t, now)
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	rev.EXPECT().Revoke(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	rev.EXPECT().Revoke(mock.Anything, mock.Anything, mock.Anything).Return(domain.ErrTokenRevoked).Once()

	require.NoError(t, uc.Logout(context.Background(), pair.RefreshToken))
	require.ErrorIs(t, uc.Logout(context.Background(), pair.RefreshToken), domain.ErrTokenRevoked)
}

func TestAuthUseCase_Logout_RevokesAccessToken(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk, _ := newMutableClock(t, now)
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	rev.EXPECT().Revoke(mock.Anything, mock.Anything, mock.Anything).Return(nil).Twice()
	rev.EXPECT().IsRevoked(mock.Anything, mock.Anything).Return(true, nil).Once()

	require.NoError(t, uc.Logout(context.Background(), pair.RefreshToken, pair.AccessToken))
	_, err = uc.VerifyAccess(context.Background(), pair.AccessToken)
	require.ErrorIs(t, err, domain.ErrTokenRevoked)
}

func TestAuthUseCase_Logout_ReusingRefreshTokenInsideClockSkewLeewayReturnsErrTokenRevoked(t *testing.T) {
	t.Parallel()

	issuedAt := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk, setClock := newMutableClock(t, issuedAt)
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	setClock(issuedAt.Add(refreshTTL + 5*time.Second))

	rev.EXPECT().Revoke(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	rev.EXPECT().Revoke(mock.Anything, mock.Anything, mock.Anything).Return(domain.ErrTokenRevoked).Once()

	require.NoError(t, uc.Logout(context.Background(), pair.RefreshToken))
	require.ErrorIs(t, uc.Logout(context.Background(), pair.RefreshToken), domain.ErrTokenRevoked)
}
