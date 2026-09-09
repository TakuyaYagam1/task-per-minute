package tournament

import (
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	appobservability "github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

func TestTournamentTransportMetricsCallSites(t *testing.T) {
	t.Parallel()

	metrics := appobservability.NewTournamentMetrics()
	tournamentID := testUUID("73000000-0000-4000-8000-000000000001")
	for _, input := range []TournamentTransportEvent{
		{
			CorrelationID: "ws-connect", TournamentID: tournamentID, Role: "participant",
			Action: TournamentTransportConnect, Outcome: appobservability.TournamentOutcomeSuccess,
			ReasonCode: "connected", Duration: 10 * time.Millisecond, Revision: 1,
		},
		{
			CorrelationID: "ws-delivery", TournamentID: tournamentID, Role: "participant",
			Action: TournamentTransportDelivery, Outcome: appobservability.TournamentOutcomeFailure,
			ReasonCode: "write_failed", Duration: 20 * time.Millisecond, Revision: 2,
		},
	} {
		require.NoError(t, ObserveTransportEvent(t.Context(), metrics, input))
	}
	appobservability.ObserveTournamentLag(metrics, "projection", 150*time.Millisecond)

	families, err := metrics.Gatherer().Gather()
	require.NoError(t, err)
	require.InDelta(t, 1, tournamentTransportCounter(t, families, "realtime", "success"), 0)
	require.InDelta(t, 1, tournamentTransportCounter(t, families, "realtime", "failure"), 0)
	require.Equal(t, uint64(1), tournamentTransportHistogramCount(t, families, "projection"))
	tournamentTransportRequireLabels(t, families)
}

func tournamentTransportCounter(
	t *testing.T,
	families []*dto.MetricFamily,
	operation string,
	outcome string,
) float64 {
	t.Helper()
	metric := tournamentTransportMetric(t, families, "tpm_tournament_operations_total", map[string]string{
		"operation": operation,
		"outcome":   outcome,
	})
	return metric.GetCounter().GetValue()
}

func tournamentTransportHistogramCount(
	t *testing.T,
	families []*dto.MetricFamily,
	kind string,
) uint64 {
	t.Helper()
	metric := tournamentTransportMetric(
		t,
		families,
		"tpm_tournament_lag_seconds",
		map[string]string{"kind": kind},
	)
	return metric.GetHistogram().GetSampleCount()
}

func tournamentTransportMetric(
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
			if tournamentTransportLabelsEqual(labels, want) {
				return metric
			}
		}
	}
	t.Fatalf("metric %s with labels %v not found", name, want)
	return nil
}

func tournamentTransportLabelsEqual(left, right map[string]string) bool {
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

func tournamentTransportRequireLabels(t *testing.T, families []*dto.MetricFamily) {
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
