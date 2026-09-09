package websocket

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
	tournamentsnapshot "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	tournamentsnapshotmocks "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound/mocks"
)

func TestTournamentPublicFlowReleasesCapacityOnConnectionCancellation(t *testing.T) {
	t.Parallel()
	tournamentID := tournamentSourceID(101)
	reader := tournamentsnapshotmocks.NewMockTournamentSnapshotUseCase(t)
	reader.EXPECT().PublicSnapshot(
		mock.Anything,
		tournamentsnapshot.PublicSnapshotQuery{TournamentID: tournamentID},
	).Return(tournamentSourcePublicView(tournamentID), nil)
	source, err := NewTournamentProductionSnapshotSource(reader)
	require.NoError(t, err)
	flow, err := NewTournamentPublicFlow(source, &tournamentws.PublicRealtimeConfig{MaxConnections: 1})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	_, err = flow.OpenTournamentPublic(ctx, TournamentPublicConnectionRequest{TournamentID: tournamentID})
	require.NoError(t, err)
	_, err = flow.OpenTournamentPublic(context.Background(), TournamentPublicConnectionRequest{TournamentID: tournamentID})
	require.ErrorIs(t, err, tournamentws.ErrPublicRealtimeConnectionLimit)

	cancel()
	require.Eventually(t, func() bool {
		retryCtx, retryCancel := context.WithCancel(context.Background())
		defer retryCancel()
		_, retryErr := flow.OpenTournamentPublic(retryCtx, TournamentPublicConnectionRequest{TournamentID: tournamentID})
		return retryErr == nil
	}, time.Second, time.Millisecond)
}
