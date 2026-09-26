package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTournamentRatePolicyCoversPublicCatalogRoutes(t *testing.T) {
	t.Parallel()

	server := New(Dependencies{})
	for _, path := range []string{
		"/api/v1/public/tournaments",
		"/api/v1/public/tournaments/alpha",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		policy, ok := server.tournamentRatePolicy(req)
		require.True(t, ok, path)
		require.Equal(t, tournamentPublicRead, policy.class, path)
	}
}
