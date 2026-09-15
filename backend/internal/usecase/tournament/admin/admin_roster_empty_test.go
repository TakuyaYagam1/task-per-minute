package admin

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAdminUseCaseGetRosterPreservesEmptyRoster(t *testing.T) {
	t.Parallel()

	tournamentID := rosterCloneTestID(1)
	emptyRoster := RosterView{
		ID:           rosterCloneTestID(2),
		TournamentID: tournamentID,
		Revision:     1,
		Participants: make([]RosterParticipantView, 0),
		CreatedAt:    rosterCloneTestTime(),
		UpdatedAt:    rosterCloneTestTime(),
	}
	workflow := NewRosterWorkflow(RosterWorkflowDependencies{
		Transactions: rosterTransactionManagerMock{},
		Repository:   &rosterWorkflowRepositoryMock{view: emptyRoster},
	})
	application := AdminNewUseCase(AdminDependencies{Roster: workflow})

	actual, err := application.GetRoster(context.Background(), RosterQuery{
		Operator:     OperatorIdentity{ActorID: rosterCloneTestID(3)},
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

		cloned := cloneRosterView(RosterView{})

		require.Nil(t, cloned.Participants)
	})

	t.Run("empty participants remain non-nil", func(t *testing.T) {
		t.Parallel()

		cloned := cloneRosterView(RosterView{Participants: make([]RosterParticipantView, 0)})

		require.NotNil(t, cloned.Participants)
		require.Empty(t, cloned.Participants)
	})

	t.Run("populated participants are independent", func(t *testing.T) {
		t.Parallel()

		view := RosterView{Participants: []RosterParticipantView{{Seed: 1}}}
		cloned := cloneRosterView(view)

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
	RosterWorkflowRepository

	view RosterView
}

func (m *rosterWorkflowRepositoryMock) GetRoster(context.Context, uuid.UUID) (RosterView, error) {
	return m.view, nil
}

func rosterCloneTestID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", value))
}

func rosterCloneTestTime() time.Time {
	return time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
}
