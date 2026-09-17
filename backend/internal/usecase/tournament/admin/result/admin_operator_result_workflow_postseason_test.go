package result_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/result"
	tournamentadminmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/result/mocks"
)

func TestOperatorResultWorkflowRejectsMissingPostseasonBeforeTransaction(t *testing.T) {
	t.Parallel()

	command, _ := operatorNoShowFixture(t, time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC))
	transactions := tournamentadminmocks.NewMockOperatorResultTransactionManager(t)
	transactions.EXPECT().Do(mock.Anything, mock.Anything).Return(nil).Maybe()
	workflow := tournamentadmin.NewOperatorResultWorkflow(tournamentadmin.OperatorResultWorkflowDependencies{
		Transactions: transactions,
		Repository:   tournamentadminmocks.NewMockOperatorResultWorkflowRepository(t),
	})

	err := workflow.ResolveNoShow(context.Background(), command)

	require.ErrorIs(t, err, domain.ErrInternal)
}
