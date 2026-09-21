package inbound_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	contract "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/inbound"
	tournamentadminmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/inbound/mocks"
	adminoperation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
)

func TestInboundAdapterMapsReplayRevisionConflict(t *testing.T) {
	t.Parallel()

	service := tournamentadminmocks.NewMockAdminService(t)
	expected := &adminoperation.RevisionConflictError{
		ExpectedRevision: 3,
		CurrentRevision:  4,
	}
	service.EXPECT().ReplayGame(mock.Anything, mock.Anything).Return(expected).Once()

	err := inbound.NewInboundAdapter(service).ReplayGame(context.Background(), contract.AdminReplayCommand{})

	var mapped *contract.AdminRevisionConflictError
	require.ErrorAs(t, err, &mapped)
	require.Equal(t, int64(3), mapped.ExpectedRevision)
	require.Equal(t, int64(4), mapped.CurrentRevision)
}
