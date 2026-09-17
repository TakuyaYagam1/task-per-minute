package telemetry

import (
	"testing"
	"time"

	"github.com/google/uuid"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	observabilitymocks "github.com/TakuyaYagam1/task-per-minute/internal/observability/mocks"
	adminobservability "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/observability"
)

func TestTournamentAdminObserverMapsPayloadFreeCorrectionOutcome(t *testing.T) {
	t.Parallel()

	commandID := uuid.MustParse("3a000000-0000-4000-8000-000000000001")
	tournamentID := uuid.MustParse("3a000000-0000-4000-8000-000000000002")
	gameID := uuid.MustParse("3a000000-0000-4000-8000-000000000003")
	shared := observabilitymocks.NewMockTournamentEventObserver(t)
	shared.EXPECT().ObserveTournamentEvent(t.Context(), observability.TournamentEvent{
		Event: tournamentAdminCommandEvent, Outcome: observability.TournamentOutcomeSuccess,
		CorrelationID: commandID.String(), CommandID: commandID.String(),
		TournamentID: tournamentID.String(), EntityKind: "game", EntityID: gameID.String(),
		Stage: "correction", Transition: string(adminobservability.OperationResultCorrect),
		Duration: 10 * time.Millisecond, ReasonCode: "completed", Revision: 11,
	}).Once()

	NewTournamentAdminObserver(shared).ObserveTournamentAdminOperation(t.Context(), adminobservability.OperationEvent{
		Operation: adminobservability.OperationResultCorrect, CommandID: commandID,
		TournamentID: tournamentID, EntityID: gameID,
		Outcome: adminobservability.OperationOutcomeSuccess, ReasonCode: "completed",
		Revision: 11, Duration: 10 * time.Millisecond,
	})
}

func TestTournamentAdminObserverRejectsUnknownOrMalformedMetadata(t *testing.T) {
	t.Parallel()

	shared := observabilitymocks.NewMockTournamentEventObserver(t)
	observer := NewTournamentAdminObserver(shared)
	require.NotNil(t, observer)
	observer.ObserveTournamentAdminOperation(t.Context(), adminobservability.OperationEvent{
		Operation: "private_payload", CommandID: uuid.New(), TournamentID: uuid.New(), EntityID: uuid.New(),
		Outcome: adminobservability.OperationOutcomeFailure, ReasonCode: "operation_failed",
	})
	observer.ObserveTournamentAdminOperation(t.Context(), adminobservability.OperationEvent{
		Operation: adminobservability.OperationResultCorrect,
		Outcome:   adminobservability.OperationOutcomeFailure, ReasonCode: "operation_failed",
	})
}

func TestTournamentAdminObserverMapsBoundedOperationMetrics(t *testing.T) {
	t.Parallel()

	metrics := observability.NewTournamentMetrics()
	observer := NewTournamentAdminObserver(metrics)
	observer.ObserveTournamentAdminOperation(t.Context(), adminobservability.OperationEvent{
		Operation:    adminobservability.OperationPairingConfigure,
		CommandID:    uuid.MustParse("3a000000-0000-4000-8000-000000000011"),
		TournamentID: uuid.MustParse("3a000000-0000-4000-8000-000000000012"),
		EntityID:     uuid.MustParse("3a000000-0000-4000-8000-000000000013"),
		Outcome:      adminobservability.OperationOutcomeSuccess, ReasonCode: "completed", Revision: 4,
	})

	families, err := metrics.Gatherer().Gather()
	require.NoError(t, err)
	require.InDelta(t, 1, tournamentAdminMetricValue(t, families, "pairing", "success"), 0)
}

func tournamentAdminMetricValue(
	t *testing.T,
	families []*dto.MetricFamily,
	operation string,
	outcome string,
) float64 {
	t.Helper()
	for _, family := range families {
		if family.GetName() != "tpm_tournament_operations_total" {
			continue
		}
		for _, metric := range family.Metric {
			labels := make(map[string]string, len(metric.Label))
			for _, pair := range metric.Label {
				labels[pair.GetName()] = pair.GetValue()
			}
			if labels["operation"] == operation && labels["outcome"] == outcome {
				return metric.GetCounter().GetValue()
			}
		}
	}
	t.Fatalf("metric operation=%s outcome=%s not found", operation, outcome)
	return 0
}
