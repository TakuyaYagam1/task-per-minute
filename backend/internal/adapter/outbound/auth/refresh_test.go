package auth_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth/mocks"
)

func TestAuthUseCase_Refresh_AfterLogin(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := newStubClock(t, now)
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

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

func TestAuthUseCase_Refresh_RevokesCurrentAccessToken(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	clock := newStubClock(t, now)
	revocations := newMemoryRevocationStore()
	useCase := newAuthUseCase(newAuthCfg(), clock, revocations)

	pair, err := useCase.Login(t.Context(), testPass)
	require.NoError(t, err)
	_, err = useCase.VerifyAccess(t.Context(), pair.AccessToken)
	require.NoError(t, err)

	rotated, err := useCase.Refresh(t.Context(), pair.RefreshToken, pair.AccessToken)
	require.NoError(t, err)
	require.NotEmpty(t, rotated.AccessToken)
	_, err = useCase.VerifyAccess(t.Context(), pair.AccessToken)
	require.ErrorIs(t, err, domain.ErrTokenRevoked)
	_, err = useCase.VerifyAccess(t.Context(), rotated.AccessToken)
	require.NoError(t, err)
}

func TestAuthUseCase_Refresh_RevokedToken_ReturnsErrTokenRevoked(t *testing.T) {
	t.Parallel()

	clk := newStubClock(t, time.Now().UTC())
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	rev.EXPECT().Revoke(mock.Anything, mock.Anything, mock.Anything).Return(domain.ErrTokenRevoked).Once()

	_, err = uc.Refresh(context.Background(), pair.RefreshToken)
	require.ErrorIs(t, err, domain.ErrTokenRevoked)
}

func TestAuthUseCase_Refresh_ReusingOldRefreshTokenReturnsErrTokenRevoked(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk, _ := newMutableClock(t, now)
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	rev.EXPECT().Revoke(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	rev.EXPECT().Revoke(mock.Anything, mock.Anything, mock.Anything).Return(domain.ErrTokenRevoked).Once()

	rotated, err := uc.Refresh(context.Background(), pair.RefreshToken)
	require.NoError(t, err)
	require.NotEmpty(t, rotated.RefreshToken)

	_, err = uc.Refresh(context.Background(), pair.RefreshToken)
	require.ErrorIs(t, err, domain.ErrTokenRevoked)
}

func TestAuthUseCase_Refresh_ReusingOldRefreshTokenInsideClockSkewLeewayReturnsErrTokenRevoked(t *testing.T) {
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

	rotated, err := uc.Refresh(context.Background(), pair.RefreshToken)
	require.NoError(t, err)
	require.NotEmpty(t, rotated.RefreshToken)

	_, err = uc.Refresh(context.Background(), pair.RefreshToken)
	require.ErrorIs(t, err, domain.ErrTokenRevoked)
}

func TestAuthUseCase_Refresh_RejectsAccessToken(t *testing.T) {
	t.Parallel()

	clk := newStubClock(t, time.Now().UTC())
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	_, err = uc.Refresh(context.Background(), pair.AccessToken)
	require.ErrorIs(t, err, domain.ErrInvalidCredentials,
		"access token in Refresh must be rejected by kind check")
}

func TestAuthUseCase_Refresh_RevocationStoreFailure_PropagatesError(t *testing.T) {
	t.Parallel()

	clk := newStubClock(t, time.Now().UTC())
	rev := authmocks.NewMockRevocationStore(t)
	uc := newAuthUseCase(newAuthCfg(), clk, rev)

	pair, err := uc.Login(context.Background(), testPass)
	require.NoError(t, err)

	storeErr := errors.New("redis: connection refused")
	rev.EXPECT().Revoke(mock.Anything, mock.Anything, mock.Anything).Return(storeErr).Once()

	_, err = uc.Refresh(context.Background(), pair.RefreshToken)
	require.ErrorIs(t, err, storeErr)
	require.Contains(t, err.Error(), "auth refresh revoke token")
}

type memoryRevocationStore struct {
	mu      sync.Mutex
	revoked map[string]time.Time
}

func newMemoryRevocationStore() *memoryRevocationStore {
	return &memoryRevocationStore{revoked: make(map[string]time.Time)}
}

func (store *memoryRevocationStore) Revoke(_ context.Context, jti string, expiresAt time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.revoked[jti]; exists {
		return domain.ErrTokenRevoked
	}
	store.revoked[jti] = expiresAt
	return nil
}

func (store *memoryRevocationStore) IsRevoked(_ context.Context, jti string) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	_, exists := store.revoked[jti]
	return exists, nil
}
