package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
)

func TestForwardedProtoSetsSecureCookieForTrustedProxy(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	handler := middleware.ForwardedProto([]string{"127.0.0.0/8"}, logkit.Noop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		middleware.SetPlayerSessionCookie(w, r, token)
	}))
	req := httptest.NewRequest(http.MethodPost, "http://app.example.com/api/v1/players/join", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-Proto", "https")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	cookie := requireCookie(t, rr.Result().Cookies(), middleware.PlayerSessionCookieName)
	require.True(t, cookie.Secure)
}

func TestForwardedProtoIgnoresUntrustedProxyHeader(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	handler := middleware.ForwardedProto([]string{"127.0.0.0/8"}, logkit.Noop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		middleware.SetPlayerSessionCookie(w, r, token)
	}))
	req := httptest.NewRequest(http.MethodPost, "http://app.example.com/api/v1/players/join", nil)
	req.RemoteAddr = "198.51.100.10:1234"
	req.Header.Set("X-Forwarded-Proto", "https")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	cookie := requireCookie(t, rr.Result().Cookies(), middleware.PlayerSessionCookieName)
	require.False(t, cookie.Secure)
}

func TestForwardedProtoRejectsChainedProtoHeader(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	handler := middleware.ForwardedProto([]string{"127.0.0.0/8"}, logkit.Noop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		middleware.SetPlayerSessionCookie(w, r, token)
	}))
	req := httptest.NewRequest(http.MethodPost, "http://app.example.com/api/v1/players/join", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-Proto", "https, http")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	cookie := requireCookie(t, rr.Result().Cookies(), middleware.PlayerSessionCookieName)
	require.False(t, cookie.Secure)
}
