package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
)

func TestOriginGuard_BlocksUnsafeCrossOrigin(t *testing.T) {
	t.Parallel()

	called := false
	handler := middleware.OriginGuard([]string{"https://app.example.com"})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/players/logout", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.False(t, called)
	require.Equal(t, http.StatusForbidden, rr.Code)
	require.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
	require.JSONEq(t, `{
		"type":"about:blank",
		"title":"Forbidden",
		"status":403,
		"detail":"origin not allowed",
		"instance":"/api/v1/players/logout"
	}`, rr.Body.String())
}

func TestOriginGuard_AllowsConfiguredOrigin(t *testing.T) {
	t.Parallel()

	called := false
	handler := middleware.OriginGuard([]string{"https://app.example.com"})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/players/logout", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.True(t, called)
	require.Equal(t, http.StatusNoContent, rr.Code)
}

func TestOriginGuard_AllowsSameOriginWithoutAllowlist(t *testing.T) {
	t.Parallel()

	called := false
	handler := middleware.OriginGuard(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	}))
	req := httptest.NewRequest(http.MethodPost, "https://app.example.com/api/v1/players/logout", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.True(t, called)
	require.Equal(t, http.StatusAccepted, rr.Code)
}

func TestOriginGuard_AllowsSameOriginBehindTrustedProxy(t *testing.T) {
	t.Parallel()

	called := false
	handler := middleware.ForwardedProto([]string{"127.0.0.0/8"}, logkit.Noop())(
		middleware.OriginGuard(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusAccepted)
		})),
	)
	req := httptest.NewRequest(http.MethodPost, "http://app.example.com/api/v1/players/logout", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("X-Forwarded-Proto", "https")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.True(t, called)
	require.Equal(t, http.StatusAccepted, rr.Code)
}

func TestOriginGuard_IgnoresForwardedProtoFromUntrustedPeer(t *testing.T) {
	t.Parallel()

	called := false
	handler := middleware.ForwardedProto([]string{"127.0.0.0/8"}, logkit.Noop())(
		middleware.OriginGuard(nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			called = true
		})),
	)
	req := httptest.NewRequest(http.MethodPost, "http://app.example.com/api/v1/players/logout", nil)
	req.RemoteAddr = "198.51.100.10:1234"
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("X-Forwarded-Proto", "https")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.False(t, called)
	require.Equal(t, http.StatusForbidden, rr.Code)
}

func TestOriginGuard_AllowsUnsafeRequestWithoutBrowserOriginHeaders(t *testing.T) {
	t.Parallel()

	called := false
	handler := middleware.OriginGuard(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusCreated)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tasks", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.True(t, called)
	require.Equal(t, http.StatusCreated, rr.Code)
}

func TestOriginGuard_AllowsSafeCrossOriginRequest(t *testing.T) {
	t.Parallel()

	called := false
	handler := middleware.OriginGuard(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboard", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.True(t, called)
	require.Equal(t, http.StatusOK, rr.Code)
}

func TestOriginGuard_BlocksMalformedReferer(t *testing.T) {
	t.Parallel()

	called := false
	handler := middleware.OriginGuard(nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/tasks/00000000-0000-0000-0000-000000000001", nil)
	req.Header.Set("Referer", "://bad")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.False(t, called)
	require.Equal(t, http.StatusForbidden, rr.Code)
}
