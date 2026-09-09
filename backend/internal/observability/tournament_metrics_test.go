package observability

import (
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func TestTournamentMetrics(t *testing.T) {
	t.Parallel()

	t.Run("records bounded terminal outcome once", func(t *testing.T) {
		t.Parallel()

		metrics := NewTournamentMetrics()
		event := tournamentMetricTestEvent(t, TournamentOutcomeSuccess, "submission")

		require.NoError(t, metrics.Record(event))
		require.NoError(t, metrics.Record(event))

		families, err := metrics.Gatherer().Gather()
		require.NoError(t, err)
		require.InDelta(t, 1, tournamentMetricCounterValue(
			t,
			families,
			"tpm_tournament_operations_total",
			map[string]string{"operation": "submission", "outcome": "success"},
		), 0)
		tournamentRequireBoundedLabels(t, families)
	})

	t.Run("counts retry attempts without terminal double count", func(t *testing.T) {
		t.Parallel()

		metrics := NewTournamentMetrics()
		event := tournamentMetricTestEvent(t, TournamentOutcomeRetry, "wave_start")

		require.NoError(t, metrics.Record(event))
		require.NoError(t, metrics.Record(event))

		families, err := metrics.Gatherer().Gather()
		require.NoError(t, err)
		require.InDelta(t, 2, tournamentMetricCounterValue(
			t,
			families,
			"tpm_tournament_operations_total",
			map[string]string{"operation": "wave", "outcome": "retry"},
		), 0)
	})

	t.Run("does not count idempotent terminal replays", func(t *testing.T) {
		t.Parallel()

		metrics := NewTournamentMetrics()
		event := tournamentMetricTestEvent(t, TournamentOutcomeSuccess, "submission")
		event.ReasonCode = "already_committed"

		require.NoError(t, metrics.Record(event))
		families, err := metrics.Gatherer().Gather()
		require.NoError(t, err)
		require.Empty(t, families)
	})

	t.Run("uses bounded fallback and dedicated lag and drift series", func(t *testing.T) {
		t.Parallel()

		metrics := NewTournamentMetrics()
		event := tournamentMetricTestEvent(t, TournamentOutcomeFailure, "unmapped_stage")
		require.NoError(t, metrics.Record(event))
		require.NoError(t, metrics.ObserveLag("deadline", 250*time.Millisecond))
		require.NoError(t, metrics.SetClockDrift("database", -125*time.Millisecond))

		families, err := metrics.Gatherer().Gather()
		require.NoError(t, err)
		require.InDelta(t, 1, tournamentMetricCounterValue(
			t,
			families,
			"tpm_tournament_operations_total",
			map[string]string{"operation": "other", "outcome": "failure"},
		), 0)
		require.Equal(t, uint64(1), tournamentMetricHistogramCount(
			t,
			families,
			"tpm_tournament_lag_seconds",
			map[string]string{"kind": "deadline"},
		))
		require.InDelta(t, -0.125, tournamentMetricGaugeValue(
			t,
			families,
			"tpm_tournament_clock_drift_seconds",
			map[string]string{"source": "database"},
		), 0.0001)
		tournamentRequireBoundedLabels(t, families)
	})

	t.Run("maps participant stages to fixed operation labels", func(t *testing.T) {
		t.Parallel()

		for _, test := range []struct {
			stage     string
			operation string
		}{
			{stage: "readiness", operation: "wave"},
			{stage: "draft", operation: "game"},
			{stage: "post_series", operation: "settlement"},
		} {
			t.Run(test.stage, func(t *testing.T) {
				metrics := NewTournamentMetrics()
				require.NoError(t, metrics.Record(tournamentMetricTestEvent(t, TournamentOutcomeSuccess, test.stage)))
				families, err := metrics.Gatherer().Gather()
				require.NoError(t, err)
				require.InDelta(t, 1, tournamentMetricCounterValue(
					t,
					families,
					"tpm_tournament_operations_total",
					map[string]string{"operation": test.operation, "outcome": "success"},
				), 0)
			})
		}
	})

	t.Run("rejects sensitive and unbounded metric inputs", func(t *testing.T) {
		t.Parallel()

		metrics := NewTournamentMetrics()
		invalid := tournamentMetricTestEvent(t, TournamentOutcomeSuccess, "submission")
		invalid.Stage = "player_token"

		require.ErrorIs(t, metrics.Record(invalid), ErrTournamentMetricInput)
		require.ErrorIs(t, metrics.ObserveLag("participant-42", time.Second), ErrTournamentMetricInput)
		require.ErrorIs(t, metrics.ObserveLag("deadline", -time.Second), ErrTournamentMetricInput)
		require.ErrorIs(t, metrics.SetClockDrift("client", time.Second), ErrTournamentMetricInput)
	})

	t.Run("isolates registries", func(t *testing.T) {
		t.Parallel()

		first := NewTournamentMetrics()
		second := NewTournamentMetrics()

		require.NotSame(t, first.Gatherer(), second.Gatherer())
		require.NoError(t, first.Record(tournamentMetricTestEvent(t, TournamentOutcomeSuccess, "game")))
		families, err := second.Gatherer().Gather()
		require.NoError(t, err)
		require.Empty(t, families)
	})
}

func tournamentMetricTestEvent(t *testing.T, outcome, stage string) TournamentEvent {
	t.Helper()

	event, err := NewTournamentEvent(TournamentEventInput{
		Event:         "tournament.metric.sample",
		Outcome:       outcome,
		CorrelationID: "correlation-1",
		TournamentID:  "tournament-1",
		EntityKind:    "submission",
		EntityID:      "entity-1",
		Stage:         stage,
		Transition:    "processed",
		Duration:      20 * time.Millisecond,
		ReasonCode:    "processed",
		Revision:      1,
	})
	require.NoError(t, err)
	return event
}

func tournamentMetricCounterValue(
	t *testing.T,
	families []*dto.MetricFamily,
	name string,
	labels map[string]string,
) float64 {
	t.Helper()
	metric := tournamentMetricWithLabels(t, families, name, labels)
	require.NotNil(t, metric.Counter)
	return metric.Counter.GetValue()
}

func tournamentMetricHistogramCount(
	t *testing.T,
	families []*dto.MetricFamily,
	name string,
	labels map[string]string,
) uint64 {
	t.Helper()
	metric := tournamentMetricWithLabels(t, families, name, labels)
	require.NotNil(t, metric.Histogram)
	return metric.Histogram.GetSampleCount()
}

func tournamentMetricGaugeValue(
	t *testing.T,
	families []*dto.MetricFamily,
	name string,
	labels map[string]string,
) float64 {
	t.Helper()
	metric := tournamentMetricWithLabels(t, families, name, labels)
	require.NotNil(t, metric.Gauge)
	return metric.Gauge.GetValue()
}

func tournamentMetricWithLabels(
	t *testing.T,
	families []*dto.MetricFamily,
	name string,
	labels map[string]string,
) *dto.Metric {
	t.Helper()
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			actual := make(map[string]string, len(metric.Label))
			for _, pair := range metric.Label {
				actual[pair.GetName()] = pair.GetValue()
			}
			if mapsEqual(actual, labels) {
				return metric
			}
		}
	}
	t.Fatalf("metric %s with labels %v not found", name, labels)
	return nil
}

func mapsEqual(left, right map[string]string) bool {
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

func tournamentRequireBoundedLabels(t *testing.T, families []*dto.MetricFamily) {
	t.Helper()
	allowed := map[string]bool{
		"kind":      true,
		"operation": true,
		"outcome":   true,
		"queue":     true,
		"reason":    true,
		"source":    true,
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, pair := range metric.Label {
				require.True(t, allowed[pair.GetName()], "unexpected label %q", pair.GetName())
				require.NotContains(t, pair.GetValue(), "tournament-1")
				require.NotContains(t, pair.GetValue(), "entity-1")
				require.NotContains(t, pair.GetValue(), "correlation-1")
			}
		}
	}
}
