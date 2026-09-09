package telemetry

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	observabilitymocks "github.com/TakuyaYagam1/task-per-minute/internal/observability/mocks"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

func TestExecutionRecoveryObserverMapsPayloadFreeTerminalEvent(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.MustParse("3b000000-0000-4000-8000-000000000001")
	shared := observabilitymocks.NewMockTournamentEventObserver(t)
	shared.EXPECT().ObserveTournamentEvent(t.Context(), observability.TournamentEvent{
		Event:         executionRecoveryEvent,
		Outcome:       observability.TournamentOutcomeSuccess,
		CorrelationID: "execution-recovery-eefaadf31425f468be7d6b16",
		TournamentID:  tournamentID.String(),
		EntityKind:    executionRecoveryEntityKind,
		EntityID:      tournamentID.String(),
		Stage:         executionRecoveryStage,
		Transition:    "scan_completed",
		ReasonCode:    "deadline_rearmed",
		Revision:      5,
	}).Once()

	observer := NewExecutionRecoveryObserver(shared)
	require.NotNil(t, observer)
	observer.ObserveExecutionRecovery(t.Context(), gameusecase.RecoveryEvent{
		TournamentID: tournamentID,
		Outcome:      gameusecase.RecoveryOutcomeSuccess,
		Transition:   "scan_completed",
		ReasonCode:   "deadline_rearmed",
		Revision:     5,
	})
}

func TestExecutionRecoveryObserverDropsMalformedEvent(t *testing.T) {
	t.Parallel()

	shared := observabilitymocks.NewMockTournamentEventObserver(t)
	observer := NewExecutionRecoveryObserver(shared)
	require.NotNil(t, observer)

	observer.ObserveExecutionRecovery(t.Context(), gameusecase.RecoveryEvent{
		TournamentID: uuid.New(),
		Outcome:      gameusecase.RecoveryOutcomeSuccess,
		Transition:   "private_token",
		ReasonCode:   "deadline_rearmed",
		Revision:     1,
	})
}
