package bootstrap

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/config"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
	idempotencymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency/mocks"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	tournamentadminincident "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/incident"
	tournamentadminmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/mocks"
	tournamentparticipant "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/participant"
	tournamentparticipantmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/participant/mocks"
	progressionmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression/mocks"
)

func TestProvideDistributedCommandCoordinatorUsesSharedReceiptStore(t *testing.T) {
	t.Parallel()

	store := idempotencymocks.NewMockStore(t)
	coordinator := provideDistributedCommandCoordinator(store)
	receipt, err := idempotency.NewCommand("participant-ready", uuid.New(), [32]byte{1})
	require.NoError(t, err)
	store.EXPECT().Begin(mock.Anything, receipt, mock.Anything).
		Return(idempotency.BeginResult{Disposition: idempotency.BeginInFlight}, nil).Once()

	_, err = idempotency.Execute(context.Background(), coordinator, receipt, func(context.Context) (struct{}, error) {
		t.Fatal("in-flight receipt must not run mutation")
		return struct{}{}, nil
	})
	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestProvideIncidentAuthenticatorUsesDedicatedConfig(t *testing.T) {
	t.Parallel()

	authenticator, err := provideIncidentAuthenticator(&config.Config{
		Incident: config.Incident{
			HMACKeyID:  "incident-2026-09",
			HMACSecret: strings.Repeat("a", 32),
		},
	})
	require.NoError(t, err)

	var port tournamentadminincident.IncidentAuthenticator = authenticator
	require.NotNil(t, port)
}

func TestProvideTournamentAdminCorrectionProvidesCorrectionPort(t *testing.T) {
	t.Parallel()

	workflow := provideTournamentAdminCorrection(
		tournamentadminmocks.NewMockCorrectionTransactionManager(t),
		tournamentadminmocks.NewMockCorrectionWorkflowRepository(t),
	)

	var port tournamentadmin.CorrectionPort = workflow
	require.NotNil(t, port)
}

func TestProvideTournamentAdminReplayProvidesReserveAndReplayPorts(t *testing.T) {
	t.Parallel()

	workflow := provideTournamentAdminReplay(
		tournamentadminmocks.NewMockExecutionTransactionManager(t),
		tournamentadminmocks.NewMockReplayWorkflowRepository(t),
	)

	var reserve tournamentadmin.ReservePort = workflow
	var replay tournamentadmin.ReplayPort = workflow
	require.NotNil(t, reserve)
	require.NotNil(t, replay)
}

func TestProvideTournamentAdminLifecycleRequiresProgression(t *testing.T) {
	t.Parallel()

	workflow := provideTournamentAdminLifecycle(
		tournamentadminmocks.NewMockLifecycleTransactionManager(t),
		tournamentadminmocks.NewMockLifecycleWorkflowRepository(t),
		tournamentadminmocks.NewMockLifecycleTransitioner(t),
		tournamentadminmocks.NewMockLifecyclePauser(t),
		tournamentadminmocks.NewMockLifecycleCanceller(t),
		progressionmocks.NewMockProgressionService(t),
		tournamentadminmocks.NewMockAdminLifecycleClock(t),
	)

	var port tournamentadmin.LifecyclePort = workflow
	require.NotNil(t, port)
}

func TestProvideTournamentAdminResultsRequiresPostseason(t *testing.T) {
	t.Parallel()

	workflow := provideTournamentAdminResults(
		tournamentadminmocks.NewMockOperatorResultTransactionManager(t),
		tournamentadminmocks.NewMockOperatorResultWorkflowRepository(t),
		tournamentadminmocks.NewMockAdminPostseasonWorkflow(t),
	)

	var noShow tournamentadmin.NoShowPort = workflow
	var forfeit tournamentadmin.ForfeitPort = workflow
	require.NotNil(t, noShow)
	require.NotNil(t, forfeit)
}

func TestProvideParticipantCommandsRequiresSettlementAndPostseason(t *testing.T) {
	t.Parallel()

	coordinator := provideParticipantCommands(
		tournamentparticipantmocks.NewMockParticipantTransactionManager(t),
		tournamentparticipantmocks.NewMockCommandAuthority(t),
		nil,
		nil,
		tournamentparticipantmocks.NewMockSubmissionWorkflow(t),
		tournamentparticipantmocks.NewMockSettlementWorkflow(t),
		nil,
		nil,
		tournamentparticipantmocks.NewMockParticipantPostseasonWorkflow(t),
	)

	var executor tournamentparticipant.CommandExecutor = coordinator
	require.NotNil(t, executor)
}
