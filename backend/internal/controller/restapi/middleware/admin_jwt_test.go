package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/repo/inmem"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
)

func TestAdminJWT_MissingHeaderReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	handler := middleware.AdminJWT(newAuthUsecase(t))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/admin/tasks", nil))

	requireUnauthorized(t, rr)
}

func TestAdminJWT_InvalidTokenReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	handler := middleware.AdminJWT(newAuthUsecase(t))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/tasks", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	requireUnauthorized(t, rr)
}

func TestAdminJWT_ValidAccessTokenInjectsClaims(t *testing.T) {
	t.Parallel()

	auth := newAuthUsecase(t)
	pair, err := auth.Login(t.Context(), "admin-password")
	require.NoError(t, err)

	handler := middleware.AdminJWT(auth)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := middleware.GetAdminClaimsFromCtx(r.Context())
		require.True(t, ok)
		require.Equal(t, "admin", claims.Subject)
		require.Equal(t, admin.TokenKindAccess, claims.Kind)
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
}

func newAuthUsecase(t *testing.T) *admin.AuthUsecase {
	t.Helper()

	clk := fixedClock{now: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)}
	return admin.NewAuthUsecase(admin.AuthConfig{
		Secret:        []byte("01234567890123456789012345678901"),
		AccessTTL:     15 * time.Minute,
		RefreshTTL:    7 * 24 * time.Hour,
		AdminPassword: []byte("admin-password"),
	}, clk, inmem.NewRevocation(clk))
}

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}

func requireUnauthorized(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()

	require.Equal(t, http.StatusUnauthorized, rr.Code)
	require.True(t, strings.HasPrefix(rr.Header().Get("Content-Type"), "application/problem+json"))
	require.Contains(t, rr.Body.String(), `"status":401`)
}
