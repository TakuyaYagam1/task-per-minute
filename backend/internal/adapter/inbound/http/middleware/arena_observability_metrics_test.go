package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

func TestArenaTransportMetricsCallSites(t *testing.T) {
	t.Parallel()

	metrics := observability.NewArenaMetrics()
	handler := middleware.ArenaStructuredLogging(metrics)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusConflict)
		},
	))
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/arena/operator/tournaments/73000000-0000-4000-8000-000000000001/start",
		nil,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	require.Equal(t, http.StatusConflict, response.Code)
	families, err := metrics.Gatherer().Gather()
	require.NoError(t, err)
	found := false
	for _, family := range families {
		if family.GetName() != "tpm_arena_operations_total" {
			continue
		}
		for _, metric := range family.Metric {
			labels := make(map[string]string, len(metric.Label))
			for _, pair := range metric.Label {
				labels[pair.GetName()] = pair.GetValue()
				require.NotContains(t, pair.GetValue(), "73000000")
			}
			if labels["operation"] == "http" && labels["outcome"] == "rejected" {
				require.InDelta(t, 1, metric.GetCounter().GetValue(), 0)
				found = true
			}
		}
	}
	require.True(t, found, "HTTP conflict metric was not recorded")
}
