package arena

import (
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	appobservability "github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

func TestArenaTransportMetricsCallSites(t *testing.T) {
	t.Parallel()

	metrics := appobservability.NewArenaMetrics()
	tournamentID := testUUID("73000000-0000-4000-8000-000000000001")
	for _, input := range []ArenaTransportEvent{
		{
			CorrelationID: "ws-connect", TournamentID: tournamentID, Role: "participant",
			Action: ArenaTransportConnect, Outcome: appobservability.ArenaOutcomeSuccess,
			ReasonCode: "connected", Duration: 10 * time.Millisecond, Revision: 1,
		},
		{
			CorrelationID: "ws-delivery", TournamentID: tournamentID, Role: "participant",
			Action: ArenaTransportDelivery, Outcome: appobservability.ArenaOutcomeFailure,
			ReasonCode: "write_failed", Duration: 20 * time.Millisecond, Revision: 2,
		},
	} {
		require.NoError(t, ObserveTransportEvent(t.Context(), metrics, input))
	}
	appobservability.ObserveArenaLag(metrics, "projection", 150*time.Millisecond)

	families, err := metrics.Gatherer().Gather()
	require.NoError(t, err)
	require.InDelta(t, 1, arenaTransportCounter(t, families, "realtime", "success"), 0)
	require.InDelta(t, 1, arenaTransportCounter(t, families, "realtime", "failure"), 0)
	require.Equal(t, uint64(1), arenaTransportHistogramCount(t, families, "projection"))
	arenaTransportRequireLabels(t, families)
}

func arenaTransportCounter(
	t *testing.T,
	families []*dto.MetricFamily,
	operation string,
	outcome string,
) float64 {
	t.Helper()
	metric := arenaTransportMetric(t, families, "tpm_arena_operations_total", map[string]string{
		"operation": operation,
		"outcome":   outcome,
	})
	return metric.GetCounter().GetValue()
}

func arenaTransportHistogramCount(
	t *testing.T,
	families []*dto.MetricFamily,
	kind string,
) uint64 {
	t.Helper()
	metric := arenaTransportMetric(
		t,
		families,
		"tpm_arena_lag_seconds",
		map[string]string{"kind": kind},
	)
	return metric.GetHistogram().GetSampleCount()
}

func arenaTransportMetric(
	t *testing.T,
	families []*dto.MetricFamily,
	name string,
	want map[string]string,
) *dto.Metric {
	t.Helper()
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			labels := make(map[string]string, len(metric.Label))
			for _, pair := range metric.Label {
				labels[pair.GetName()] = pair.GetValue()
			}
			if arenaTransportLabelsEqual(labels, want) {
				return metric
			}
		}
	}
	t.Fatalf("metric %s with labels %v not found", name, want)
	return nil
}

func arenaTransportLabelsEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func arenaTransportRequireLabels(t *testing.T, families []*dto.MetricFamily) {
	t.Helper()
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, pair := range metric.Label {
				require.Contains(t, []string{"kind", "operation", "outcome", "source"}, pair.GetName())
				require.NotContains(t, pair.GetValue(), "73000000")
				require.NotContains(t, pair.GetValue(), "ws-")
			}
		}
	}
}
