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
	tournamentadmincorrection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/correction"
	correctionmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/correction/mocks"
	tournamentadminincident "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/incident"
	tournamentadminlifecycle "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/lifecycle"
	tournamentadminlifecyclemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/lifecycle/mocks"
	tournamentadminmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/mocks"
	tournamentadminreplay "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/replay"
	tournamentadminreplaymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/replay/mocks"
	tournamentadminresult "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/result"
	tournamentadminresultmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/result/mocks"
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
		correctionmocks.NewMockCorrectionTransactionManager(t),
		correctionmocks.NewMockCorrectionWorkflowRepository(t),
	)

	var port tournamentadmincorrection.CorrectionPort = workflow
	require.NotNil(t, port)
}

func TestProvideTournamentAdminReplayProvidesReserveAndReplayPorts(t *testing.T) {
	t.Parallel()

	workflow := provideTournamentAdminReplay(
		tournamentadminmocks.NewMockExecutionTransactionManager(t),
		tournamentadminreplaymocks.NewMockReplayWorkflowRepository(t),
	)

	var reserve tournamentadminreplay.ReservePort = workflow
	var replay tournamentadminreplay.ReplayPort = workflow
	require.NotNil(t, reserve)
	require.NotNil(t, replay)
}

func TestProvideTournamentAdminLifecycleRequiresProgression(t *testing.T) {
	t.Parallel()

	workflow := provideTournamentAdminLifecycle(
		tournamentadminlifecyclemocks.NewMockLifecycleTransactionManager(t),
		tournamentadminlifecyclemocks.NewMockLifecycleWorkflowRepository(t),
		tournamentadminlifecyclemocks.NewMockLifecycleTransitioner(t),
		tournamentadminlifecyclemocks.NewMockLifecyclePauser(t),
		tournamentadminlifecyclemocks.NewMockLifecycleCanceller(t),
		progressionmocks.NewMockProgressionService(t),
		tournamentadminlifecyclemocks.NewMockAdminLifecycleClock(t),
	)

	var port tournamentadminlifecycle.LifecyclePort = workflow
	require.NotNil(t, port)
}

func TestProvideTournamentAdminResultsRequiresPostseason(t *testing.T) {
	t.Parallel()

	workflow := provideTournamentAdminResults(
		tournamentadminresultmocks.NewMockOperatorResultTransactionManager(t),
		tournamentadminresultmocks.NewMockOperatorResultWorkflowRepository(t),
		tournamentadminresultmocks.NewMockPostseasonWorkflow(t),
	)

	var noShow tournamentadminresult.NoShowPort = workflow
	var forfeit tournamentadminresult.ForfeitPort = workflow
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
