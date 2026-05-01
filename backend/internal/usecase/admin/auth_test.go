package admin_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
	usecasemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/mocks"
	clockmocks "github.com/TakuyaYagam1/task-per-minute/pkg/clock/mocks"
)

const (
	testSecret = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	testPass   = "p@ssw0rd"
	accessTTL  = 15 * time.Minute
	refreshTTL = 168 * time.Hour
)

func newAuthCfg() admin.AuthConfig {
	return admin.AuthConfig{
		Secret:        []byte(testSecret),
		AccessTTL:     accessTTL,
		RefreshTTL:    refreshTTL,
		AdminPassword: []byte(testPass),
	}
}

// newStubClock returns a fixed-time clock. Mockery is used here to satisfy the
// mockery requirement for test doubles, but a struct fake (mutableClock below)
// is preferred when the test needs to advance time.
func newStubClock(t *testing.T, now time.Time) *clockmocks.MockClock {
	t.Helper()
	c := clockmocks.NewMockClock(t)
	c.EXPECT().Now().Return(now).Maybe()
	return c
}

// mutableClock is a tiny in-test fake for tests that advance time between
// phases (e.g. expiry verification). Lives in the test package — no production
// callers need it.
type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutableClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

func TestAuthUsecase_Login_Success(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := newStubClock(t, now)
	rev := usecasemocks.NewMockRevocationStore(t)

	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)
	require.NotEmpty(t, pair.AccessToken)
	require.NotEmpty(t, pair.RefreshToken)
	require.NotEqual(t, pair.AccessToken, pair.RefreshToken,
		"access and refresh must be distinct tokens (different jti, kind)")
	require.Equal(t, now.Add(accessTTL).Unix(), pair.AccessExpiresAt.Unix())
	require.Equal(t, now.Add(refreshTTL).Unix(), pair.RefreshExpiresAt.Unix())
}

func TestAuthUsecase_Login_WrongPassword_ReturnsErrInvalidCredentials(t *testing.T) {
	t.Parallel()

	clk := newStubClock(t, time.Now().UTC())
	rev := usecasemocks.NewMockRevocationStore(t)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	_, err := uc.Login(context.Background(), "wrong")
	require.ErrorIs(t, err, apperr.ErrInvalidCredentials)
}

func TestAuthUsecase_VerifyAccess_AfterLogin(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := newStubClock(t, now)
	rev := usecasemocks.NewMockRevocationStore(t)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	claims, err := uc.VerifyAccess(context.Background(), pair.AccessToken)
	require.NoError(t, err)
	require.Equal(t, admin.TokenKindAccess, claims.Kind)
	require.NotEmpty(t, claims.JTI)
}

func TestAuthUsecase_VerifyAccess_RejectsRefreshToken(t *testing.T) {
	t.Parallel()

	clk := newStubClock(t, time.Now().UTC())
	rev := usecasemocks.NewMockRevocationStore(t)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	_, err = uc.VerifyAccess(context.Background(), pair.RefreshToken)
	require.ErrorIs(t, err, apperr.ErrInvalidCredentials,
		"a refresh token must NOT pass VerifyAccess — kind mismatch")
}

func TestAuthUsecase_VerifyAccess_ExpiredToken_ReturnsErrTokenExpired(t *testing.T) {
	t.Parallel()

	issuedAt := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := &mutableClock{now: issuedAt}
	rev := usecasemocks.NewMockRevocationStore(t)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	clk.Set(issuedAt.Add(accessTTL + time.Minute))

	_, err = uc.VerifyAccess(context.Background(), pair.AccessToken)
	require.ErrorIs(t, err, apperr.ErrTokenExpired)
}

func TestAuthUsecase_Refresh_AfterLogin(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := newStubClock(t, now)
	rev := usecasemocks.NewMockRevocationStore(t)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	rev.EXPECT().IsRevoked(mock.Anything, mock.Anything).Return(false, nil).Once()
	rev.EXPECT().Revoke(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	rotated, err := uc.Refresh(context.Background(), pair.RefreshToken)
	require.NoError(t, err)
	require.NotEmpty(t, rotated.AccessToken)
	require.NotEmpty(t, rotated.RefreshToken)
	require.NotEqual(t, pair.RefreshToken, rotated.RefreshToken,
		"refresh rotation must mint a NEW refresh token (different jti)")
}

func TestAuthUsecase_Refresh_RevokedToken_ReturnsErrTokenRevoked(t *testing.T) {
	t.Parallel()

	clk := newStubClock(t, time.Now().UTC())
	rev := usecasemocks.NewMockRevocationStore(t)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	rev.EXPECT().IsRevoked(mock.Anything, mock.Anything).Return(true, nil).Once()

	_, err = uc.Refresh(context.Background(), pair.RefreshToken)
	require.ErrorIs(t, err, apperr.ErrTokenRevoked)
}

func TestAuthUsecase_Refresh_RejectsAccessToken(t *testing.T) {
	t.Parallel()

	clk := newStubClock(t, time.Now().UTC())
	rev := usecasemocks.NewMockRevocationStore(t)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	_, err = uc.Refresh(context.Background(), pair.AccessToken)
	require.ErrorIs(t, err, apperr.ErrInvalidCredentials,
		"access token in Refresh must be rejected by kind check")
}

func TestAuthUsecase_Refresh_RevocationStoreFailure_PropagatesError(t *testing.T) {
	t.Parallel()

	clk := newStubClock(t, time.Now().UTC())
	rev := usecasemocks.NewMockRevocationStore(t)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	storeErr := errors.New("redis: connection refused")
	rev.EXPECT().IsRevoked(mock.Anything, mock.Anything).Return(false, storeErr).Once()

	_, err = uc.Refresh(context.Background(), pair.RefreshToken)
	require.ErrorIs(t, err, storeErr)
	require.Contains(t, err.Error(), "AuthUsecase - Refresh - RevocationStore.IsRevoked")
}

func TestAuthUsecase_VerifyAccess_BadSignature_ReturnsErrInvalidCredentials(t *testing.T) {
	t.Parallel()

	clk := newStubClock(t, time.Now().UTC())
	rev := usecasemocks.NewMockRevocationStore(t)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	_, err := uc.VerifyAccess(context.Background(), "garbage.not.a.jwt")
	require.ErrorIs(t, err, apperr.ErrInvalidCredentials)
}

func TestAuthUsecase_VerifyAccess_DifferentSecret_ReturnsErrInvalidCredentials(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	clk := newStubClock(t, now)
	rev := usecasemocks.NewMockRevocationStore(t)

	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)
	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	cfg2 := newAuthCfg()
	cfg2.Secret = []byte("ffffeeeeddddccccbbbbaaaa9999888877776666555544443333222211110000")
	uc2 := admin.NewAuthUsecase(cfg2, clk, rev)

	_, err = uc2.VerifyAccess(context.Background(), pair.AccessToken)
	require.ErrorIs(t, err, apperr.ErrInvalidCredentials,
		"token signed with a different secret must be rejected")
}
