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

// A participant can open a socket before the Wave starts and reload it after
// the Wave starts. The old socket must still close its own durable lease; the
// close must not be rolled back when the current game binding differs from the
// pre-start lease binding.
func TestParticipantConnectionCloseAfterWaveStartReload(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

	fixture := createParticipantReconnectSwissProofFixture(ctx, t)
	playerParticipantID := fixture.participants[0]
	var playerID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT player_id FROM participants WHERE id = $1`, playerParticipantID).Scan(&playerID))

	clock := &participantReconnectTestClock{at: time.Now().UTC().Add(-time.Second)}
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

	connectionID := uuid.New()
	command := inbound.TournamentParticipantConnectionCommand{
		TournamentID:         fixture.tournamentID,
		PlayerID:             playerID,
		ConnectionID:         connectionID,
		ConnectionGeneration: 1,
	}
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO realtime_subscribers (
			id, instance_id, connection_id, connection_generation,
			tournament_id, role, principal_id, initial_sequence,
			last_acknowledged_sequence, snapshot_sequence, connected_at
		)
		VALUES ($1, $2, $3, 1, $4, 'participant', $5, 0, 0, 0, $6)`,
		uuid.New(), uuid.New(), connectionID, fixture.tournamentID, playerID, time.Now().UTC().Truncate(time.Microsecond))
	require.NoError(t, err)
	require.NoError(t, coordinator.Connect(ctx, command))

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
	deadline := started.GameClock.OriginalDeadline
	pauseRemaining := deadline.Sub(time.Now().UTC()).Truncate(time.Millisecond) + time.Millisecond
	require.Greater(t, pauseRemaining, time.Duration(0), "disconnect pause must occur before the game deadline")
	clock.at = deadline.Add(-pauseRemaining)
	closeCommand := inbound.TournamentParticipantConnectionCommand{
		TournamentID:         fixture.tournamentID,
		PlayerID:             playerID,
		ConnectionID:         connectionID,
		ConnectionGeneration: 1,
	}
	require.NoError(t, coordinator.Disconnect(ctx, closeCommand))
	require.Equal(t, 0, participantReconnectActiveLeaseCount(ctx, t, fixture, playerParticipantID))
	authority, err := fixture.adapter.LoadAuthority(ctx, scope, playerParticipantID)
	require.NoError(t, err)
	require.Equal(t, domain.GameStatePaused, authority.Game.State)
	require.Len(t, authority.Reconnect, 1)
	require.Equal(t, pause.ReconnectStateOpen, authority.Reconnect[0].State)

	// A hard reload opens the replacement only after the browser has closed
	// the old socket. The replacement must consume the persisted pause and
	// reconnect the same participant through a fresh durable lease.
	connectionID2 := uuid.New()
	command2 := inbound.TournamentParticipantConnectionCommand{
		TournamentID:         fixture.tournamentID,
		PlayerID:             playerID,
		ConnectionID:         connectionID2,
		ConnectionGeneration: 1,
	}
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO realtime_subscribers (
			id, instance_id, connection_id, connection_generation,
			tournament_id, role, principal_id, initial_sequence,
			last_acknowledged_sequence, snapshot_sequence, connected_at
		)
		VALUES ($1, $2, $3, 1, $4, 'participant', $5, 0, 0, 0, $6)`,
		uuid.New(), uuid.New(), connectionID2, fixture.tournamentID, playerID, time.Now().UTC().Truncate(time.Microsecond))
	require.NoError(t, err)
	clock.at = clock.at.Add(5 * time.Second)
	require.NoError(t, coordinator.Connect(ctx, command2))
	require.Equal(t, 1, participantReconnectActiveLeaseCount(ctx, t, fixture, playerParticipantID))
	authority, err = fixture.adapter.LoadAuthority(ctx, scope, playerParticipantID)
	require.NoError(t, err)
	require.Equal(t, domain.GameStateActive, authority.Game.State)
	require.Empty(t, authority.Reconnect)
}

// A participant may keep a readiness tab open while the active-game tab is
// opened. Closing the readiness tab must not pause the game; closing the last
// active-game lease must create the reconnect interval that a replacement tab
// consumes.
func TestParticipantConnectionReloadWithReadinessLeaseReconnects(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

	fixture := createParticipantReconnectSwissProofFixture(ctx, t)
	playerParticipantID := fixture.participants[0]
	playerID := participantReconnectPlayerAccount(ctx, t, playerParticipantID)
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

	readiness := participantReconnectConnectionCommand(
		participantReconnectStartedFixture{fixture: fixture, playerAccountID: playerID}, uuid.New(),
	)
	participantReconnectInsertReloadSubscriber(ctx, t, fixture.tournamentID, readiness)
	require.NoError(t, coordinator.Connect(ctx, readiness))
	require.Equal(t, 1, participantReconnectActiveLeaseCount(ctx, t, fixture, playerParticipantID))

	startCommand := fixture.startCommand(ctx, t)
	var changed bool
	err = fixture.tx.Do(ctx, func(txCtx context.Context) error {
		_, changed, err = fixture.start.Start(txCtx, startCommand)
		return err
	})
	require.NoError(t, err)
	require.True(t, changed)

	active := participantReconnectConnectionCommand(
		participantReconnectStartedFixture{fixture: fixture, playerAccountID: playerID}, uuid.New(),
	)
	participantReconnectInsertReloadSubscriber(ctx, t, fixture.tournamentID, active)
	require.NoError(t, coordinator.Connect(ctx, active))
	require.Equal(t, 2, participantReconnectActiveLeaseCount(ctx, t, fixture, playerParticipantID))

	// The readiness lease is no longer bound to the game, but it is still an
	// active lease. Its close must not run the game disconnect action.
	require.NoError(t, coordinator.Disconnect(ctx, readiness))
	require.Equal(t, 1, participantReconnectActiveLeaseCount(ctx, t, fixture, playerParticipantID))

	scope := pause.GraphScope{
		TournamentID: fixture.tournamentID,
		RosterID:     fixture.rosterID,
		WaveID:       fixture.waveID,
		Authority:    fixture.executionAuthority,
	}
	started, err := fixture.adapter.LoadAuthority(ctx, scope, playerParticipantID)
	require.NoError(t, err)
	require.Equal(t, domain.GameStateActive, started.Game.State)
	// Leave a sub-millisecond residual in the frozen duration. The domain
	// receipt retains it, while pause_clocks persists only milliseconds.
	disconnectAt := started.GameClock.OriginalDeadline.Add(-10 * time.Second).Add(466 * time.Microsecond)
	clock.at = disconnectAt

	// Closing the active-game lease is now the last-lease transition and must
	// freeze the game with one open reconnect interval.
	require.NoError(t, coordinator.Disconnect(ctx, active))
	require.Equal(t, 0, participantReconnectActiveLeaseCount(ctx, t, fixture, playerParticipantID))
	paused, err := fixture.adapter.LoadAuthority(ctx, scope, playerParticipantID)
	require.NoError(t, err)
	require.Equal(t, domain.GameStatePaused, paused.Game.State)
	require.Len(t, paused.Reconnect, 1)
	require.Equal(t, pause.ReconnectStateOpen, paused.Reconnect[0].State)

	replacement := participantReconnectConnectionCommand(
		participantReconnectStartedFixture{fixture: fixture, playerAccountID: playerID}, uuid.New(),
	)
	participantReconnectInsertReloadSubscriber(ctx, t, fixture.tournamentID, replacement)
	clock.at = disconnectAt.Add(5 * time.Second)
	require.NoError(t, coordinator.Connect(ctx, replacement))
	require.Equal(t, 1, participantReconnectActiveLeaseCount(ctx, t, fixture, playerParticipantID))
	resumed, err := fixture.adapter.LoadAuthority(ctx, scope, playerParticipantID)
	require.NoError(t, err)
	require.Equal(t, domain.GameStateActive, resumed.Game.State)
	require.Empty(t, resumed.Reconnect)
	require.NotNil(t, resumed.GameClock.ResumedDeadline)

	var durableResumedAt, durableResumedDeadline time.Time
	var frozenRemainingMs int64
	var receiptResumedDeadline string
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT clock.resumed_at, clock.resumed_deadline, clock.frozen_remaining_ms,
		       receipt.record_document #>> '{record,ReconnectAuthority,GameClock,ResumedDeadline}'
		FROM pause_clocks AS clock
		JOIN pauses AS pause ON pause.id = clock.pause_id
		JOIN reconnect_command_receipts AS receipt
		  ON receipt.tournament_id = $1
		 AND receipt.roster_id = $2
		 AND receipt.wave_id = $3
		 AND receipt.participant_id = $4
		 AND receipt.mutation_kind = 'reconnect'
		 AND receipt.record_document #>> '{record,ReconnectAuthority,GameClock,ResumedDeadline}' IS NOT NULL
		WHERE pause.game_attempt_id = $5
		  AND clock.resumed_at IS NOT NULL
		ORDER BY clock.resumed_at DESC, clock.pause_id DESC
		LIMIT 1`,
		fixture.tournamentID, fixture.rosterID, fixture.waveID, playerParticipantID, started.Game.ID,
	).Scan(&durableResumedAt, &durableResumedDeadline, &frozenRemainingMs, &receiptResumedDeadline))
	durableRemaining := time.Duration(frozenRemainingMs) * time.Millisecond
	require.Equal(t, paused.GameClock.Remaining.Milliseconds(), frozenRemainingMs)
	require.Equal(t, durableResumedAt.UTC().Add(durableRemaining), durableResumedDeadline.UTC())
	require.NotEqual(t, durableResumedDeadline.UTC(), resumed.GameClock.ResumedDeadline.UTC())
	require.Equal(t, resumed.GameClock.ResumedDeadline.UTC(), timeMustParseRFC3339(t, receiptResumedDeadline))
	require.Equal(t, paused.GameClock.Remaining, resumed.GameClock.Remaining)

	// A resumed game may disconnect again. The next reconnect pause must retain
	// the consumed root slot instead of losing it when the old pause is closed.
	secondDisconnectAt := resumed.GameClock.ResumedDeadline.Add(-5 * time.Second)
	clock.at = secondDisconnectAt
	require.NoError(t, coordinator.Disconnect(ctx, replacement))
	require.Equal(t, 0, participantReconnectActiveLeaseCount(ctx, t, fixture, playerParticipantID))
	pausedAgain, err := fixture.adapter.LoadAuthority(ctx, scope, playerParticipantID)
	require.NoError(t, err)
	require.Equal(t, domain.GameStatePaused, pausedAgain.Game.State)
	require.Len(t, pausedAgain.Reconnect, 1)
	require.Equal(t, 2, reconnectCounterUsed(pausedAgain.Counters, playerParticipantID))
	require.Equal(t, 2, pausedAgain.Reconnect[0].Number)

	replacementAgain := participantReconnectConnectionCommand(
		participantReconnectStartedFixture{fixture: fixture, playerAccountID: playerID}, uuid.New(),
	)
	participantReconnectInsertReloadSubscriber(ctx, t, fixture.tournamentID, replacementAgain)
	clock.at = secondDisconnectAt.Add(5 * time.Second)
	require.NoError(t, coordinator.Connect(ctx, replacementAgain))
	require.Equal(t, 1, participantReconnectActiveLeaseCount(ctx, t, fixture, playerParticipantID))
	resumedAgain, err := fixture.adapter.LoadAuthority(ctx, scope, playerParticipantID)
	require.NoError(t, err)
	require.Equal(t, domain.GameStateActive, resumedAgain.Game.State)
	require.Empty(t, resumedAgain.Reconnect)

	var oldRoots, oldNumber, oldSlots int
	var oldRootState, oldPauseState string
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*), MIN(intervals.interval_number), MIN(intervals.state),
		       counters.slots_used, pause.state
		FROM reconnect_intervals AS intervals
		JOIN reconnect_slot_counters AS counters
		  ON counters.pause_id = intervals.pause_id
		 AND counters.participant_id = intervals.participant_id
		JOIN pauses AS pause ON pause.id = intervals.pause_id
		WHERE intervals.pause_id = $1
		  AND intervals.participant_id = $2
		  AND intervals.continuation_number = 0
		GROUP BY counters.slots_used, pause.state`,
		paused.PauseID, playerParticipantID,
	).Scan(&oldRoots, &oldNumber, &oldRootState, &oldSlots, &oldPauseState))
	require.Equal(t, 1, oldRoots)
	require.Equal(t, 1, oldNumber)
	require.Equal(t, string(pause.ReconnectStateReconnected), oldRootState)
	require.Equal(t, 1, oldSlots)
	require.Equal(t, "resumed", oldPauseState)

	var durableRoots, durableNumber, durableSlots int
	var durableRootState, durablePauseState string
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*), MIN(intervals.interval_number), MIN(intervals.state),
		       counters.slots_used, pause.state
		FROM reconnect_intervals AS intervals
		JOIN reconnect_slot_counters AS counters
		  ON counters.pause_id = intervals.pause_id
		 AND counters.participant_id = intervals.participant_id
		JOIN pauses AS pause ON pause.id = intervals.pause_id
		WHERE intervals.pause_id = $1
		  AND intervals.participant_id = $2
		  AND intervals.continuation_number = 0
		GROUP BY counters.slots_used, pause.state`,
		pausedAgain.PauseID, playerParticipantID,
	).Scan(&durableRoots, &durableNumber, &durableRootState, &durableSlots, &durablePauseState))
	require.Equal(t, 1, durableRoots)
	require.Equal(t, 2, durableNumber)
	require.Equal(t, string(pause.ReconnectStateReconnected), durableRootState)
	require.Equal(t, 2, durableSlots)
	require.Equal(t, "resumed", durablePauseState)
}

func reconnectCounterUsed(counters []pause.PauseReconnectCounter, participantID uuid.UUID) int {
	for _, counter := range counters {
		if counter.ParticipantID == participantID {
			return counter.Used
		}
	}
	return -1
}

func timeMustParseRFC3339(t *testing.T, value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	require.NoError(t, err)
	return parsed
}
