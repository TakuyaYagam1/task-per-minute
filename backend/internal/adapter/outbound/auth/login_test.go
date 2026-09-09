package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth/mocks"
)

func TestAuthUseCase_Login_Success(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := newStubClock(t, now)
	rev := authmocks.NewMockRevocationStore(t)

	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)
	require.NotEmpty(t, pair.AccessToken)
	require.NotEmpty(t, pair.RefreshToken)
	require.NotEqual(t, pair.AccessToken, pair.RefreshToken,
		"access and refresh must be distinct tokens (different jti, kind)")
	require.Equal(t, now.Add(accessTTL).Unix(), pair.AccessExpiresAt.Unix())
	require.Equal(t, now.Add(refreshTTL).Unix(), pair.RefreshExpiresAt.Unix())
}

func TestAuthUseCase_Login_WrongPassword_ReturnsErrInvalidCredentials(t *testing.T) {
	t.Parallel()

	clk := newStubClock(t, time.Now().UTC())
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	_, err := uc.Login(context.Background(), "wrong")
	require.ErrorIs(t, err, domain.ErrInvalidCredentials)
}

func TestAuthUseCase_Login_PlaintextRejectsWrongPasswordWithDifferentLength(t *testing.T) {
	t.Parallel()

	clk := newStubClock(t, time.Now().UTC())
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	_, err := uc.Login(context.Background(), testPass+"-extra")
	require.ErrorIs(t, err, domain.ErrInvalidCredentials)
}

func TestAuthUseCase_Login_BcryptHash_AcceptsCorrectPassword(t *testing.T) {
	t.Parallel()

	hash, err := bcrypt.GenerateFromPassword([]byte(testPass), bcrypt.MinCost)
	require.NoError(t, err)

	cfg := newAuthCfg()
	cfg.AdminPassword = hash

	clk := newStubClock(t, time.Now().UTC())
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(cfg, clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)
	require.NotEmpty(t, pair.AccessToken)
	require.NotEmpty(t, pair.RefreshToken)
}

func TestAuthUseCase_Login_BcryptHash_RejectsWrongPassword(t *testing.T) {
	t.Parallel()

	hash, err := bcrypt.GenerateFromPassword([]byte(testPass), bcrypt.MinCost)
	require.NoError(t, err)

	cfg := newAuthCfg()
	cfg.AdminPassword = hash

	clk := newStubClock(t, time.Now().UTC())
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(cfg, clk, rev)

	_, err = uc.Login(context.Background(), "wrong-password")
	require.ErrorIs(t, err, domain.ErrInvalidCredentials)
}
