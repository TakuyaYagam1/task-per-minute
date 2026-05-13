package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
	"github.com/TakuyaYagam1/task-per-minute/internal/repo/inmem"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	adminusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
)

func TestAdminLoginSetsHttpOnlySessionCookies(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()
	auth := &adminCookieAuthStub{
		loginPair: &usecase.TokenPair{
			AccessToken:      "access-token",
			RefreshToken:     "refresh-token",
			AccessExpiresAt:  now.Add(time.Minute),
			RefreshExpiresAt: now.Add(time.Hour),
		},
	}
	server := New(Dependencies{
		AdminAuth:    auth,
		LoginLimiter: middleware.NewLoginRateLimiter(t.Context(), 10, time.Hour, time.Hour),
		Now:          func() time.Time { return now },
	})

	req := httptest.NewRequest(http.MethodPost, "https://app.example.com/api/v1/admin/login", strings.NewReader(`{"password":"admin-password"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	server.AdminLogin(rr, req)

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
}

func TestAdminLoginBrowserSourceReturnsCookieSessionMarkers(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()
	auth := &adminCookieAuthStub{
		loginPair: &usecase.TokenPair{
			AccessToken:      "access-token",
			RefreshToken:     "refresh-token",
			AccessExpiresAt:  now.Add(time.Minute),
			RefreshExpiresAt: now.Add(time.Hour),
		},
	}
	server := New(Dependencies{
		AdminAuth:    auth,
		LoginLimiter: middleware.NewLoginRateLimiter(t.Context(), 10, time.Hour, time.Hour),
		Now:          func() time.Time { return now },
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(`{"password":"admin-password"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://app.example.com")
	rr := httptest.NewRecorder()

	server.AdminLogin(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "access-token", requireCookie(t, rr.Result().Cookies(), middleware.AdminAccessCookieName).Value)
	require.Equal(t, "refresh-token", requireCookie(t, rr.Result().Cookies(), middleware.AdminRefreshCookieName).Value)

	got := decodeAdminTokenResponse(t, rr)
	require.Equal(t, response.CookieAdminSessionToken, got.AccessToken)
	require.Equal(t, response.CookieAdminSessionToken, got.RefreshToken)
	require.Equal(t, int32(60), got.ExpiresIn)
}

func TestAdminLoginFetchMetadataReturnsCookieSessionMarkers(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()
	auth := &adminCookieAuthStub{
		loginPair: &usecase.TokenPair{
			AccessToken:      "access-token",
			RefreshToken:     "refresh-token",
			AccessExpiresAt:  now.Add(time.Minute),
			RefreshExpiresAt: now.Add(time.Hour),
		},
	}
	server := New(Dependencies{
		AdminAuth:    auth,
		LoginLimiter: middleware.NewLoginRateLimiter(t.Context(), 10, time.Hour, time.Hour),
		Now:          func() time.Time { return now },
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(`{"password":"admin-password"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()

	server.AdminLogin(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	got := decodeAdminTokenResponse(t, rr)
	require.Equal(t, response.CookieAdminSessionToken, got.AccessToken)
	require.Equal(t, response.CookieAdminSessionToken, got.RefreshToken)
	require.Equal(t, int32(60), got.ExpiresIn)
}

func TestAdminRefreshRateLimited(t *testing.T) {
	t.Parallel()

	server := New(Dependencies{
		AdminAuth:      refreshAuthStub{},
		RefreshLimiter: middleware.NewLoginRateLimiter(t.Context(), 1, time.Hour, time.Hour),
		Now:            func() time.Time { return time.Unix(100, 0).UTC() },
	})

	body := `{"refresh_token":"refresh-token"}`
	first := httptest.NewRecorder()
	firstReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/refresh", strings.NewReader(body))
	firstReq.Header.Set("Content-Type", "application/json")
	firstReq.RemoteAddr = "198.51.100.10:1234"
	server.AdminRefresh(first, firstReq)
	require.Equal(t, http.StatusOK, first.Code)

	second := httptest.NewRecorder()
	secondReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/refresh", strings.NewReader(body))
	secondReq.Header.Set("Content-Type", "application/json")
	secondReq.RemoteAddr = "198.51.100.10:1234"
	server.AdminRefresh(second, secondReq)
	require.Equal(t, http.StatusTooManyRequests, second.Code)
	require.Equal(t, "3600", second.Header().Get("Retry-After"))
}

func TestAdminRefreshUsesRefreshCookieWhenBodyTokenIsEmpty(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()
	auth := &adminCookieAuthStub{
		refreshPair: &usecase.TokenPair{
			AccessToken:      "next-access",
			RefreshToken:     "next-refresh",
			AccessExpiresAt:  now.Add(time.Minute),
			RefreshExpiresAt: now.Add(time.Hour),
		},
	}
	server := New(Dependencies{
		AdminAuth:      auth,
		RefreshLimiter: middleware.NewLoginRateLimiter(t.Context(), 10, time.Hour, time.Hour),
		Now:            func() time.Time { return now },
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/refresh", strings.NewReader(`{"refresh_token":""}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: middleware.AdminRefreshCookieName, Value: "cookie-refresh"})
	rr := httptest.NewRecorder()

	server.AdminRefresh(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "cookie-refresh", auth.refreshToken)
	require.Equal(t, "next-access", requireCookie(t, rr.Result().Cookies(), middleware.AdminAccessCookieName).Value)
	require.Equal(t, "next-refresh", requireCookie(t, rr.Result().Cookies(), middleware.AdminRefreshCookieName).Value)
	require.NotEmpty(t, rr.Header().Get(middleware.CSRFHeaderName))
	require.NotEmpty(t, rr.Header().Get(middleware.AdminRefreshCSRFHeaderName))

	got := decodeAdminTokenResponse(t, rr)
	require.Equal(t, response.CookieAdminSessionToken, got.AccessToken)
	require.Equal(t, response.CookieAdminSessionToken, got.RefreshToken)
	require.Equal(t, int32(60), got.ExpiresIn)
}

func TestAdminRefreshBodyTokenReturnsRawTokenResponse(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()
	auth := &adminCookieAuthStub{
		refreshPair: &usecase.TokenPair{
			AccessToken:      "next-access",
			RefreshToken:     "next-refresh",
			AccessExpiresAt:  now.Add(time.Minute),
			RefreshExpiresAt: now.Add(time.Hour),
		},
	}
	server := New(Dependencies{
		AdminAuth:      auth,
		RefreshLimiter: middleware.NewLoginRateLimiter(t.Context(), 10, time.Hour, time.Hour),
		Now:            func() time.Time { return now },
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/refresh", strings.NewReader(`{"refresh_token":"body-refresh"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	server.AdminRefresh(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "body-refresh", auth.refreshToken)

	got := decodeAdminTokenResponse(t, rr)
	require.Equal(t, "next-access", got.AccessToken)
	require.Equal(t, "next-refresh", got.RefreshToken)
}

func TestAdminLogoutUsesRefreshCookieWithoutAccessAndClearsAdminCookies(t *testing.T) {
	t.Parallel()

	auth := newAdminCookieAuthUsecase(t)
	pair, err := auth.Login(t.Context(), "admin-password")
	require.NoError(t, err)

	server := New(Dependencies{AdminAuth: auth})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/logout", strings.NewReader(`{"refresh_token":""}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: middleware.AdminRefreshCookieName, Value: pair.RefreshToken})
	rr := httptest.NewRecorder()

	server.AdminLogout(rr, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
	cookies := rr.Result().Cookies()
	require.Len(t, cookies, 4)
	require.Equal(t, -1, requireCookie(t, cookies, middleware.AdminAccessCookieName).MaxAge)
	require.Equal(t, -1, requireCookie(t, cookies, middleware.AdminRefreshCookieName).MaxAge)
	require.Equal(t, -1, requireCookie(t, cookies, middleware.AdminAccessCSRFCookieName).MaxAge)
	require.Equal(t, -1, requireCookie(t, cookies, middleware.AdminRefreshCSRFCookieName).MaxAge)
	_, err = auth.Refresh(t.Context(), pair.RefreshToken)
	require.ErrorIs(t, err, apperr.ErrTokenRevoked)
}

func TestAdminLogoutRevokesAccessCookie(t *testing.T) {
	t.Parallel()

	auth := newAdminCookieAuthUsecase(t)
	pair, err := auth.Login(t.Context(), "admin-password")
	require.NoError(t, err)

	server := New(Dependencies{AdminAuth: auth})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/logout", strings.NewReader(`{"refresh_token":""}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: middleware.AdminAccessCookieName, Value: pair.AccessToken})
	req.AddCookie(&http.Cookie{Name: middleware.AdminRefreshCookieName, Value: pair.RefreshToken})
	rr := httptest.NewRecorder()

	server.AdminLogout(rr, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
	_, err = auth.VerifyAccess(t.Context(), pair.AccessToken)
	require.ErrorIs(t, err, apperr.ErrTokenRevoked)
}

func TestAdminLogoutRouteAllowsRefreshCookieWithoutAccess(t *testing.T) {
	t.Parallel()

	auth := newAdminCookieAuthUsecase(t)
	pair, err := auth.Login(t.Context(), "admin-password")
	require.NoError(t, err)

	server := New(Dependencies{AdminAuth: auth})
	handler := NewHandler(server, HandlerOptions{AdminAuth: auth})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/logout", strings.NewReader(`{"refresh_token":""}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: middleware.AdminRefreshCookieName, Value: pair.RefreshToken})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
	_, err = auth.Refresh(t.Context(), pair.RefreshToken)
	require.ErrorIs(t, err, apperr.ErrTokenRevoked)
}

type refreshAuthStub struct{}

func (refreshAuthStub) Login(context.Context, string) (*usecase.TokenPair, error) {
	panic("unused")
}

func (refreshAuthStub) Refresh(context.Context, string) (*usecase.TokenPair, error) {
	now := time.Unix(100, 0).UTC()
	return &usecase.TokenPair{
		AccessToken:      "access",
		RefreshToken:     "refresh",
		AccessExpiresAt:  now.Add(time.Minute),
		RefreshExpiresAt: now.Add(time.Hour),
	}, nil
}

func (refreshAuthStub) Logout(context.Context, string, ...string) error {
	panic("unused")
}

type adminCookieAuthStub struct {
	loginPair    *usecase.TokenPair
	refreshPair  *usecase.TokenPair
	refreshToken string
	logoutToken  string
}

func (s *adminCookieAuthStub) Login(context.Context, string) (*usecase.TokenPair, error) {
	return s.loginPair, nil
}

func (s *adminCookieAuthStub) Refresh(_ context.Context, token string) (*usecase.TokenPair, error) {
	s.refreshToken = token
	return s.refreshPair, nil
}

func (s *adminCookieAuthStub) Logout(_ context.Context, token string, _ ...string) error {
	s.logoutToken = token
	return nil
}

func newAdminCookieAuthUsecase(t *testing.T) *adminusecase.AuthUsecase {
	t.Helper()

	clk := adminCookieFixedClock{now: time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)}
	return adminusecase.NewAuthUsecase(adminusecase.AuthConfig{
		Secret:        []byte("01234567890123456789012345678901"),
		AccessTTL:     15 * time.Minute,
		RefreshTTL:    time.Hour,
		AdminPassword: []byte("admin-password"),
	}, clk, inmem.NewRevocation(clk))
}

func decodeAdminTokenResponse(t *testing.T, rr *httptest.ResponseRecorder) openapi.AdminTokenResponse {
	t.Helper()

	var got openapi.AdminTokenResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	return got
}

type adminCookieFixedClock struct {
	now time.Time
}

func (c adminCookieFixedClock) Now() time.Time {
	return c.now
}
