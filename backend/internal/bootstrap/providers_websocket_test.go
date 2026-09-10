package bootstrap

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket"
	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
	authadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/auth"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
	authmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth/mocks"
)

func TestProvideOperatorSessionResolverUsesVerifiedAdminSubject(t *testing.T) {
	t.Parallel()

	revocations := authmocks.NewMockRevocationStore(t)
	revocations.EXPECT().IsRevoked(mock.Anything, mock.Anything).Return(false, nil).Twice()
	auth := newOperatorTestAuth(revocations, newOperatorTestClock(t), "tournament-operator-test-secret")
	pair, err := auth.Login(t.Context(), "operator-password")
	require.NoError(t, err)

	tournamentID := uuid.New()
	request := tournamentOperatorRequest(tournamentID, pair.AccessToken)
	resolver := provideOperatorSessionResolver(auth)

	session, ok := resolver(request, tournamentID)
	require.True(t, ok)
	principal := session.Principal
	require.True(t, principal.Authenticated)
	require.Equal(t, tournamentws.OperatorRealtimeRole, principal.Role)
	require.Equal(t, tournamentID, principal.TournamentID)
	expectedPrincipalID, err := inbound.OperatorActorID("admin")
	require.NoError(t, err)
	require.Equal(t, expectedPrincipalID, principal.PrincipalID)
	require.Equal(t, pair.AccessExpiresAt, session.ExpiresAt)

	require.True(t, session.Validate(t.Context()))
}

func TestProvideOperatorSessionResolverRejectsUnverifiedRequests(t *testing.T) {
	t.Parallel()

	revocations := authmocks.NewMockRevocationStore(t)
	auth := newOperatorTestAuth(revocations, newOperatorTestClock(t), "tournament-operator-test-secret")
	resolver := provideOperatorSessionResolver(auth)

	tests := []struct {
		name    string
		request *http.Request
	}{
		{name: "missing token", request: tournamentOperatorRequest(uuid.New(), "")},
		{name: "invalid token", request: tournamentOperatorRequest(uuid.New(), "not-a-token")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			session, ok := resolver(tt.request, uuid.New())

			require.False(t, ok)
			require.False(t, session.Principal.Authenticated)
			require.Equal(t, uuid.Nil, session.Principal.PrincipalID)
		})
	}
}

func TestProvideOperatorSessionResolverRevalidationFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		revoked bool
		err     error
	}{
		{name: "revoked", revoked: true},
		{name: "revocation store failure", err: errors.New("revocation store unavailable")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			revocations := authmocks.NewMockRevocationStore(t)
			revocations.EXPECT().IsRevoked(mock.Anything, mock.Anything).Return(false, nil).Once()
			revocations.EXPECT().IsRevoked(mock.Anything, mock.Anything).Return(tt.revoked, tt.err).Once()
			auth := newOperatorTestAuth(revocations, newOperatorTestClock(t), "tournament-operator-test-secret")
			pair, err := auth.Login(t.Context(), "operator-password")
			require.NoError(t, err)

			tournamentID := uuid.New()
			session, ok := provideOperatorSessionResolver(auth)(
				tournamentOperatorRequest(tournamentID, pair.AccessToken),
				tournamentID,
			)
			require.True(t, ok)
			require.False(t, session.Validate(context.Background()))
		})
	}
}

func TestProvideOperatorSessionResolverRejectsRotatedAccessToken(t *testing.T) {
	t.Parallel()

	revocations := authmocks.NewMockRevocationStore(t)
	revocations.EXPECT().IsRevoked(mock.Anything, mock.Anything).Return(false, nil).Twice()
	revocations.EXPECT().Revoke(mock.Anything, mock.Anything, mock.Anything).Return(nil).Twice()
	revocations.EXPECT().IsRevoked(mock.Anything, mock.Anything).Return(true, nil).Once()
	auth := newOperatorTestAuth(revocations, newOperatorTestClock(t), "tournament-operator-test-secret")
	pair, err := auth.Login(t.Context(), "operator-password")
	require.NoError(t, err)

	tournamentID := uuid.New()
	session, ok := provideOperatorSessionResolver(auth)(
		tournamentOperatorRequest(tournamentID, pair.AccessToken),
		tournamentID,
	)
	require.True(t, ok)
	require.True(t, session.Validate(t.Context()))

	_, err = auth.Refresh(t.Context(), pair.RefreshToken, pair.AccessToken)
	require.NoError(t, err)
	require.False(t, session.Validate(t.Context()))
}

func tournamentOperatorRequest(tournamentID uuid.UUID, token string) *http.Request {
	path := strings.ReplaceAll(websocket.TournamentOperatorWebSocketPath, "{tournament_id}", tournamentID.String())
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		request.AddCookie(&http.Cookie{Name: middleware.AdminAccessCookieName, Value: token})
	}
	return request
}

func newOperatorTestAuth(
	revocations authusecase.RevocationStore,
	clock authusecase.Clock,
	secret string,
) *authusecase.UseCase {
	return authusecase.NewUseCase(authusecase.Config{
		AccessTTL:  time.Hour,
		RefreshTTL: 24 * time.Hour,
	}, clock, revocations, authadapter.NewJWTCodec(authadapter.JWTConfig{
		Secret: []byte(secret),
		Now:    clock.Now,
	}), authadapter.NewPasswordVerifier([]byte("operator-password")))
}

func newOperatorTestClock(t *testing.T) *authmocks.MockClock {
	t.Helper()
	clock := authmocks.NewMockClock(t)
	clock.EXPECT().Now().Return(time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)).Maybe()
	return clock
}
