package telemetry_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	telemetryadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/telemetry"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	observabilitymocks "github.com/TakuyaYagam1/task-per-minute/internal/observability/mocks"
	tournamentparticipant "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/participant"
)

func TestTournamentParticipantObserverMapsPayloadFreeTerminalEvent(t *testing.T) {
	t.Parallel()

	commandID := uuid.MustParse("4d000000-0000-4000-8000-000000000001")
	tournamentID := uuid.MustParse("4d000000-0000-4000-8000-000000000002")
	gameID := uuid.MustParse("4d000000-0000-4000-8000-000000000003")
	capture := observabilitymocks.NewMockTournamentEventObserver(t)
	capture.EXPECT().ObserveTournamentEvent(t.Context(), observability.TournamentEvent{
		Event:         "tournament.participant.command",
		Outcome:       tournamentparticipant.ParticipantOutcomeSuccess,
		CorrelationID: commandID.String(),
		CommandID:     commandID.String(),
		TournamentID:  tournamentID.String(),
		EntityKind:    "game",
		EntityID:      gameID.String(),
		Stage:         "submission",
		Transition:    "submission",
		Duration:      25 * time.Millisecond,
		ReasonCode:    "completed",
		Revision:      7,
	}).Once()
	observer := telemetryadapter.NewTournamentParticipantObserver(capture)

	observer.ObserveTournamentParticipantOperation(t.Context(), tournamentparticipant.ParticipantOperationEvent{
		Operation:    tournamentparticipant.ParticipantOperationSubmission,
		CommandID:    commandID,
		TournamentID: tournamentID,
		EntityID:     gameID,
		Outcome:      tournamentparticipant.ParticipantOutcomeSuccess,
		ReasonCode:   "completed",
		Revision:     7,
		Duration:     25 * time.Millisecond,
	})
}

func TestTournamentParticipantObserverRejectsInvalidEvent(t *testing.T) {
	t.Parallel()

	capture := observabilitymocks.NewMockTournamentEventObserver(t)
	observer := telemetryadapter.NewTournamentParticipantObserver(capture)

	observer.ObserveTournamentParticipantOperation(t.Context(), tournamentparticipant.ParticipantOperationEvent{
		Operation:    tournamentparticipant.ParticipantOperationSubmission,
		CommandID:    uuid.New(),
		TournamentID: uuid.New(),
		EntityID:     uuid.New(),
		Outcome:      tournamentparticipant.ParticipantOutcomeSuccess,
		ReasonCode:   "private_flag_payload",
		Revision:     1,
	})

	capture.AssertNotCalled(t, "ObserveTournamentEvent", mock.Anything, mock.Anything)
}
