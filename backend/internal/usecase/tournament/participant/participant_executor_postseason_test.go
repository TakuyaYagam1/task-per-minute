package participant_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	tournamentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/participant"
	tournamentmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/participant/mocks"
)

func TestCommandCoordinatorRejectsDraftActionWithoutPostseasonBeforeTransaction(t *testing.T) {
	t.Parallel()

	transactions := tournamentmocks.NewMockParticipantTransactionManager(t)
	transactions.EXPECT().Do(mock.Anything, mock.Anything).Return(nil).Maybe()
	coordinator := tournamentusecase.NewCommandCoordinator(tournamentusecase.CommandCoordinatorDependencies{
		Transactions: transactions,
		Authority:    tournamentmocks.NewMockCommandAuthority(t),
		Draft:        draftusecase.NewActionUseCase(nil, nil),
	})

	_, err := coordinator.SubmitDraftAction(context.Background(), usecase.DraftActionCommand{
		Actor:                      usecase.Identity{PlayerID: uuid.New()},
		TournamentID:               uuid.New(),
		SeriesID:                   uuid.New(),
		CommandID:                  uuid.New(),
		ExpectedProjectionRevision: 1,
		ExpectedDraftRevision:      1,
		ExpectedTurn:               1,
		Action:                     domain.DraftActionPick,
		Category:                   domain.CategoryWeb,
	})

	require.ErrorIs(t, err, domain.ErrInternal)
}
