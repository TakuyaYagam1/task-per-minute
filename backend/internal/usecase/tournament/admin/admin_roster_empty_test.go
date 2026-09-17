package admin

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	operationusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
	rosterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
)

func TestAdminUseCaseGetRosterPreservesEmptyRoster(t *testing.T) {
	t.Parallel()

	tournamentID := rosterCloneTestID(1)
	emptyRoster := rosterusecase.RosterView{
		ID:           rosterCloneTestID(2),
		TournamentID: tournamentID,
		Revision:     1,
		Participants: make([]rosterusecase.RosterParticipantView, 0),
		CreatedAt:    rosterCloneTestTime(),
		UpdatedAt:    rosterCloneTestTime(),
	}
	workflow := rosterusecase.NewRosterWorkflow(rosterusecase.RosterWorkflowDependencies{
		Transactions: rosterTransactionManagerMock{},
		Repository:   &rosterWorkflowRepositoryMock{view: emptyRoster},
	})
	application := AdminNewUseCase(AdminDependencies{Roster: workflow})

	actual, err := application.GetRoster(context.Background(), rosterusecase.RosterQuery{
		Operator:     operationusecase.OperatorIdentity{ActorID: rosterCloneTestID(3)},
		TournamentID: tournamentID,
	})

	require.NoError(t, err)
	require.NotNil(t, actual.Participants)
	require.Empty(t, actual.Participants)
}

func TestCloneRosterView(t *testing.T) {
	t.Parallel()

	t.Run("nil participants remain nil", func(t *testing.T) {
		t.Parallel()

		cloned := rosterusecase.CloneRosterView(rosterusecase.RosterView{})

		require.Nil(t, cloned.Participants)
	})

	t.Run("empty participants remain non-nil", func(t *testing.T) {
		t.Parallel()

		cloned := rosterusecase.CloneRosterView(rosterusecase.RosterView{Participants: make([]rosterusecase.RosterParticipantView, 0)})

		require.NotNil(t, cloned.Participants)
		require.Empty(t, cloned.Participants)
	})

	t.Run("populated participants are independent", func(t *testing.T) {
		t.Parallel()

		view := rosterusecase.RosterView{Participants: []rosterusecase.RosterParticipantView{{Seed: 1}}}
		cloned := rosterusecase.CloneRosterView(view)

		require.NotSame(t, &view.Participants[0], &cloned.Participants[0])
		cloned.Participants[0].Seed = 2
		require.Equal(t, 1, view.Participants[0].Seed)
	})
}

type rosterTransactionManagerMock struct{}

func (rosterTransactionManagerMock) Do(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type rosterWorkflowRepositoryMock struct {
	rosterusecase.RosterWorkflowRepository

	view rosterusecase.RosterView
}

func (m *rosterWorkflowRepositoryMock) GetRoster(context.Context, uuid.UUID) (rosterusecase.RosterView, error) {
	return m.view, nil
}

func rosterCloneTestID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", value))
}

func rosterCloneTestTime() time.Time {
	return time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
}
