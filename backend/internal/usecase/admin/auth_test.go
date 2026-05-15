package admin_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/repo/inmem"
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
// phases (e.g. expiry verification). Lives in the test package - no production
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

// signWithKind mints a HS256-signed JWT with an arbitrary `kind` claim. It
// lets the suite assert that the parser rejects values outside the
// {access,refresh} enum even when the signature itself is valid.
func signWithKind(t *testing.T, secret []byte, kind string, iat, exp time.Time) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss":  "task-per-minute-backend",
		"aud":  "task-per-minute-admin",
		"sub":  "admin",
		"kind": kind,
		"iat":  iat.Unix(),
		"exp":  exp.Unix(),
		"jti":  uuid.NewString(),
	})
	signed, err := tok.SignedString(secret)
	require.NoError(t, err)
	return signed
}

func signWithSubject(t *testing.T, secret []byte, sub string, kind admin.TokenKind, iat, exp time.Time) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss":  "task-per-minute-backend",
		"aud":  "task-per-minute-admin",
		"sub":  sub,
		"kind": string(kind),
		"iat":  iat.Unix(),
		"exp":  exp.Unix(),
		"jti":  uuid.NewString(),
	})
	signed, err := tok.SignedString(secret)
	require.NoError(t, err)
	return signed
}

func signWithClaims(t *testing.T, secret []byte, method jwt.SigningMethod, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(method, claims)
	signed, err := tok.SignedString(secret)
	require.NoError(t, err)
	return signed
}

func validAdminClaims(now time.Time) jwt.MapClaims {
	return jwt.MapClaims{
		"iss":  "task-per-minute-backend",
		"aud":  "task-per-minute-admin",
		"sub":  "admin",
		"kind": string(admin.TokenKindAccess),
		"iat":  now.Unix(),
		"exp":  now.Add(accessTTL).Unix(),
		"jti":  uuid.NewString(),
	}
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

func TestAuthUsecase_Login_PlaintextRejectsWrongPasswordWithDifferentLength(t *testing.T) {
	t.Parallel()

	clk := newStubClock(t, time.Now().UTC())
	rev := usecasemocks.NewMockRevocationStore(t)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	_, err := uc.Login(context.Background(), testPass+"-extra")
	require.ErrorIs(t, err, apperr.ErrInvalidCredentials)
}

func TestAuthUsecase_Login_BcryptHash_AcceptsCorrectPassword(t *testing.T) {
	t.Parallel()

	hash, err := bcrypt.GenerateFromPassword([]byte(testPass), bcrypt.MinCost)
	require.NoError(t, err)

	cfg := newAuthCfg()
	cfg.AdminPassword = hash

	clk := newStubClock(t, time.Now().UTC())
	rev := usecasemocks.NewMockRevocationStore(t)
	uc := admin.NewAuthUsecase(cfg, clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)
	require.NotEmpty(t, pair.AccessToken)
	require.NotEmpty(t, pair.RefreshToken)
}

func TestAuthUsecase_Login_BcryptHash_RejectsWrongPassword(t *testing.T) {
	t.Parallel()

	hash, err := bcrypt.GenerateFromPassword([]byte(testPass), bcrypt.MinCost)
	require.NoError(t, err)

	cfg := newAuthCfg()
	cfg.AdminPassword = hash

	clk := newStubClock(t, time.Now().UTC())
	rev := usecasemocks.NewMockRevocationStore(t)
	uc := admin.NewAuthUsecase(cfg, clk, rev)

	_, err = uc.Login(context.Background(), "wrong-password")
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

	rev.EXPECT().IsRevoked(mock.Anything, mock.Anything).Return(false, nil).Once()

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
		"a refresh token must NOT pass VerifyAccess - kind mismatch")
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

	rev.EXPECT().Revoke(mock.Anything, mock.Anything, mock.Anything).Return(apperr.ErrTokenRevoked).Once()

	_, err = uc.Refresh(context.Background(), pair.RefreshToken)
	require.ErrorIs(t, err, apperr.ErrTokenRevoked)
}

func TestAuthUsecase_Refresh_ReusingOldRefreshTokenReturnsErrTokenRevoked(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := &mutableClock{now: now}
	rev := inmem.NewRevocation(clk)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	rotated, err := uc.Refresh(context.Background(), pair.RefreshToken)
	require.NoError(t, err)
	require.NotEmpty(t, rotated.RefreshToken)

	_, err = uc.Refresh(context.Background(), pair.RefreshToken)
	require.ErrorIs(t, err, apperr.ErrTokenRevoked)
}

func TestAuthUsecase_Refresh_ReusingOldRefreshTokenInsideClockSkewLeewayReturnsErrTokenRevoked(t *testing.T) {
	t.Parallel()

	issuedAt := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := &mutableClock{now: issuedAt}
	rev := inmem.NewRevocation(clk)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	clk.Set(issuedAt.Add(refreshTTL + 5*time.Second))

	rotated, err := uc.Refresh(context.Background(), pair.RefreshToken)
	require.NoError(t, err)
	require.NotEmpty(t, rotated.RefreshToken)

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
	rev.EXPECT().Revoke(mock.Anything, mock.Anything, mock.Anything).Return(storeErr).Once()

	_, err = uc.Refresh(context.Background(), pair.RefreshToken)
	require.ErrorIs(t, err, storeErr)
	require.Contains(t, err.Error(), "AuthUsecase - Refresh - RevocationStore.Revoke")
}

func TestAuthUsecase_Logout_ReusingRefreshTokenReturnsErrTokenRevoked(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := &mutableClock{now: now}
	rev := inmem.NewRevocation(clk)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	require.NoError(t, uc.Logout(context.Background(), pair.RefreshToken))
	require.ErrorIs(t, uc.Logout(context.Background(), pair.RefreshToken), apperr.ErrTokenRevoked)
}

func TestAuthUsecase_Logout_RevokesAccessToken(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := &mutableClock{now: now}
	rev := inmem.NewRevocation(clk)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	require.NoError(t, uc.Logout(context.Background(), pair.RefreshToken, pair.AccessToken))
	_, err = uc.VerifyAccess(context.Background(), pair.AccessToken)
	require.ErrorIs(t, err, apperr.ErrTokenRevoked)
}

func TestAuthUsecase_Logout_ReusingRefreshTokenInsideClockSkewLeewayReturnsErrTokenRevoked(t *testing.T) {
	t.Parallel()

	issuedAt := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := &mutableClock{now: issuedAt}
	rev := inmem.NewRevocation(clk)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	clk.Set(issuedAt.Add(refreshTTL + 5*time.Second))

	require.NoError(t, uc.Logout(context.Background(), pair.RefreshToken))
	require.ErrorIs(t, uc.Logout(context.Background(), pair.RefreshToken), apperr.ErrTokenRevoked)
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

func TestAuthUsecase_VerifyAccess_ClockSkewWithinLeeway_Accepted(t *testing.T) {
	t.Parallel()

	issuedAt := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := &mutableClock{now: issuedAt}
	rev := usecasemocks.NewMockRevocationStore(t)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	clk.Set(issuedAt.Add(accessTTL + 5*time.Second))

	rev.EXPECT().IsRevoked(mock.Anything, mock.Anything).Return(false, nil).Once()

	claims, err := uc.VerifyAccess(context.Background(), pair.AccessToken)
	require.NoError(t, err)
	require.Equal(t, admin.TokenKindAccess, claims.Kind)
}

func TestAuthUsecase_VerifyAccess_RevokedToken_ReturnsErrTokenRevoked(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := newStubClock(t, now)
	rev := usecasemocks.NewMockRevocationStore(t)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	rev.EXPECT().IsRevoked(mock.Anything, mock.Anything).Return(true, nil).Once()

	_, err = uc.VerifyAccess(context.Background(), pair.AccessToken)
	require.ErrorIs(t, err, apperr.ErrTokenRevoked)
}

func TestAuthUsecase_VerifyAccess_RevocationStoreFailure_PropagatesError(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := newStubClock(t, now)
	rev := usecasemocks.NewMockRevocationStore(t)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	storeErr := errors.New("redis: connection refused")
	rev.EXPECT().IsRevoked(mock.Anything, mock.Anything).Return(false, storeErr).Once()

	_, err = uc.VerifyAccess(context.Background(), pair.AccessToken)
	require.ErrorIs(t, err, storeErr)
	require.Contains(t, err.Error(), "AuthUsecase - VerifyAccess - RevocationStore.IsRevoked")
}

func TestAuthUsecase_VerifyAccess_UnknownKind_ReturnsErrInvalidCredentials(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := newStubClock(t, now)
	rev := usecasemocks.NewMockRevocationStore(t)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	bogus := signWithKind(t, []byte(testSecret), "bogus", now, now.Add(accessTTL))

	_, err := uc.VerifyAccess(context.Background(), bogus)
	require.ErrorIs(t, err, apperr.ErrInvalidCredentials)
}

func TestAuthUsecase_VerifyAccess_MissingExp_ReturnsErrInvalidCredentials(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := newStubClock(t, now)
	rev := usecasemocks.NewMockRevocationStore(t)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	claims := validAdminClaims(now)
	delete(claims, "exp")
	token := signWithClaims(t, []byte(testSecret), jwt.SigningMethodHS256, claims)

	_, err := uc.VerifyAccess(context.Background(), token)
	require.ErrorIs(t, err, apperr.ErrInvalidCredentials)
}

func TestAuthUsecase_VerifyAccess_MissingIssuedAt_ReturnsErrInvalidCredentials(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := newStubClock(t, now)
	rev := usecasemocks.NewMockRevocationStore(t)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	claims := validAdminClaims(now)
	delete(claims, "iat")
	token := signWithClaims(t, []byte(testSecret), jwt.SigningMethodHS256, claims)

	_, err := uc.VerifyAccess(context.Background(), token)
	require.ErrorIs(t, err, apperr.ErrInvalidCredentials)
}

func TestAuthUsecase_VerifyAccess_ExpiresBeforeIssuedAt_ReturnsErrInvalidCredentials(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := newStubClock(t, now)
	rev := usecasemocks.NewMockRevocationStore(t)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	claims := validAdminClaims(now)
	claims["exp"] = now.Unix()
	token := signWithClaims(t, []byte(testSecret), jwt.SigningMethodHS256, claims)

	_, err := uc.VerifyAccess(context.Background(), token)
	require.ErrorIs(t, err, apperr.ErrInvalidCredentials)
}

func TestAuthUsecase_VerifyAccess_WrongAlgorithm_ReturnsErrInvalidCredentials(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := newStubClock(t, now)
	rev := usecasemocks.NewMockRevocationStore(t)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	token := signWithClaims(t, []byte(testSecret), jwt.SigningMethodHS512, validAdminClaims(now))

	_, err := uc.VerifyAccess(context.Background(), token)
	require.ErrorIs(t, err, apperr.ErrInvalidCredentials)
}

func TestAuthUsecase_VerifyAccess_ForeignSubject_ReturnsErrInvalidCredentials(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := newStubClock(t, now)
	rev := usecasemocks.NewMockRevocationStore(t)
	uc := admin.NewAuthUsecase(newAuthCfg(), clk, rev)

	bogus := signWithSubject(t, []byte(testSecret), "not-admin", admin.TokenKindAccess, now, now.Add(accessTTL))

	_, err := uc.VerifyAccess(context.Background(), bogus)
	require.ErrorIs(t, err, apperr.ErrInvalidCredentials)
}
