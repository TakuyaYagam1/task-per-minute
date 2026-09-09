package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	middlewaremocks "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware/mocks"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
)

const adminAccessTestToken = "admin-access-token"

func TestAdminLoginSetsHttpOnlySessionCookies(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()
	pair := &authusecase.TokenPair{
		AccessToken:      "access-token",
		RefreshToken:     "refresh-token",
		AccessExpiresAt:  now.Add(time.Minute),
		RefreshExpiresAt: now.Add(time.Hour),
	}
	auth := NewMockAdminAuthService(t)
	auth.EXPECT().Login(mock.Anything, "admin-password").Return(pair, nil)
	server := New(Dependencies{
		AdminAuth:    auth,
		LoginLimiter: newAllowingRateLimiter(t),
		Now:          func() time.Time { return now },
	})

	req := httptest.NewRequest(http.MethodPost, "https://app.example.com/api/v1/admin/login", strings.NewReader(`{"password":"admin-password"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	server.LoginAdmin(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	cookies := rr.Result().Cookies()
	require.Len(t, cookies, 4)

	accessCookie := requireCookie(t, cookies, middleware.AdminAccessCookieName)
	require.Equal(t, "access-token", accessCookie.Value)
	require.Equal(t, "/api/v1/admin", accessCookie.Path)
	require.True(t, accessCookie.HttpOnly)
	require.True(t, accessCookie.Secure)
	require.Equal(t, http.SameSiteLaxMode, accessCookie.SameSite)

	refreshCookie := requireCookie(t, cookies, middleware.AdminRefreshCookieName)
	require.Equal(t, "refresh-token", refreshCookie.Value)
	require.Equal(t, "/api/v1/admin", refreshCookie.Path)
	require.True(t, refreshCookie.HttpOnly)
	require.True(t, refreshCookie.Secure)
	require.Equal(t, http.SameSiteLaxMode, refreshCookie.SameSite)

	accessCSRFCookie := requireCookie(t, cookies, middleware.AdminAccessCSRFCookieName)
	require.NotEmpty(t, accessCSRFCookie.Value)
	require.Equal(t, accessCSRFCookie.Value, rr.Header().Get(middleware.CSRFHeaderName))
	require.Equal(t, "/api/v1/admin", accessCSRFCookie.Path)
	require.False(t, accessCSRFCookie.HttpOnly)
	require.True(t, accessCSRFCookie.Secure)

	refreshCSRFCookie := requireCookie(t, cookies, middleware.AdminRefreshCSRFCookieName)
	require.NotEmpty(t, refreshCSRFCookie.Value)
	require.Equal(t, refreshCSRFCookie.Value, rr.Header().Get(middleware.AdminRefreshCSRFHeaderName))
	require.Equal(t, "/api/v1/admin", refreshCSRFCookie.Path)
	require.False(t, refreshCSRFCookie.HttpOnly)
	require.True(t, refreshCSRFCookie.Secure)

	got := decodeAdminSessionResponse(t, rr)
	require.Equal(t, int32(60), got.ExpiresIn)
	require.NotContains(t, rr.Body.String(), "access_token")
	require.NotContains(t, rr.Body.String(), "refresh_token")
}

func TestAdminRefreshRateLimited(t *testing.T) {
	t.Parallel()

	pair := &authusecase.TokenPair{
		AccessToken:      "access",
		RefreshToken:     "refresh",
		AccessExpiresAt:  time.Unix(100, 0).UTC().Add(time.Minute),
		RefreshExpiresAt: time.Unix(100, 0).UTC().Add(time.Hour),
	}
	auth := NewMockAdminAuthService(t)
	auth.EXPECT().Refresh(mock.Anything, "refresh-token").Return(pair, nil)
	server := New(Dependencies{
		AdminAuth:      auth,
		RefreshLimiter: newOneRequestRateLimiter(t, "3600"),
		Now:            func() time.Time { return time.Unix(100, 0).UTC() },
	})

	first := httptest.NewRecorder()
	firstReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/refresh", nil)
	firstReq.AddCookie(&http.Cookie{Name: middleware.AdminRefreshCookieName, Value: "refresh-token"})
	firstReq.RemoteAddr = "198.51.100.10:1234"
	server.RefreshAdminSession(first, firstReq, api.RefreshAdminSessionParams{})
	require.Equal(t, http.StatusOK, first.Code)

	second := httptest.NewRecorder()
	secondReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/refresh", nil)
	secondReq.AddCookie(&http.Cookie{Name: middleware.AdminRefreshCookieName, Value: "refresh-token"})
	secondReq.RemoteAddr = "198.51.100.10:1234"
	server.RefreshAdminSession(second, secondReq, api.RefreshAdminSessionParams{})
	require.Equal(t, http.StatusTooManyRequests, second.Code)
	require.Equal(t, "3600", second.Header().Get("Retry-After"))
}

func TestAdminRefreshUsesRefreshCookie(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()
	pair := &authusecase.TokenPair{
		AccessToken:      "next-access",
		RefreshToken:     "next-refresh",
		AccessExpiresAt:  now.Add(time.Minute),
		RefreshExpiresAt: now.Add(time.Hour),
	}
	auth := NewMockAdminAuthService(t)
	auth.EXPECT().Refresh(mock.Anything, "cookie-refresh").Return(pair, nil)
	server := New(Dependencies{
		AdminAuth:      auth,
		RefreshLimiter: newAllowingRateLimiter(t),
		Now:            func() time.Time { return now },
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/refresh", nil)
	req.AddCookie(&http.Cookie{Name: middleware.AdminRefreshCookieName, Value: "cookie-refresh"})
	rr := httptest.NewRecorder()

	server.RefreshAdminSession(rr, req, api.RefreshAdminSessionParams{})

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "next-access", requireCookie(t, rr.Result().Cookies(), middleware.AdminAccessCookieName).Value)
	require.Equal(t, "next-refresh", requireCookie(t, rr.Result().Cookies(), middleware.AdminRefreshCookieName).Value)
	require.NotEmpty(t, rr.Header().Get(middleware.CSRFHeaderName))
	require.NotEmpty(t, rr.Header().Get(middleware.AdminRefreshCSRFHeaderName))

	got := decodeAdminSessionResponse(t, rr)
	require.Equal(t, int32(60), got.ExpiresIn)
	require.NotContains(t, rr.Body.String(), "next-access")
	require.NotContains(t, rr.Body.String(), "next-refresh")
}

func TestAdminRefreshRejectsBodyTokenWithoutCookie(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()
	auth := NewMockAdminAuthService(t)
	server := New(Dependencies{
		AdminAuth:      auth,
		RefreshLimiter: newAllowingRateLimiter(t),
		Now:            func() time.Time { return now },
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/refresh", strings.NewReader(`{"refresh_token":"body-refresh"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	server.RefreshAdminSession(rr, req, api.RefreshAdminSessionParams{})

	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAdminLogoutRejectsBodyTokenWithoutCookie(t *testing.T) {
	t.Parallel()

	auth := NewMockAdminAuthService(t)
	server := New(Dependencies{AdminAuth: auth})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/logout", strings.NewReader(`{"refresh_token":"body-refresh"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	server.LogoutAdmin(rr, req, api.LogoutAdminParams{})

	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAdminLogoutUsesRefreshCookieWithoutAccessAndClearsAdminCookies(t *testing.T) {
	t.Parallel()

	auth := NewMockAdminAuthService(t)
	auth.EXPECT().Logout(mock.Anything, "refresh-token").Return(nil).Once()
	server := New(Dependencies{AdminAuth: auth})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/logout", nil)
	req.AddCookie(&http.Cookie{Name: middleware.AdminRefreshCookieName, Value: "refresh-token"})
	rr := httptest.NewRecorder()

	server.LogoutAdmin(rr, req, api.LogoutAdminParams{})

	require.Equal(t, http.StatusNoContent, rr.Code)
	cookies := rr.Result().Cookies()
	require.Len(t, cookies, 4)
	require.Equal(t, -1, requireCookie(t, cookies, middleware.AdminAccessCookieName).MaxAge)
	require.Equal(t, -1, requireCookie(t, cookies, middleware.AdminRefreshCookieName).MaxAge)
	require.Equal(t, -1, requireCookie(t, cookies, middleware.AdminAccessCSRFCookieName).MaxAge)
	require.Equal(t, -1, requireCookie(t, cookies, middleware.AdminRefreshCSRFCookieName).MaxAge)
}

func TestAdminLogoutRevokesAccessCookie(t *testing.T) {
	t.Parallel()

	auth := NewMockAdminAuthService(t)
	auth.EXPECT().Logout(mock.Anything, "refresh-token", []string{"access-token"}).Return(nil).Once()
	server := New(Dependencies{AdminAuth: auth})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/logout", nil)
	req.AddCookie(&http.Cookie{Name: middleware.AdminAccessCookieName, Value: "access-token"})
	req.AddCookie(&http.Cookie{Name: middleware.AdminRefreshCookieName, Value: "refresh-token"})
	rr := httptest.NewRecorder()

	server.LogoutAdmin(rr, req, api.LogoutAdminParams{})

	require.Equal(t, http.StatusNoContent, rr.Code)
}

func TestAdminLogoutRouteAllowsRefreshCookieWithoutAccess(t *testing.T) {
	t.Parallel()

	auth := NewMockAdminAuthService(t)
	auth.EXPECT().Logout(mock.Anything, "refresh-token").Return(nil).Once()
	server := New(Dependencies{AdminAuth: auth})
	verifier := middlewaremocks.NewMockAdminAccessVerifier(t)
	handler := NewHandler(server, HandlerOptions{AdminAuth: verifier})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/logout", nil)
	req.AddCookie(&http.Cookie{Name: middleware.AdminRefreshCookieName, Value: "refresh-token"})
	csrfToken, err := middleware.NewAdminCSRFToken(middleware.AdminRefreshCSRFCookieName, "refresh-token")
	require.NoError(t, err)
	req.AddCookie(&http.Cookie{Name: middleware.AdminRefreshCSRFCookieName, Value: csrfToken})
	req.Header.Set(middleware.CSRFHeaderName, csrfToken)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
}

func newAdminAccessVerifier(t *testing.T) *middlewaremocks.MockAdminAccessVerifier {
	t.Helper()

	verifier := middlewaremocks.NewMockAdminAccessVerifier(t)
	verifier.EXPECT().VerifyAccess(mock.Anything, adminAccessTestToken).Return(&authusecase.Claims{
		JTI:       "admin-access-session",
		Subject:   "admin",
		Kind:      authusecase.TokenKindAccess,
		IssuedAt:  time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC),
		ExpiresAt: time.Date(2026, 5, 13, 12, 15, 0, 0, time.UTC),
	}, nil).Once()
	return verifier
}

func decodeAdminSessionResponse(t *testing.T, rr *httptest.ResponseRecorder) api.AdminSessionResponse {
	t.Helper()

	var got api.AdminSessionResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	return got
}
