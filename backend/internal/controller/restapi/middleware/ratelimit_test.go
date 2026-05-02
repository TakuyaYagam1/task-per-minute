package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	httpkitmw "github.com/wahrwelt-kit/go-httpkit/httputil/middleware"

	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
)

func TestLoginRateLimiter_AllowsBurstPerIP(t *testing.T) {
	t.Parallel()

	limiter := middleware.NewLoginRateLimiter(t.Context(), 2, time.Hour, time.Hour)

	require.True(t, limiter.Allow("198.51.100.10"))
	require.True(t, limiter.Allow("198.51.100.10"))
	require.False(t, limiter.Allow("198.51.100.10"))
	require.True(t, limiter.Allow("198.51.100.11"))
	require.True(t, limiter.Allow(""))
}

func TestLoginRateLimiter_InvalidConfigIsNoop(t *testing.T) {
	t.Parallel()

	limiter := middleware.NewLoginRateLimiter(t.Context(), 0, time.Hour, time.Hour)

	require.True(t, limiter.Allow("198.51.100.10"))
}

func TestLoginRateLimiter_RetryAfterUsesConfiguredWindow(t *testing.T) {
	t.Parallel()

	limiter := middleware.NewLoginRateLimiter(t.Context(), 5, 90*time.Second, time.Hour)

	require.Equal(t, "90", limiter.RetryAfter())
}

func TestJoinRateLimiter_InvalidConfigIsNoop(t *testing.T) {
	t.Parallel()

	limiter := middleware.NewJoinRateLimiter(t.Context(), 0, time.Hour, time.Hour)

	require.True(t, limiter.Allow("198.51.100.10"))
}

func TestJoinRateLimiter_RetryAfterUsesConfiguredWindow(t *testing.T) {
	t.Parallel()

	limiter := middleware.NewJoinRateLimiter(t.Context(), 5, 1500*time.Millisecond, time.Hour)

	require.Equal(t, "2", limiter.RetryAfter())
}

func TestClientIPFromRequest(t *testing.T) {
	t.Parallel()

	require.Empty(t, middleware.ClientIPFromRequest(nil))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	require.Equal(t, "203.0.113.10", middleware.ClientIPFromRequest(req))

	req.RemoteAddr = "203.0.113.10"
	require.Equal(t, "203.0.113.10", middleware.ClientIPFromRequest(req))
}

func TestClientIPFromRequest_PrefersResolvedContextIP(t *testing.T) {
	t.Parallel()

	clientIP, err := httpkitmw.ClientIP([]string{"127.0.0.0/8"})
	require.NoError(t, err)

	var got string
	handler := clientIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = middleware.ClientIPFromRequest(r)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.42")

	handler.ServeHTTP(httptest.NewRecorder(), req)

	require.Equal(t, "198.51.100.42", got)
}
