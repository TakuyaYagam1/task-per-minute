package admin_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	tournamentadminmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/mocks"
)

func TestObservedServiceEmitsOneRosterTerminalEvent(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	command := tournamentadmin.ReplaceRosterCommand{CommandScope: observedCommandScope()}
	view := tournamentadmin.RosterView{
		ID: command.TournamentID, TournamentID: command.TournamentID, Revision: 7,
	}
	next := tournamentadminmocks.NewMockAdminService(t)
	next.EXPECT().ReplaceRoster(t.Context(), command).Return(view, nil).Once()
	clock := tournamentadminmocks.NewMockOperationClock(t)
	clock.EXPECT().Now().Return(startedAt).Once()
	clock.EXPECT().Now().Return(startedAt.Add(25 * time.Millisecond)).Once()
	observer := tournamentadminmocks.NewMockOperationObserver(t)
	observer.EXPECT().ObserveTournamentAdminOperation(t.Context(), tournamentadmin.OperationEvent{
		Operation: tournamentadmin.OperationRosterReplace, CommandID: command.CommandID,
		TournamentID: command.TournamentID, EntityID: command.TournamentID,
		Outcome:    tournamentadmin.OperationOutcomeSuccess,
		ReasonCode: "completed", Revision: 7, Duration: 25 * time.Millisecond,
	}).Once()
	service := tournamentadmin.AdminNewObservedService(next, clock, observer)

	actual, err := service.ReplaceRoster(t.Context(), command)

	require.NoError(t, err)
	require.Equal(t, view, actual)
}

func TestObservedServiceMapsConflictWithoutLeakingCommandReason(t *testing.T) {
	t.Parallel()

	command := tournamentadmin.ReplayCommand{
		CommandScope:              observedCommandScope(),
		ReplacementGameID:         uuid.MustParse("39000000-0000-4000-8000-000000000004"),
		Reason:                    "private operator explanation",
		ExpectedAuthorityRevision: 9,
	}
	next := tournamentadminmocks.NewMockAdminService(t)
	next.EXPECT().ReplayGame(t.Context(), command).Return(domain.ErrConflict).Once()
	observer := tournamentadminmocks.NewMockOperationObserver(t)
	observer.EXPECT().ObserveTournamentAdminOperation(t.Context(), tournamentadmin.OperationEvent{
		Operation: tournamentadmin.OperationGameReplay, CommandID: command.CommandID,
		TournamentID: command.TournamentID, EntityID: command.ReplacementGameID,
		Outcome:    tournamentadmin.OperationOutcomeRejected,
		ReasonCode: "stale_revision", Revision: 9,
	}).Once()
	service := tournamentadmin.AdminNewObservedService(next, nil, observer)

	err := service.ReplayGame(t.Context(), command)

	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestObservedServiceIsolatesObserverPanic(t *testing.T) {
	t.Parallel()

	command := tournamentadmin.ReserveCommand{
		CommandScope: observedCommandScope(),
		AssignmentID: uuid.MustParse("39000000-0000-4000-8000-000000000005"),
	}
	next := tournamentadminmocks.NewMockAdminService(t)
	next.EXPECT().AssignReserve(t.Context(), command).Return(nil).Once()
	observer := tournamentadminmocks.NewMockOperationObserver(t)
	observer.EXPECT().ObserveTournamentAdminOperation(mock.Anything, mock.Anything).
		Run(func(context.Context, tournamentadmin.OperationEvent) { panic("observer failed") }).Once()
	service := tournamentadmin.AdminNewObservedService(next, nil, observer)

	require.NotPanics(t, func() {
		require.NoError(t, service.AssignReserve(t.Context(), command))
	})
}

func TestObservedServiceEmitsOneLifecycleTerminalEventAfterCommit(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC)
	command := tournamentadmin.TournamentActionCommand{
		CommandScope: observedCommandScope(),
		Action:       tournamentadmin.TournamentActionStartSwiss,
	}
	view := inbound.TournamentView{ID: command.TournamentID, Revision: 8}
	next := tournamentadminmocks.NewMockAdminService(t)
	next.EXPECT().ApplyTournamentAction(t.Context(), command).Return(view, nil).Once()
	clock := tournamentadminmocks.NewMockOperationClock(t)
	clock.EXPECT().Now().Return(startedAt).Once()
	clock.EXPECT().Now().Return(startedAt.Add(25 * time.Millisecond)).Once()
	observer := tournamentadminmocks.NewMockOperationObserver(t)
	observer.EXPECT().ObserveTournamentAdminOperation(t.Context(), tournamentadmin.OperationEvent{
		Operation:    tournamentadmin.Operation("tournament_action"),
		CommandID:    command.CommandID,
		TournamentID: command.TournamentID,
		EntityID:     command.TournamentID,
		Outcome:      tournamentadmin.OperationOutcomeSuccess,
		ReasonCode:   "completed",
		Revision:     8,
		Duration:     25 * time.Millisecond,
	}).Once()
	service := tournamentadmin.AdminNewObservedService(next, clock, observer)

	actual, err := service.ApplyTournamentAction(t.Context(), command)

	require.NoError(t, err)
	require.Equal(t, view, actual)
}

func observedCommandScope() tournamentadmin.CommandScope {
	return tournamentadmin.CommandScope{
		Operator: tournamentadmin.OperatorIdentity{
			ActorID: uuid.MustParse("39000000-0000-4000-8000-000000000001"),
		},
		TournamentID: uuid.MustParse("39000000-0000-4000-8000-000000000002"),
		CommandID:    uuid.MustParse("39000000-0000-4000-8000-000000000003"),
	}
}
