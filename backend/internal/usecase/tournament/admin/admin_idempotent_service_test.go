package admin_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inboundmocks "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound/mocks"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
	idempotencymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency/mocks"
	idempotentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/idempotent"
	tournamentadminmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/inbound/mocks"
	rosterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
)

func TestIdempotentServiceRejectsInFlightAdminCommandBeforeDispatch(t *testing.T) {
	t.Parallel()

	command := idempotentReplaceRosterCommand()
	store := idempotencymocks.NewMockStore(t)
	store.EXPECT().Begin(mock.Anything, mock.Anything, mock.Anything).
		Return(idempotency.BeginResult{Disposition: idempotency.BeginInFlight}, nil).Once()
	next := tournamentadminmocks.NewMockAdminService(t)
	service, err := idempotentusecase.NewIdempotentService(
		next, inboundmocks.NewMockTournamentUseCase(t), idempotency.NewCoordinator(store),
	)
	require.NoError(t, err)

	_, err = service.ReplaceRoster(t.Context(), command)

	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestIdempotentServiceReplaysCompletedAdminCommandThroughNextService(t *testing.T) {
	t.Parallel()

	command := idempotentReplaceRosterCommand()
	view := rosterusecase.RosterView{ID: command.TournamentID, TournamentID: command.TournamentID, Revision: 7}
	store := idempotencymocks.NewMockStore(t)
	store.EXPECT().Begin(mock.Anything, mock.Anything, mock.Anything).
		Return(idempotency.BeginResult{Disposition: idempotency.BeginSucceeded}, nil).Once()
	next := tournamentadminmocks.NewMockAdminService(t)
	next.EXPECT().ReplaceRoster(mock.Anything, command).Return(view, nil).Once()
	service, err := idempotentusecase.NewIdempotentService(
		next, inboundmocks.NewMockTournamentUseCase(t), idempotency.NewCoordinator(store),
	)
	require.NoError(t, err)

	actual, err := service.ReplaceRoster(context.Background(), command)

	require.NoError(t, err)
	require.Equal(t, view, actual)
}

func idempotentReplaceRosterCommand() rosterusecase.ReplaceRosterCommand {
	operatorID := uuid.MustParse("71000000-0000-4000-8000-000000000001")
	tournamentID := uuid.MustParse("71000000-0000-4000-8000-000000000002")
	return rosterusecase.NewReplaceRosterCommand(
		rosterusecase.OperatorIdentity{ActorID: operatorID},
		tournamentID,
		uuid.MustParse("71000000-0000-4000-8000-000000000003"),
		4,
		[]rosterusecase.RosterParticipantInput{
			{PlayerID: uuid.MustParse("71000000-0000-4000-8000-000000000011"), Seed: 1, Attendance: domain.AttendanceStateCheckedIn},
			{PlayerID: uuid.MustParse("71000000-0000-4000-8000-000000000012"), Seed: 2, Attendance: domain.AttendanceStateCheckedIn},
			{PlayerID: uuid.MustParse("71000000-0000-4000-8000-000000000013"), Seed: 3, Attendance: domain.AttendanceStateCheckedIn},
			{PlayerID: uuid.MustParse("71000000-0000-4000-8000-000000000014"), Seed: 4, Attendance: domain.AttendanceStateCheckedIn},
		},
	)
}
