package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
)

func TestAuth_AdminRefreshSession(t *testing.T) {
	t.Parallel()

	t.Run("rejects a missing refresh cookie", func(t *testing.T) {
		t.Parallel()

		called := false
		handler := adminRefreshAuthTestHandler(t, &called)
		request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/refresh", nil)
		request.Header.Set(CSRFHeaderName, "contract-presence")
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)

		require.False(t, called)
		require.Equal(t, http.StatusUnauthorized, response.Code)
		require.Equal(t, "application/problem+json", response.Header().Get("Content-Type"))
	})

	t.Run("passes the refresh cookie to the owning handler", func(t *testing.T) {
		t.Parallel()

		called := false
		handler := adminRefreshAuthTestHandler(t, &called)
		request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/refresh", nil)
		request.AddCookie(&http.Cookie{Name: AdminRefreshCookieName, Value: "refresh-session"})
		request.Header.Set(CSRFHeaderName, "contract-presence")
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)

		require.True(t, called)
		require.Equal(t, http.StatusNoContent, response.Code)
	})
}

type adminRefreshAuthTestServer struct {
	api.Unimplemented

	called *bool
}

func (server *adminRefreshAuthTestServer) RefreshAdminSession(
	w http.ResponseWriter,
	_ *http.Request,
	_ api.RefreshAdminSessionParams,
) {
	*server.called = true
	w.WriteHeader(http.StatusNoContent)
}

func adminRefreshAuthTestHandler(t *testing.T, called *bool) http.Handler {
	t.Helper()
	return api.HandlerWithOptions(&adminRefreshAuthTestServer{called: called}, api.ChiServerOptions{
		Middlewares: []api.MiddlewareFunc{Auth(nil, nil)},
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			t.Errorf("generated admin refresh binding failed: %v", err)
			w.WriteHeader(http.StatusBadRequest)
		},
	})
}
