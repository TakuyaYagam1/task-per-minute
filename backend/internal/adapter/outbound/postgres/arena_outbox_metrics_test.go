package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

func TestArenaTransportMetricsCallSites(t *testing.T) {
	t.Parallel()

	metrics := observability.NewArenaMetrics()
	tournamentID := outboxTestUUID(730)
	record := outboxTestEvent(731, tournamentID, 1, "published", true, false)
	record.createdAt = time.Now().UTC().Add(-2 * time.Second)
	sink := &outboxTestSink{failures: 1}
	publisher := newArenaOutboxPublisher(
		&outboxTestTx{db: &outboxTestDB{events: []*outboxTestRecord{record}}},
		sink,
		metrics,
	)

	published, err := publisher.PublishNext(context.Background())
	require.Error(t, err)
	require.False(t, published)
	published, err = publisher.PublishNext(context.Background())
	require.NoError(t, err)
	require.True(t, published)

	families, err := metrics.Gatherer().Gather()
	require.NoError(t, err)
	values := map[string]float64{}
	lagCount := uint64(0)
	for _, family := range families {
		switch family.GetName() {
		case "tpm_arena_operations_total":
			for _, metric := range family.Metric {
				labels := map[string]string{}
				for _, pair := range metric.Label {
					labels[pair.GetName()] = pair.GetValue()
					require.NotContains(t, pair.GetValue(), tournamentID.String())
					require.NotContains(t, pair.GetValue(), record.id.String())
				}
				if labels["operation"] == "outbox" {
					values[labels["outcome"]] = metric.GetCounter().GetValue()
				}
			}
		case "tpm_arena_lag_seconds":
			for _, metric := range family.Metric {
				if len(metric.Label) == 1 && metric.Label[0].GetName() == "kind" &&
					metric.Label[0].GetValue() == "delivery" {
					lagCount = metric.GetHistogram().GetSampleCount()
				}
			}
		}
	}
	require.InDelta(t, 1, values["failure"], 0)
	require.InDelta(t, 1, values["success"], 0)
	require.Equal(t, uint64(1), lagCount)
}
