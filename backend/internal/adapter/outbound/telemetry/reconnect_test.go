package telemetry

import (
	"testing"
	"time"

	"github.com/google/uuid"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	observabilitymocks "github.com/TakuyaYagam1/task-per-minute/internal/observability/mocks"
	gamereconnect "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
)

func TestReconnectObserverMapsEvent(t *testing.T) {
	t.Parallel()

	commandID := uuid.MustParse("75000000-0000-0000-0000-000000000001")
	tournamentID := uuid.MustParse("75000000-0000-0000-0000-000000000002")
	participantID := uuid.MustParse("75000000-0000-0000-0000-000000000003")
	duration := 5 * time.Second
	observer := observabilitymocks.NewMockTournamentEventObserver(t)
	observer.EXPECT().ObserveTournamentEvent(t.Context(), observability.TournamentEvent{
		Event:         "tournament.command.reconnect",
		Outcome:       observability.TournamentOutcomeSuccess,
		CorrelationID: commandID.String(),
		CommandID:     commandID.String(),
		TournamentID:  tournamentID.String(),
		EntityKind:    "participant",
		EntityID:      participantID.String(),
		Stage:         "reconnect",
		Transition:    "reconnect",
		Duration:      duration,
		ReasonCode:    "committed",
		Revision:      7,
	}).Once()

	adapter := NewReconnectObserver(observer)
	require.NotNil(t, adapter)
	adapter.Observe(t.Context(), gamereconnect.ReconnectEvent{
		ReconnectEvent: "tournament.command.reconnect", Outcome: gamereconnect.OutcomeSuccess,
		CommandID: commandID, TournamentID: tournamentID, ParticipantID: participantID,
		Stage: "reconnect", Transition: "reconnect", Duration: duration,
		ReasonCode: "committed", Revision: 7,
	})
}

func TestNewReconnectObserverRejectsNilObserver(t *testing.T) {
	t.Parallel()

	require.Nil(t, NewReconnectObserver(nil))
	var observer *observabilitymocks.MockTournamentEventObserver
	require.Nil(t, NewReconnectObserver(observer))
}

func TestReconnectObserverRecordsMetrics(t *testing.T) {
	t.Parallel()

	metrics := observability.NewTournamentMetrics()
	adapter := NewReconnectObserver(metrics)
	require.NotNil(t, adapter)
	commandID := uuid.MustParse("75000000-0000-0000-0000-000000000011")
	tournamentID := uuid.MustParse("75000000-0000-0000-0000-000000000012")
	participantID := uuid.MustParse("75000000-0000-0000-0000-000000000013")
	adapter.Observe(t.Context(), gamereconnect.ReconnectEvent{
		ReconnectEvent: "tournament.command.reconnect", Outcome: gamereconnect.OutcomeSuccess,
		CommandID: commandID, TournamentID: tournamentID, ParticipantID: participantID,
		Stage: "reconnect", Transition: "reconnect", ReasonCode: "committed", Revision: 1,
	})
	adapter.Observe(t.Context(), gamereconnect.ReconnectEvent{
		ReconnectEvent: "tournament.command.reconnect_timeout", Outcome: gamereconnect.OutcomeSuccess,
		CommandID:    uuid.MustParse("75000000-0000-0000-0000-000000000014"),
		TournamentID: tournamentID, ParticipantID: participantID,
		Stage: "deadline", Transition: "expire_reconnect", ReasonCode: "committed", Revision: 2,
		HasDeadlineLag: true, DeadlineLag: 500 * time.Millisecond,
	})

	require.InDelta(t, 1, reconnectMetricValue(t, metrics, "reconnect", "success"), 0)
	require.InDelta(t, 1, reconnectMetricValue(t, metrics, "deadline", "success"), 0)
	require.Equal(t, uint64(1), reconnectLagCount(t, metrics, "deadline"))
}

func reconnectMetricValue(
	t *testing.T,
	metrics *observability.TournamentMetrics,
	operation string,
	outcome string,
) float64 {
	t.Helper()
	metric := reconnectMetric(t, metrics, "tpm_tournament_operations_total", map[string]string{
		"operation": operation,
		"outcome":   outcome,
	})
	return metric.GetCounter().GetValue()
}

func reconnectLagCount(
	t *testing.T,
	metrics *observability.TournamentMetrics,
	kind string,
) uint64 {
	t.Helper()
	metric := reconnectMetric(t, metrics, "tpm_tournament_lag_seconds", map[string]string{
		"kind": kind,
	})
	return metric.GetHistogram().GetSampleCount()
}

func reconnectMetric(
	t *testing.T,
	metrics *observability.TournamentMetrics,
	name string,
	want map[string]string,
) *dto.Metric {
	t.Helper()
	families, err := metrics.Gatherer().Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			labels := make(map[string]string, len(metric.Label))
			for _, pair := range metric.Label {
				labels[pair.GetName()] = pair.GetValue()
			}
			if reconnectLabelsEqual(labels, want) {
				return metric
			}
		}
	}
	t.Fatalf("metric %s with labels %v not found", name, want)
	return nil
}

func reconnectLabelsEqual(left, right map[string]string) bool {
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
