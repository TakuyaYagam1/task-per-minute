package participant_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	inboundmocks "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound/mocks"
	tournamentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/participant"
	tournamentmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/participant/mocks"
)

func TestObservedServiceEmitsOneSubmissionTerminalEvent(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, time.September, 7, 18, 0, 0, 0, time.UTC)
	command := usecase.SubmissionCommand{
		Actor:                      usecase.Identity{PlayerID: uuid.New()},
		TournamentID:               uuid.New(),
		SeriesID:                   uuid.New(),
		GameID:                     uuid.New(),
		CommandID:                  uuid.New(),
		ExpectedProjectionRevision: 3,
	}
	result := usecase.SubmissionResult{ProjectionRevision: 4}
	next := inboundmocks.NewMockTournamentParticipantUseCase(t)
	next.EXPECT().SubmitFlag(t.Context(), command).Return(result, nil).Once()
	clock := tournamentmocks.NewMockParticipantOperationClock(t)
	clock.EXPECT().Now().Return(startedAt).Once()
	clock.EXPECT().Now().Return(startedAt.Add(25 * time.Millisecond)).Once()
	observer := tournamentmocks.NewMockParticipantOperationObserver(t)
	observer.EXPECT().ObserveTournamentParticipantOperation(t.Context(), tournamentusecase.ParticipantOperationEvent{
		Operation:    tournamentusecase.ParticipantOperationSubmission,
		CommandID:    command.CommandID,
		TournamentID: command.TournamentID,
		EntityID:     command.GameID,
		Outcome:      tournamentusecase.ParticipantOutcomeSuccess,
		ReasonCode:   "completed",
		Revision:     result.ProjectionRevision,
		Duration:     25 * time.Millisecond,
	}).Once()
	service := tournamentusecase.ParticipantNewObservedService(next, clock, observer)

	actual, err := service.SubmitFlag(t.Context(), command)

	require.NoError(t, err)
	require.Equal(t, result, actual)
}

func TestObservedServiceLeavesParticipantReadsUnobserved(t *testing.T) {
	t.Parallel()

	query := usecase.LobbyQuery{
		Actor: usecase.Identity{PlayerID: uuid.New()}, TournamentID: uuid.New(),
	}
	view := usecase.LobbyView{TournamentID: query.TournamentID}
	next := inboundmocks.NewMockTournamentParticipantUseCase(t)
	next.EXPECT().GetLobby(t.Context(), query).Return(view, nil).Once()
	observer := tournamentmocks.NewMockParticipantOperationObserver(t)
	service := tournamentusecase.ParticipantNewObservedService(next, nil, observer)

	actual, err := service.GetLobby(t.Context(), query)

	require.NoError(t, err)
	require.Equal(t, view, actual)
}

func TestObservedServiceIsolatesParticipantObserverPanic(t *testing.T) {
	t.Parallel()

	command := usecase.SurrenderCommand{
		Actor:                      usecase.Identity{PlayerID: uuid.New()},
		TournamentID:               uuid.New(),
		SeriesID:                   uuid.New(),
		CommandID:                  uuid.New(),
		ExpectedProjectionRevision: 4,
		Confirmed:                  true,
	}
	next := inboundmocks.NewMockTournamentParticipantUseCase(t)
	next.EXPECT().Surrender(t.Context(), command).Return(usecase.OfficialResultView{}, nil).Once()
	observer := tournamentmocks.NewMockParticipantOperationObserver(t)
	observer.EXPECT().ObserveTournamentParticipantOperation(mock.Anything, mock.Anything).
		Run(func(context.Context, tournamentusecase.ParticipantOperationEvent) { panic("observer failed") }).Once()
	service := tournamentusecase.ParticipantNewObservedService(next, nil, observer)

	require.NotPanics(t, func() {
		_, err := service.Surrender(t.Context(), command)
		require.NoError(t, err)
	})
}
