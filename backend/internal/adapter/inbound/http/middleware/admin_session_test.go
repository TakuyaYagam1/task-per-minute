package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	middlewaremocks "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware/mocks"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
)

func TestAdminSession_MissingCookieReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	auth := middlewaremocks.NewMockAdminAccessVerifier(t)
	handler := middleware.AdminSession(auth)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/admin/tasks", nil))

	requireUnauthorized(t, rr)
}

func TestAdminSession_RejectsAuthorizationBearerWithoutCookie(t *testing.T) {
	t.Parallel()

	auth := middlewaremocks.NewMockAdminAccessVerifier(t)
	handler := middleware.AdminSession(auth)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/tasks", nil)
	req.Header.Set("Authorization", "Bearer access-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	requireUnauthorized(t, rr)
}

func TestAdminSession_InvalidCookieReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	auth := middlewaremocks.NewMockAdminAccessVerifier(t)
	auth.EXPECT().VerifyAccess(mock.Anything, "not-a-jwt").Return(nil, domain.ErrInvalidCredentials).Once()
	handler := middleware.AdminSession(auth)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/tasks", nil)
	req.AddCookie(&http.Cookie{Name: middleware.AdminAccessCookieName, Value: "not-a-jwt"})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	requireUnauthorized(t, rr)
}

func TestAdminSession_ValidCookieInjectsClaims(t *testing.T) {
	t.Parallel()

	auth := middlewaremocks.NewMockAdminAccessVerifier(t)
	auth.EXPECT().VerifyAccess(mock.Anything, "access-token").Return(&authusecase.Claims{
		Subject: "admin",
		Kind:    authusecase.TokenKindAccess,
	}, nil).Once()

	handler := middleware.AdminSession(auth)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := middleware.GetAdminClaimsFromCtx(r.Context())
		require.True(t, ok)
		require.Equal(t, "admin", claims.Subject)
		require.Equal(t, authusecase.TokenKindAccess, claims.Kind)
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/tasks", nil)
	req.AddCookie(&http.Cookie{Name: middleware.AdminAccessCookieName, Value: "access-token"})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
	require.NotEmpty(t, rr.Header().Get(middleware.CSRFHeaderName))
	csrfCookie := requireRootCookie(t, rr.Result().Cookies(), middleware.AdminAccessCSRFCookieName)
	require.Equal(t, "/", csrfCookie.Path)
	require.False(t, csrfCookie.HttpOnly)
	csrfToken, valid := middleware.AdminCSRFTokenFromRequest(
		requestWithCookie(req, csrfCookie),
		middleware.AdminAccessCSRFCookieName,
		"access-token",
	)
	require.True(t, valid)
	require.Equal(t, csrfCookie.Value, csrfToken)
}

func TestAdminSession_IgnoresAuthorizationWhenCookieIsValid(t *testing.T) {
	t.Parallel()

	auth := middlewaremocks.NewMockAdminAccessVerifier(t)
	auth.EXPECT().VerifyAccess(mock.Anything, "access-token").Return(&authusecase.Claims{
		Subject: "admin",
		Kind:    authusecase.TokenKindAccess,
	}, nil).Once()

	handler := middleware.AdminSession(auth)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/tasks", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	req.AddCookie(&http.Cookie{Name: middleware.AdminAccessCookieName, Value: "access-token"})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
}

func requireUnauthorized(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()

	require.Equal(t, http.StatusUnauthorized, rr.Code)
	require.True(t, strings.HasPrefix(rr.Header().Get("Content-Type"), "application/problem+json"))
	require.Contains(t, rr.Body.String(), `"status":401`)
}

func requestWithCookie(req *http.Request, cookie *http.Cookie) *http.Request {
	clonedRequest := req.Clone(req.Context())
	clonedRequest.Header = make(http.Header)
	clonedRequest.AddCookie(cookie)
	return clonedRequest
}

func requireRootCookie(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()

	for _, cookie := range cookies {
		if cookie.Name == name && cookie.Path == "/" {
			return cookie
		}
	}
	t.Fatalf("cookie %q with root path not found in %#v", name, cookies)
	return nil
}
