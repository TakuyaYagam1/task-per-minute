package participant_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	inboundmocks "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound/mocks"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
	idempotencymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency/mocks"
	tournamentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/participant"
)

func TestIdempotentServiceRejectsInFlightParticipantCommandBeforeDispatch(t *testing.T) {
	t.Parallel()

	command := idempotentReadyCommand()
	store := idempotencymocks.NewMockStore(t)
	store.EXPECT().Begin(mock.Anything, mock.Anything, mock.Anything).
		Return(idempotency.BeginResult{Disposition: idempotency.BeginInFlight}, nil).Once()
	next := inboundmocks.NewMockTournamentParticipantUseCase(t)
	service := tournamentusecase.ParticipantNewIdempotentService(next, idempotency.NewCoordinator(store))

	_, err := service.SetReady(t.Context(), command)

	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestIdempotentServiceReplaysCompletedParticipantCommandThroughNextService(t *testing.T) {
	t.Parallel()

	command := idempotentReadyCommand()
	event := usecase.ReadinessEvent{CommandID: command.CommandID}
	store := idempotencymocks.NewMockStore(t)
	store.EXPECT().Begin(mock.Anything, mock.Anything, mock.Anything).
		Return(idempotency.BeginResult{Disposition: idempotency.BeginSucceeded}, nil).Once()
	next := inboundmocks.NewMockTournamentParticipantUseCase(t)
	next.EXPECT().SetReady(mock.Anything, command).Return(event, nil).Once()
	service := tournamentusecase.ParticipantNewIdempotentService(next, idempotency.NewCoordinator(store))

	actual, err := service.SetReady(context.Background(), command)

	require.NoError(t, err)
	require.Equal(t, event, actual)
}

func idempotentReadyCommand() usecase.ReadyCommand {
	return usecase.ReadyCommand{
		Actor:                      usecase.Identity{PlayerID: uuid.MustParse("72000000-0000-4000-8000-000000000001")},
		TournamentID:               uuid.MustParse("72000000-0000-4000-8000-000000000002"),
		WaveID:                     uuid.MustParse("72000000-0000-4000-8000-000000000003"),
		CommandID:                  uuid.MustParse("72000000-0000-4000-8000-000000000004"),
		ExpectedProjectionRevision: 1,
		Ready:                      true,
	}
}
