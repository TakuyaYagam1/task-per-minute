package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
)

func TestNoStoreSensitiveResponses_AddsHeadersForSensitivePaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
	}{
		{name: "admin", path: "/api/v1/admin/login"},
		{name: "player", path: "/api/v1/players/me"},
		{name: "duel", path: "/api/v1/duels/11111111-1111-1111-1111-111111111111"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := middleware.NoStoreSensitiveResponses()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			require.Equal(t, http.StatusNoContent, rr.Code)
			require.Equal(t, "no-store", rr.Header().Get("Cache-Control"))
			require.Equal(t, "no-cache", rr.Header().Get("Pragma"))
			require.Equal(t, "0", rr.Header().Get("Expires"))
		})
	}
}

func TestNoStoreSensitiveResponses_LeavesPublicCacheablePathsUntouched(t *testing.T) {
	t.Parallel()

	handler := middleware.NoStoreSensitiveResponses()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboard", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Empty(t, rr.Header().Get("Cache-Control"))
	require.Empty(t, rr.Header().Get("Pragma"))
	require.Empty(t, rr.Header().Get("Expires"))
}
