package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	httpkitmw "github.com/wahrwelt-kit/go-httpkit/httputil/middleware"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
)

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

func TestNewClientIPResolver_UsesTrustedProxyHeaders(t *testing.T) {
	t.Parallel()

	resolver, err := middleware.NewClientIPResolver([]string{"127.0.0.0/8"})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.42")

	require.Equal(t, "198.51.100.42", resolver(req))
}

func TestNewClientIPResolver_RejectsUntrustedForwardedHeaders(t *testing.T) {
	t.Parallel()

	resolver, err := middleware.NewClientIPResolver([]string{"127.0.0.0/8"})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.42")

	require.Equal(t, "203.0.113.10", resolver(req))
}
