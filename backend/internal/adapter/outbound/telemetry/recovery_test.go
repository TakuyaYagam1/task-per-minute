package telemetry

import (
	"testing"

	"github.com/google/uuid"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	observabilitymocks "github.com/TakuyaYagam1/task-per-minute/internal/observability/mocks"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

func TestRecoveryObserverMapsEvent(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.MustParse("78000000-0000-0000-0000-000000000001")
	observer := observabilitymocks.NewMockTournamentEventObserver(t)
	observer.EXPECT().ObserveTournamentEvent(t.Context(), observability.TournamentEvent{
		Event:         "tournament.recovery",
		Outcome:       observability.TournamentOutcomeSuccess,
		CorrelationID: "recovery-aabbccddeeff",
		TournamentID:  tournamentID.String(),
		EntityKind:    "tournament",
		EntityID:      tournamentID.String(),
		Stage:         "startup_recovery",
		Transition:    "rearm_succeeded",
		ReasonCode:    "work_rearmed",
		Revision:      7,
	}).Once()

	adapter := NewRecoveryObserver(observer)
	require.NotNil(t, adapter)
	adapter.ObserveRecovery(t.Context(), recovery.RecoveryEvent{
		Outcome:       recovery.RecoveryOutcomeSuccess,
		CorrelationID: "recovery-aabbccddeeff",
		TournamentID:  tournamentID,
		Stage:         recovery.RecoveryStageStartup,
		Transition:    "rearm_succeeded",
		ReasonCode:    "work_rearmed",
		Revision:      7,
	})
}

func TestRecoveryObserverPreservesDeadlineStage(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.MustParse("78000000-0000-0000-0000-000000000002")
	observer := observabilitymocks.NewMockTournamentEventObserver(t)
	observer.EXPECT().ObserveTournamentEvent(t.Context(), observability.TournamentEvent{
		Event: "tournament.recovery", Outcome: observability.TournamentOutcomeRejected,
		CorrelationID: "recovery-deadline", TournamentID: tournamentID.String(),
		EntityKind: "tournament", EntityID: tournamentID.String(), Stage: "deadline",
		Transition: "deadline_already_current", ReasonCode: "nothing_to_rearm", Revision: 8,
	}).Once()

	NewRecoveryObserver(observer).ObserveRecovery(t.Context(), recovery.RecoveryEvent{
		Outcome: recovery.RecoveryOutcomeRejected, CorrelationID: "recovery-deadline",
		TournamentID: tournamentID, Stage: recovery.RecoveryStageDeadline,
		Transition: "deadline_already_current", ReasonCode: "nothing_to_rearm", Revision: 8,
	})
}

func TestNewRecoveryObserverRejectsNilObserver(t *testing.T) {
	t.Parallel()

	require.Nil(t, NewRecoveryObserver(nil))
	var observer *observabilitymocks.MockTournamentEventObserver
	require.Nil(t, NewRecoveryObserver(observer))
}

func TestRecoveryObserverRecordsBoundedMetrics(t *testing.T) {
	t.Parallel()

	metrics := observability.NewTournamentMetrics()
	adapter := NewRecoveryObserver(metrics)
	require.NotNil(t, adapter)
	tournamentID := uuid.MustParse("78000000-0000-0000-0000-000000000011")
	adapter.ObserveRecovery(t.Context(), recovery.RecoveryEvent{
		Outcome: recovery.RecoveryOutcomeSuccess, CorrelationID: "recovery-success",
		TournamentID: tournamentID, Stage: recovery.RecoveryStageStartup, Transition: "rearm_succeeded",
		ReasonCode: "work_rearmed", Revision: 1,
	})
	adapter.ObserveRecovery(t.Context(), recovery.RecoveryEvent{
		Outcome: recovery.RecoveryOutcomeRejected, CorrelationID: "recovery-rejected",
		TournamentID: tournamentID, Stage: recovery.RecoveryStageStartup, Transition: "fail_closed",
		ReasonCode: "missing_lease", Revision: 1,
	})

	require.InDelta(t, 1, recoveryMetricValue(t, metrics, "success"), 0)
	require.InDelta(t, 1, recoveryMetricValue(t, metrics, "rejected"), 0)
}

func recoveryMetricValue(
	t *testing.T,
	metrics *observability.TournamentMetrics,
	outcome string,
) float64 {
	t.Helper()
	families, err := metrics.Gatherer().Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != "tpm_tournament_operations_total" {
			continue
		}
		for _, metric := range family.Metric {
			if recoveryMetricLabels(metric, outcome) {
				return metric.GetCounter().GetValue()
			}
		}
	}
	t.Fatalf("recovery metric with outcome %s not found", outcome)
	return 0
}

func recoveryMetricLabels(metric *dto.Metric, outcome string) bool {
	labels := make(map[string]string, len(metric.Label))
	for _, pair := range metric.Label {
		labels[pair.GetName()] = pair.GetValue()
	}
	return len(labels) == 2 && labels["operation"] == "recovery" &&
		labels["outcome"] == outcome
}
