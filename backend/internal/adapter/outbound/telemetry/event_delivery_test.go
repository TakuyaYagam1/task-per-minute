package telemetry

import (
	"testing"
	"time"

	"github.com/google/uuid"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	observabilitymocks "github.com/TakuyaYagam1/task-per-minute/internal/observability/mocks"
	delivery "github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery"
)

func TestEventDeliveryObserverMapsPayloadFreeOutcome(t *testing.T) {
	t.Parallel()

	eventID := uuid.MustParse("38000000-0000-4000-8000-000000000001")
	correlationID := uuid.MustParse("38000000-0000-4000-8000-000000000003")
	tournamentID := uuid.MustParse("38000000-0000-4000-8000-000000000002")
	observer := observabilitymocks.NewMockTournamentEventObserver(t)
	observer.EXPECT().ObserveTournamentEvent(mock.Anything, observability.TournamentEvent{
		Event: tournamentEventDeliveryEvent, Outcome: observability.TournamentOutcomeSuccess,
		CorrelationID: correlationID.String(), TournamentID: tournamentID.String(),
		EntityKind: tournamentEventDeliveryEntityKind, EntityID: eventID.String(),
		Stage: tournamentEventDeliveryStage, Transition: "acknowledged",
		Duration: 25 * time.Millisecond, ReasonCode: "delivered", Revision: 7,
	}).Once()

	NewEventDeliveryObserver(observer).ObserveEventDelivery(t.Context(), delivery.WorkerEvent{
		EventID: eventID, CorrelationID: correlationID, TournamentID: tournamentID, ProjectionRevision: 7, Sequence: 11,
		Outcome: delivery.WorkerOutcomeSuccess, Transition: "acknowledged",
		ReasonCode: "delivered", Duration: 25 * time.Millisecond,
	})
}

func TestEventDeliveryObserverRejectsSensitiveOrMalformedMetadata(t *testing.T) {
	t.Parallel()

	observer := observabilitymocks.NewMockTournamentEventObserver(t)
	NewEventDeliveryObserver(observer).ObserveEventDelivery(t.Context(), delivery.WorkerEvent{
		EventID: uuid.New(), TournamentID: uuid.New(), ProjectionRevision: 1, Sequence: 1,
		Outcome: delivery.WorkerOutcomeFailure, Transition: "token leaked",
		ReasonCode: "repository_error",
	})
}

func TestEventDeliveryObserverRejectsNilCorrelation(t *testing.T) {
	t.Parallel()

	observer := observabilitymocks.NewMockTournamentEventObserver(t)
	NewEventDeliveryObserver(observer).ObserveEventDelivery(t.Context(), delivery.WorkerEvent{
		EventID:            uuid.MustParse("38000000-0000-4000-8000-000000000021"),
		TournamentID:       uuid.MustParse("38000000-0000-4000-8000-000000000022"),
		ProjectionRevision: 1,
		Sequence:           1,
		Outcome:            delivery.WorkerOutcomeSuccess,
		Transition:         "acknowledged",
		ReasonCode:         "delivered",
		Duration:           25 * time.Millisecond,
	})
}

func TestEventDeliveryObserverRejectsNilIdentity(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name  string
		event delivery.WorkerEvent
	}{
		{
			name: "event",
			event: delivery.WorkerEvent{
				CorrelationID: uuid.MustParse("38000000-0000-4000-8000-000000000031"),
				TournamentID:  uuid.MustParse("38000000-0000-4000-8000-000000000032"),
			},
		},
		{
			name: "tournament",
			event: delivery.WorkerEvent{
				EventID:       uuid.MustParse("38000000-0000-4000-8000-000000000041"),
				CorrelationID: uuid.MustParse("38000000-0000-4000-8000-000000000043"),
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			observer := observabilitymocks.NewMockTournamentEventObserver(t)
			event := testCase.event
			event.ProjectionRevision = 1
			event.Sequence = 1
			event.Outcome = delivery.WorkerOutcomeSuccess
			event.Transition = "acknowledged"
			event.ReasonCode = "delivered"
			event.Duration = 25 * time.Millisecond
			NewEventDeliveryObserver(observer).ObserveEventDelivery(t.Context(), event)
		})
	}
}

func TestEventDeliveryObserverRecordsBoundedDeliveryLag(t *testing.T) {
	metrics := observability.NewTournamentMetrics()
	adapter := NewEventDeliveryObserver(metrics)
	require.NotNil(t, adapter)

	adapter.ObserveEventDelivery(t.Context(), delivery.WorkerEvent{
		EventID:            uuid.MustParse("38000000-0000-4000-8000-000000000011"),
		CorrelationID:      uuid.MustParse("38000000-0000-4000-8000-000000000013"),
		TournamentID:       uuid.MustParse("38000000-0000-4000-8000-000000000012"),
		ProjectionRevision: 1,
		Sequence:           1,
		Outcome:            delivery.WorkerOutcomeSuccess,
		Transition:         "acknowledged",
		ReasonCode:         "delivered",
		Duration:           25 * time.Millisecond,
	})

	require.Equal(t, uint64(1), eventDeliveryLagCount(t, metrics, "delivery"))
}

func eventDeliveryLagCount(t *testing.T, metrics *observability.TournamentMetrics, kind string) uint64 {
	t.Helper()
	families, err := metrics.Gatherer().Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != "tpm_tournament_lag_seconds" {
			continue
		}
		for _, metric := range family.Metric {
			if eventDeliveryMetricLabel(metric, "kind") == kind {
				return metric.GetHistogram().GetSampleCount()
			}
		}
	}
	t.Fatalf("delivery lag metric for %q not found", kind)
	return 0
}

func eventDeliveryMetricLabel(metric *dto.Metric, name string) string {
	for _, label := range metric.Label {
		if label.GetName() == name {
			return label.GetValue()
		}
	}
	return ""
}
