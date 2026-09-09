package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
	authmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth/mocks"
)

func TestAuthUseCase_VerifyAccess_AfterLogin(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := newStubClock(t, now)
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	rev.EXPECT().IsRevoked(mock.Anything, mock.Anything).Return(false, nil).Once()

	claims, err := uc.VerifyAccess(context.Background(), pair.AccessToken)
	require.NoError(t, err)
	require.Equal(t, authusecase.TokenKindAccess, claims.Kind)
	require.NotEmpty(t, claims.JTI)
}

func TestAuthUseCase_VerifyAccess_RejectsRefreshToken(t *testing.T) {
	t.Parallel()

	clk := newStubClock(t, time.Now().UTC())
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	_, err = uc.VerifyAccess(context.Background(), pair.RefreshToken)
	require.ErrorIs(t, err, domain.ErrInvalidCredentials,
		"a refresh token must NOT pass VerifyAccess - kind mismatch")
}

func TestAuthUseCase_VerifyAccess_ExpiredToken_ReturnsErrTokenExpired(t *testing.T) {
	t.Parallel()

	issuedAt := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk, setClock := newMutableClock(t, issuedAt)
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	setClock(issuedAt.Add(accessTTL + time.Minute))

	_, err = uc.VerifyAccess(context.Background(), pair.AccessToken)
	require.ErrorIs(t, err, domain.ErrTokenExpired)
}

func TestAuthUseCase_VerifyAccess_BadSignature_ReturnsErrInvalidCredentials(t *testing.T) {
	t.Parallel()

	clk := newStubClock(t, time.Now().UTC())
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	_, err := uc.VerifyAccess(context.Background(), "garbage.not.a.jwt")
	require.ErrorIs(t, err, domain.ErrInvalidCredentials)
}

func TestAuthUseCase_VerifyAccess_DifferentSecret_ReturnsErrInvalidCredentials(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	clk := newStubClock(t, now)
	rev := authmocks.NewMockRevocationStore(t)

	uc := newAuthUseCase(newAuthCfg(), clk, rev)
	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	cfg2 := newAuthCfg()
	cfg2.Secret = []byte("ffffeeeeddddccccbbbbaaaa9999888877776666555544443333222211110000")
	uc2 := newAuthUseCase(cfg2, clk, rev)

	_, err = uc2.VerifyAccess(context.Background(), pair.AccessToken)
	require.ErrorIs(t, err, domain.ErrInvalidCredentials,
		"token signed with a different secret must be rejected")
}

func TestAuthUseCase_VerifyAccess_ClockSkewWithinLeeway_Accepted(t *testing.T) {
	t.Parallel()

	issuedAt := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk, setClock := newMutableClock(t, issuedAt)
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	setClock(issuedAt.Add(accessTTL + 5*time.Second))

	rev.EXPECT().IsRevoked(mock.Anything, mock.Anything).Return(false, nil).Once()

	claims, err := uc.VerifyAccess(context.Background(), pair.AccessToken)
	require.NoError(t, err)
	require.Equal(t, authusecase.TokenKindAccess, claims.Kind)
}

func TestAuthUseCase_VerifyAccess_RevokedToken_ReturnsErrTokenRevoked(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := newStubClock(t, now)
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	rev.EXPECT().IsRevoked(mock.Anything, mock.Anything).Return(true, nil).Once()

	_, err = uc.VerifyAccess(context.Background(), pair.AccessToken)
	require.ErrorIs(t, err, domain.ErrTokenRevoked)
}

func TestAuthUseCase_VerifyAccess_RevocationStoreFailure_PropagatesError(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := newStubClock(t, now)
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	storeErr := errors.New("redis: connection refused")
	rev.EXPECT().IsRevoked(mock.Anything, mock.Anything).Return(false, storeErr).Once()

	_, err = uc.VerifyAccess(context.Background(), pair.AccessToken)
	require.ErrorIs(t, err, storeErr)
	require.Contains(t, err.Error(), "auth verify access revocation")
}

func TestAuthUseCase_VerifyAccess_UnknownKind_ReturnsErrInvalidCredentials(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := newStubClock(t, now)
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	bogus := signWithKind(t, []byte(testSecret), "bogus", now, now.Add(accessTTL))

	_, err := uc.VerifyAccess(context.Background(), bogus)
	require.ErrorIs(t, err, domain.ErrInvalidCredentials)
}

func TestAuthUseCase_VerifyAccess_MissingExp_ReturnsErrInvalidCredentials(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := newStubClock(t, now)
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	claims := validAdminClaims(now)
	delete(claims, "exp")
	token := signWithClaims(t, []byte(testSecret), jwt.SigningMethodHS256, claims)

	_, err := uc.VerifyAccess(context.Background(), token)
	require.ErrorIs(t, err, domain.ErrInvalidCredentials)
}

func TestAuthUseCase_VerifyAccess_MissingIssuedAt_ReturnsErrInvalidCredentials(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := newStubClock(t, now)
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	claims := validAdminClaims(now)
	delete(claims, "iat")
	token := signWithClaims(t, []byte(testSecret), jwt.SigningMethodHS256, claims)

	_, err := uc.VerifyAccess(context.Background(), token)
	require.ErrorIs(t, err, domain.ErrInvalidCredentials)
}

func TestAuthUseCase_VerifyAccess_ExpiresBeforeIssuedAt_ReturnsErrInvalidCredentials(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := newStubClock(t, now)
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	claims := validAdminClaims(now)
	claims["exp"] = now.Unix()
	token := signWithClaims(t, []byte(testSecret), jwt.SigningMethodHS256, claims)

	_, err := uc.VerifyAccess(context.Background(), token)
	require.ErrorIs(t, err, domain.ErrInvalidCredentials)
}

func TestAuthUseCase_VerifyAccess_WrongAlgorithm_ReturnsErrInvalidCredentials(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := newStubClock(t, now)
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	token := signWithClaims(t, []byte(testSecret), jwt.SigningMethodHS512, validAdminClaims(now))

	_, err := uc.VerifyAccess(context.Background(), token)
	require.ErrorIs(t, err, domain.ErrInvalidCredentials)
}

func TestAuthUseCase_VerifyAccess_ForeignSubject_ReturnsErrInvalidCredentials(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := newStubClock(t, now)
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	bogus := signWithSubject(t, []byte(testSecret), "not-admin", authusecase.TokenKindAccess, now, now.Add(accessTTL))

	_, err := uc.VerifyAccess(context.Background(), bogus)
	require.ErrorIs(t, err, domain.ErrInvalidCredentials)
}
