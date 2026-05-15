package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
)

func TestCSRFGuard_AllowsUnsafePlayerRequestWithoutSessionCookie(t *testing.T) {
	t.Parallel()

	called := false
	handler := middleware.CSRFGuard()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/players/join", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.True(t, called)
	require.Equal(t, http.StatusNoContent, rr.Code)
}

func TestCSRFGuard_AllowsJoinWithSessionCookieForBootstrapCompatibility(t *testing.T) {
	t.Parallel()

	called := false
	handler := middleware.CSRFGuard()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/players/join", nil)
	req.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: uuid.NewString()})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.True(t, called)
	require.Equal(t, http.StatusNoContent, rr.Code)
}

func TestCSRFGuard_BlocksMissingCSRFForSessionCookie(t *testing.T) {
	t.Parallel()

	handler := middleware.CSRFGuard()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/players/logout", nil)
	req.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: uuid.NewString()})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusForbidden, rr.Code)
	require.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
	require.JSONEq(t, `{
		"type":"about:blank",
		"title":"Forbidden",
		"status":403,
		"detail":"csrf token invalid",
		"instance":"/api/v1/players/logout"
	}`, rr.Body.String())
}

func TestCSRFGuard_BlocksMismatchedCSRF(t *testing.T) {
	t.Parallel()

	sessionToken := uuid.New()
	cookieToken, err := middleware.NewPlayerCSRFToken(sessionToken)
	require.NoError(t, err)
	headerToken, err := middleware.NewPlayerCSRFToken(sessionToken)
	require.NoError(t, err)

	handler := middleware.CSRFGuard()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/players/logout", nil)
	req.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: sessionToken.String()})
	req.AddCookie(&http.Cookie{Name: middleware.PlayerCSRFCookieName, Value: cookieToken})
	req.Header.Set(middleware.CSRFHeaderName, headerToken)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusForbidden, rr.Code)
}

func TestCSRFGuard_BlocksTokenBoundToDifferentSession(t *testing.T) {
	t.Parallel()

	sessionToken := uuid.New()
	otherSessionToken := uuid.New()
	csrfToken, err := middleware.NewPlayerCSRFToken(otherSessionToken)
	require.NoError(t, err)

	handler := middleware.CSRFGuard()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/players/logout", nil)
	req.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: sessionToken.String()})
	req.AddCookie(&http.Cookie{Name: middleware.PlayerCSRFCookieName, Value: csrfToken})
	req.Header.Set(middleware.CSRFHeaderName, csrfToken)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusForbidden, rr.Code)
}

func TestCSRFGuard_AllowsMatchingCSRF(t *testing.T) {
	t.Parallel()

	sessionToken := uuid.New()
	csrfToken, err := middleware.NewPlayerCSRFToken(sessionToken)
	require.NoError(t, err)

	called := false
	handler := middleware.CSRFGuard()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/players/logout", nil)
	req.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: sessionToken.String()})
	req.AddCookie(&http.Cookie{Name: middleware.PlayerCSRFCookieName, Value: csrfToken})
	req.Header.Set(middleware.CSRFHeaderName, csrfToken)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.True(t, called)
	require.Equal(t, http.StatusNoContent, rr.Code)
}

func TestCSRFGuard_AllowsUnsafeAdminRequestWithoutCookieSession(t *testing.T) {
	t.Parallel()

	called := false
	handler := middleware.CSRFGuard()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tasks", nil)
	req.Header.Set("Authorization", "Bearer access-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.True(t, called)
	require.Equal(t, http.StatusNoContent, rr.Code)
}

func TestCSRFGuard_AllowsAdminLoginWithExistingCookiesForReauth(t *testing.T) {
	t.Parallel()

	called := false
	handler := middleware.CSRFGuard()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", nil)
	req.AddCookie(&http.Cookie{Name: middleware.AdminAccessCookieName, Value: "access-token"})
	req.AddCookie(&http.Cookie{Name: middleware.AdminRefreshCookieName, Value: "refresh-token"})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.True(t, called)
	require.Equal(t, http.StatusNoContent, rr.Code)
}

func TestCSRFGuard_BlocksMissingAdminAccessCSRF(t *testing.T) {
	t.Parallel()

	handler := middleware.CSRFGuard()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tasks", nil)
	req.AddCookie(&http.Cookie{Name: middleware.AdminAccessCookieName, Value: "access-token"})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusForbidden, rr.Code)
	require.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestCSRFGuard_AllowsMatchingAdminAccessCSRF(t *testing.T) {
	t.Parallel()

	token, err := middleware.NewAdminCSRFToken(middleware.AdminAccessCSRFCookieName, "access-token")
	require.NoError(t, err)

	called := false
	handler := middleware.CSRFGuard()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tasks", nil)
	req.AddCookie(&http.Cookie{Name: middleware.AdminAccessCookieName, Value: "access-token"})
	req.AddCookie(&http.Cookie{Name: middleware.AdminAccessCSRFCookieName, Value: token})
	req.Header.Set(middleware.CSRFHeaderName, token)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.True(t, called)
	require.Equal(t, http.StatusNoContent, rr.Code)
}

func TestCSRFGuard_RefreshUsesAdminRefreshCSRF(t *testing.T) {
	t.Parallel()

	refreshCSRF, err := middleware.NewAdminCSRFToken(middleware.AdminRefreshCSRFCookieName, "refresh-token")
	require.NoError(t, err)
	accessCSRF, err := middleware.NewAdminCSRFToken(middleware.AdminAccessCSRFCookieName, "access-token")
	require.NoError(t, err)

	handler := middleware.CSRFGuard()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	wrong := httptest.NewRecorder()
	wrongReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/refresh", nil)
	wrongReq.AddCookie(&http.Cookie{Name: middleware.AdminRefreshCookieName, Value: "refresh-token"})
	wrongReq.AddCookie(&http.Cookie{Name: middleware.AdminRefreshCSRFCookieName, Value: refreshCSRF})
	wrongReq.Header.Set(middleware.CSRFHeaderName, accessCSRF)
	handler.ServeHTTP(wrong, wrongReq)
	require.Equal(t, http.StatusForbidden, wrong.Code)

	right := httptest.NewRecorder()
	rightReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/refresh", nil)
	rightReq.AddCookie(&http.Cookie{Name: middleware.AdminRefreshCookieName, Value: "refresh-token"})
	rightReq.AddCookie(&http.Cookie{Name: middleware.AdminRefreshCSRFCookieName, Value: refreshCSRF})
	rightReq.Header.Set(middleware.CSRFHeaderName, refreshCSRF)
	handler.ServeHTTP(right, rightReq)
	require.Equal(t, http.StatusNoContent, right.Code)

	refreshHeader := httptest.NewRecorder()
	refreshHeaderReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/refresh", nil)
	refreshHeaderReq.AddCookie(&http.Cookie{Name: middleware.AdminRefreshCookieName, Value: "refresh-token"})
	refreshHeaderReq.AddCookie(&http.Cookie{Name: middleware.AdminRefreshCSRFCookieName, Value: refreshCSRF})
	refreshHeaderReq.Header.Set(middleware.CSRFHeaderName, accessCSRF)
	refreshHeaderReq.Header.Set(middleware.AdminRefreshCSRFHeaderName, refreshCSRF)
	handler.ServeHTTP(refreshHeader, refreshHeaderReq)
	require.Equal(t, http.StatusNoContent, refreshHeader.Code)
}

func TestEnsurePlayerCSRFCookieSetsReadableCookie(t *testing.T) {
	t.Parallel()

	sessionToken := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "https://app.example.com/api/v1/players/me", nil)
	rr := httptest.NewRecorder()

	require.NoError(t, middleware.EnsurePlayerCSRFCookie(rr, req, sessionToken))

	cookies := rr.Result().Cookies()
	require.Len(t, cookies, 1)
	require.Equal(t, middleware.PlayerCSRFCookieName, cookies[0].Name)
	require.NotEmpty(t, cookies[0].Value)
	require.Equal(t, cookies[0].Value, rr.Header().Get(middleware.CSRFHeaderName))
	require.False(t, cookies[0].HttpOnly)
	require.True(t, cookies[0].Secure)
	require.Equal(t, http.SameSiteLaxMode, cookies[0].SameSite)
}

func TestEnsurePlayerCSRFCookieReissuesTokenBoundToDifferentSession(t *testing.T) {
	t.Parallel()

	sessionToken := uuid.New()
	oldSessionToken := uuid.New()
	oldToken, err := middleware.NewPlayerCSRFToken(oldSessionToken)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/players/me", nil)
	req.AddCookie(&http.Cookie{Name: middleware.PlayerCSRFCookieName, Value: oldToken})
	rr := httptest.NewRecorder()

	require.NoError(t, middleware.EnsurePlayerCSRFCookie(rr, req, sessionToken))

	cookies := rr.Result().Cookies()
	require.Len(t, cookies, 1)
	require.NotEqual(t, oldToken, cookies[0].Value)
	require.Equal(t, cookies[0].Value, rr.Header().Get(middleware.CSRFHeaderName))
}

func TestSetAndClearPlayerCSRFCookie(t *testing.T) {
	t.Parallel()

	token, err := middleware.NewPlayerCSRFToken(uuid.New())
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/players/join", nil)
	setRecorder := httptest.NewRecorder()

	middleware.SetPlayerCSRFCookie(setRecorder, req, token)

	setCookie := setRecorder.Result().Cookies()[0]
	require.Equal(t, token, setRecorder.Header().Get(middleware.CSRFHeaderName))
	require.Equal(t, middleware.PlayerCSRFCookieName, setCookie.Name)
	require.Equal(t, token, setCookie.Value)
	require.Equal(t, "/", setCookie.Path)
	require.False(t, setCookie.HttpOnly)
	require.False(t, setCookie.Secure)
	require.Equal(t, http.SameSiteLaxMode, setCookie.SameSite)

	clearRecorder := httptest.NewRecorder()
	middleware.ClearPlayerCSRFCookie(clearRecorder, req)

	clearCookie := clearRecorder.Result().Cookies()[0]
	require.Equal(t, middleware.PlayerCSRFCookieName, clearCookie.Name)
	require.Equal(t, -1, clearCookie.MaxAge)
	require.True(t, clearCookie.Expires.Before(time.Now()))
}

func TestSetAndClearAdminCSRFCookies(t *testing.T) {
	t.Parallel()

	accessToken, err := middleware.NewAdminCSRFToken(middleware.AdminAccessCSRFCookieName, "access-token")
	require.NoError(t, err)
	refreshToken, err := middleware.NewAdminCSRFToken(middleware.AdminRefreshCSRFCookieName, "refresh-token")
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "https://app.example.com/api/v1/admin/login", nil)
	setRecorder := httptest.NewRecorder()

	middleware.SetAdminCSRFCookie(setRecorder, req, middleware.AdminAccessCSRFCookieName, accessToken, 60)
	middleware.SetAdminCSRFCookie(setRecorder, req, middleware.AdminRefreshCSRFCookieName, refreshToken, 3600)

	require.Equal(t, accessToken, setRecorder.Header().Get(middleware.CSRFHeaderName))
	require.Equal(t, refreshToken, setRecorder.Header().Get(middleware.AdminRefreshCSRFHeaderName))
	accessCookie := requireCookie(t, setRecorder.Result().Cookies(), middleware.AdminAccessCSRFCookieName)
	require.Equal(t, accessToken, accessCookie.Value)
	require.Equal(t, "/api/v1/admin", accessCookie.Path)
	require.False(t, accessCookie.HttpOnly)
	require.True(t, accessCookie.Secure)
	require.Equal(t, http.SameSiteLaxMode, accessCookie.SameSite)

	clearRecorder := httptest.NewRecorder()
	middleware.ClearAdminCSRFCookies(clearRecorder, req)
	clearCookies := clearRecorder.Result().Cookies()
	require.Equal(t, -1, requireCookie(t, clearCookies, middleware.AdminAccessCSRFCookieName).MaxAge)
	require.Equal(t, -1, requireCookie(t, clearCookies, middleware.AdminRefreshCSRFCookieName).MaxAge)
}

func requireCookie(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()

	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("cookie %q not found in %#v", name, cookies)
	return nil
}
