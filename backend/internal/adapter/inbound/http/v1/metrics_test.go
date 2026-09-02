package v1

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

func TestArenaPrivateMetricsEndpoint(t *testing.T) {
	t.Parallel()

	t.Run("scrapes the isolated registry", func(t *testing.T) {
		t.Parallel()

		metrics := observability.NewArenaMetrics()
		event, err := observability.NewArenaEvent(observability.ArenaEventInput{
			Event:         "arena.http",
			Outcome:       observability.ArenaOutcomeRejected,
			CorrelationID: "request-private-1",
			TournamentID:  "tournament-private-1",
			EntityKind:    "request",
			EntityID:      "entity-private-1",
			Stage:         "operator_mutation",
			Transition:    "post:/arena/operator/tournaments",
			Duration:      5 * time.Millisecond,
			ReasonCode:    "conflict",
		})
		require.NoError(t, err)
		require.NoError(t, metrics.Record(event))

		server := New(Dependencies{ArenaMetrics: metrics.Gatherer()})
		request := httptest.NewRequest(http.MethodGet, "/internal/metrics", nil)
		response := httptest.NewRecorder()
		server.ArenaPrivateMetricsHandler().ServeHTTP(response, request)

		require.Equal(t, http.StatusOK, response.Code)
		body := response.Body.String()
		require.Contains(t, body, "tpm_arena_operations_total")
		require.Contains(t, body, `operation="http",outcome="rejected"`)
		for _, forbidden := range []string{
			"request-private-1",
			"tournament-private-1",
			"entity-private-1",
			"correlation_id",
			"entity_id",
			"tournament_id",
			"flag",
			"token",
		} {
			require.NotContains(t, strings.ToLower(body), forbidden)
		}
	})

	t.Run("is absent from the generated public router", func(t *testing.T) {
		t.Parallel()

		metrics := observability.NewArenaMetrics()
		public := NewHandler(New(Dependencies{ArenaMetrics: metrics.Gatherer()}), HandlerOptions{})
		request := httptest.NewRequest(http.MethodGet, "/internal/metrics", nil)
		response := httptest.NewRecorder()
		public.ServeHTTP(response, request)

		require.Equal(t, http.StatusNotFound, response.Code)
	})

	t.Run("fails closed without a registry", func(t *testing.T) {
		t.Parallel()

		request := httptest.NewRequest(http.MethodGet, "/internal/metrics", nil)
		response := httptest.NewRecorder()
		New(Dependencies{}).ArenaPrivateMetricsHandler().ServeHTTP(response, request)

		require.Equal(t, http.StatusServiceUnavailable, response.Code)
	})

	t.Run("allows independent server construction without duplicate collectors", func(t *testing.T) {
		t.Parallel()

		first := New(Dependencies{ArenaMetrics: observability.NewArenaMetrics().Gatherer()})
		second := New(Dependencies{ArenaMetrics: observability.NewArenaMetrics().Gatherer()})
		for _, server := range []*Server{first, second} {
			response := httptest.NewRecorder()
			server.ArenaPrivateMetricsHandler().ServeHTTP(
				response,
				httptest.NewRequest(http.MethodGet, "/internal/metrics", nil),
			)
			require.Equal(t, http.StatusOK, response.Code)
		}
	})
}

var _ api.ServerInterface = (*Server)(nil)
