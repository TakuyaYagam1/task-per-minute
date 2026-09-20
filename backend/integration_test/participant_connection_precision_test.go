//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	participantrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/connection"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	gamereconnect "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
	connection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/connection"
)

// A reload can close the old pre-start lease after the Wave starts and before
// the replacement subscriber is opened. That close must commit the pause even
// when the server clock includes sub-microsecond precision.
func TestParticipantConnectionReloadClosesOldLeaseBeforeReplacement(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

	fixture := createParticipantReconnectSwissProofFixture(ctx, t)
	playerParticipantID := fixture.participants[0]
	var playerID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT player_id FROM participants WHERE id = $1`, playerParticipantID).Scan(&playerID))

	clock := &participantReconnectTestClock{at: time.Now().UTC().Truncate(time.Microsecond)}
	repository := participantrepo.NewParticipantConnectionPostgres(
		fixture.tx,
		participantReconnectAuthorityProvider{identity: fixture.executionAuthority},
	)
	coordinator, err := connection.NewCoordinator(connection.Dependencies{
		Transactions: fixture.tx,
		Authority:    repository,
		Repository:   repository,
		Disconnect:   gamereconnect.NewDisconnectUseCase(fixture.adapter, clock),
		Reconnect:    gamereconnect.ReconnectNewUseCase(fixture.adapter, clock),
		Clock:        clock,
		Config:       connection.Config{ReconnectDuration: 30 * time.Second},
	})
	require.NoError(t, err)

	oldCommand := inbound.TournamentParticipantConnectionCommand{
		TournamentID:         fixture.tournamentID,
		PlayerID:             playerID,
		ConnectionID:         uuid.New(),
		ConnectionGeneration: 1,
	}
	participantReconnectInsertReloadSubscriber(ctx, t, fixture.tournamentID, oldCommand)
	require.NoError(t, coordinator.Connect(ctx, oldCommand))

	startCommand := fixture.startCommand(ctx, t)
	var changed bool
	err = fixture.tx.Do(ctx, func(txCtx context.Context) error {
		_, changed, err = fixture.start.Start(txCtx, startCommand)
		return err
	})
	require.NoError(t, err)
	require.True(t, changed)

	scope := pause.GraphScope{
		TournamentID: fixture.tournamentID,
		RosterID:     fixture.rosterID,
		WaveID:       fixture.waveID,
		Authority:    fixture.executionAuthority,
	}
	started, err := fixture.adapter.LoadAuthority(ctx, scope, playerParticipantID)
	require.NoError(t, err)
	disconnectAt := started.GameClock.OriginalDeadline.Add(-10 * time.Second).Add(123 * time.Nanosecond)
	require.NotEqual(t, disconnectAt, disconnectAt.Truncate(time.Microsecond))
	clock.at = disconnectAt

	// This is the production ordering: the old pre-start lease is the last
	// active lease when its socket closes, so the close runs ActionGameDisconnect.
	require.NoError(t, coordinator.Disconnect(ctx, oldCommand))
	require.Equal(t, 0, participantReconnectActiveLeaseCount(ctx, t, fixture, playerParticipantID))
	paused, err := fixture.adapter.LoadAuthority(ctx, scope, playerParticipantID)
	require.NoError(t, err)
	require.Equal(t, domain.GameStatePaused, paused.Game.State)
	require.Len(t, paused.Reconnect, 1)

	replacement := inbound.TournamentParticipantConnectionCommand{
		TournamentID:         fixture.tournamentID,
		PlayerID:             playerID,
		ConnectionID:         uuid.New(),
		ConnectionGeneration: 1,
	}
	participantReconnectInsertReloadSubscriber(ctx, t, fixture.tournamentID, replacement)
	clock.at = disconnectAt.Truncate(time.Microsecond).Add(5 * time.Second)
	require.NoError(t, coordinator.Connect(ctx, replacement))
	require.Equal(t, 1, participantReconnectActiveLeaseCount(ctx, t, fixture, playerParticipantID))
	resumed, err := fixture.adapter.LoadAuthority(ctx, scope, playerParticipantID)
	require.NoError(t, err)
	require.Equal(t, domain.GameStateActive, resumed.Game.State)
}

func participantReconnectInsertReloadSubscriber(
	ctx context.Context,
	t *testing.T,
	tournamentID uuid.UUID,
	command inbound.TournamentParticipantConnectionCommand,
) {
	t.Helper()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO realtime_subscribers (
			id, instance_id, connection_id, connection_generation,
			tournament_id, role, principal_id, initial_sequence,
			last_acknowledged_sequence, snapshot_sequence, connected_at
		)
		VALUES ($1, $2, $3, $4, $5, 'participant', $6, 0, 0, 0, $7)`,
		uuid.New(), uuid.New(), command.ConnectionID, command.ConnectionGeneration,
		tournamentID, command.PlayerID, time.Now().UTC().Truncate(time.Microsecond))
	require.NoError(t, err)
}
