package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
)

func TestCORS_AllowedPreflight(t *testing.T) {
	t.Parallel()

	called := false
	handler := middleware.CORS([]string{"http://localhost:3000"})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/players/join", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "Content-Type, X-Session-Token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.False(t, called)
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.Equal(t, "http://localhost:3000", rr.Header().Get("Access-Control-Allow-Origin"))
	require.Equal(t, "GET, POST, PUT, DELETE, OPTIONS", rr.Header().Get("Access-Control-Allow-Methods"))
	require.Equal(t, "Content-Type, Authorization, X-Session-Token", rr.Header().Get("Access-Control-Allow-Headers"))
	require.Equal(t, "Retry-After", rr.Header().Get("Access-Control-Expose-Headers"))
	require.Contains(t, rr.Header().Values("Vary"), "Origin")
	require.Contains(t, rr.Header().Values("Vary"), "Access-Control-Request-Method")
	require.Contains(t, rr.Header().Values("Vary"), "Access-Control-Request-Headers")
}

func TestCORS_DisallowedPreflight(t *testing.T) {
	t.Parallel()

	called := false
	handler := middleware.CORS([]string{"https://app.example.com"})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/admin/tasks", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.False(t, called)
	require.Equal(t, http.StatusForbidden, rr.Code)
	require.Empty(t, rr.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORS_AllowedNormalRequest(t *testing.T) {
	t.Parallel()

	handler := middleware.CORS([]string{"https://app.example.com"})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusTooManyRequests, rr.Code)
	require.Equal(t, "https://app.example.com", rr.Header().Get("Access-Control-Allow-Origin"))
	require.Equal(t, "Retry-After", rr.Header().Get("Access-Control-Expose-Headers"))
	require.Equal(t, "10", rr.Header().Get("Retry-After"))
}

func TestCORS_DisabledPassthrough(t *testing.T) {
	t.Parallel()

	called := false
	handler := middleware.CORS(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	}))
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/players/join", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.True(t, called)
	require.Equal(t, http.StatusAccepted, rr.Code)
	require.Empty(t, rr.Header().Get("Access-Control-Allow-Origin"))
}
