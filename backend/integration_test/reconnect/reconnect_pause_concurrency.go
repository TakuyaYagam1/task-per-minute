//go:build integration

package reconnect

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/stretchr/testify/require"
)

func testReconnectContinuationPauseConcurrency(t *testing.T) {
	ctx := context.Background()

	t.Run("rejects a covered root backdated across an active normal Wave pause", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixtureWithSlotLimit(ctx, t, 1)
		participantID := fixture.draft.participantIDs[0]
		waveID := createCoveringWave(ctx, t, fixture, fixture.pausedAt)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)
		pauseTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		require.NoError(t, insertNormalWavePauseEvidence(
			ctx,
			pauseTx,
			fixture,
			waveID,
			uuid.New(),
			uuid.New(),
			suspendedAt,
		))
		require.NoError(t, pauseTx.Commit(ctx))

		openedAt := suspendedAt.Add(-10 * time.Second)
		intervalTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = intervalTx.Rollback(ctx) }()
		_, err = intervalTx.Exec(ctx, `
			SELECT 1 FROM pauses WHERE id = $1 FOR UPDATE`, fixture.gamePauseID)
		require.NoError(t, err)
		_, err = intervalTx.Exec(
			ctx, `
			UPDATE presence_states
			SET state = 'disconnected',
				presence_epoch = presence_epoch + 1,
				revision = revision + 1,
				disconnected_at = $3,
				updated_at = $3
			WHERE series_id = $1 AND participant_id = $2`,
			fixture.draft.seriesID,
			participantID,
			openedAt,
		)
		require.NoError(t, err)
		_, err = intervalTx.Exec(
			ctx, `
			INSERT INTO reconnect_intervals (
				id, pause_id, roster_id, series_id, game_attempt_id,
				participant_id, presence_epoch, interval_number,
				opened_at, deadline_at, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, 2, 1, $7, $8, $7, $7)`,
			uuid.New(),
			fixture.gamePauseID,
			fixture.draft.rosterID,
			fixture.draft.seriesID,
			fixture.attemptID,
			participantID,
			openedAt,
			suspendedAt.Add(time.Minute),
		)
		if err == nil {
			_, err = intervalTx.Exec(
				ctx, `
				UPDATE reconnect_slot_counters
				SET slots_used = slots_used + 1,
					revision = revision + 1,
					updated_at = $3
				WHERE pause_id = $1 AND participant_id = $2`,
				fixture.gamePauseID,
				participantID,
				openedAt,
			)
		}
		if err == nil {
			err = intervalTx.Commit(ctx)
		}
		requireReconnectExactCheckViolation(
			t,
			err,
			"reconnect interval cannot backdate across normal Wave pause history",
		)
	})

	t.Run("rejects a covered root backdated across a resolved normal Wave pause", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixtureWithSlotLimit(ctx, t, 1)
		waveID := createCoveringWave(ctx, t, fixture, fixture.pausedAt)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)
		pauseID := uuid.New()
		pauseTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		require.NoError(t, insertNormalWavePauseEvidence(
			ctx,
			pauseTx,
			fixture,
			waveID,
			pauseID,
			uuid.New(),
			suspendedAt,
		))
		require.NoError(t, pauseTx.Commit(ctx))
		resumeNormalWavePause(ctx, t, pauseID, suspendedAt.Add(time.Second))

		err = attemptReconnectRoot(
			ctx,
			fixture,
			fixture.draft.participantIDs[0],
			suspendedAt.Add(-10*time.Second),
			suspendedAt.Add(time.Minute),
			nil,
		)
		requireReconnectExactCheckViolation(
			t,
			err,
			"reconnect interval cannot backdate across normal Wave pause history",
		)
	})

	t.Run("serializes a normal Wave pause against a concurrent backdated root", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixtureWithSlotLimit(ctx, t, 1)
		waveID := createCoveringWave(ctx, t, fixture, fixture.pausedAt)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)
		pauseTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = pauseTx.Rollback(ctx) }()
		require.NoError(t, insertNormalWavePauseEvidence(
			ctx,
			pauseTx,
			fixture,
			waveID,
			uuid.New(),
			uuid.New(),
			suspendedAt,
		))

		started := make(chan int)
		intervalResult := make(chan error, 1)
		go func() {
			intervalResult <- attemptReconnectRoot(
				ctx,
				fixture,
				fixture.draft.participantIDs[0],
				suspendedAt.Add(-10*time.Second),
				suspendedAt.Add(time.Minute),
				started,
			)
		}()

		rootPID := <-started
		waitReconnectBackendLock(ctx, t, rootPID)
		require.NoError(t, pauseTx.Commit(ctx))
		requireReconnectExactCheckViolation(
			t,
			<-intervalResult,
			"reconnect interval cannot backdate across normal Wave pause history",
		)
	})

	t.Run("preserves a Game-first root writer during normal Wave entry", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixtureWithSlotLimit(ctx, t, 1)
		participantID := fixture.draft.participantIDs[0]
		waveID := createCoveringWave(ctx, t, fixture, fixture.pausedAt)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)

		rootTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = rootTx.Rollback(ctx) }()
		require.NoError(t, setReconnectDeadlockTimeouts(ctx, rootTx))
		_, err = rootTx.Exec(ctx, `
			SELECT 1 FROM pauses WHERE id = $1 FOR UPDATE`, fixture.gamePauseID)
		require.NoError(t, err)

		pauseTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = pauseTx.Rollback(ctx) }()
		require.NoError(t, setReconnectDeadlockTimeouts(ctx, pauseTx))
		var pausePID int
		require.NoError(t, pauseTx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pausePID))
		pauseResult := make(chan error, 1)
		go func() {
			pauseResult <- insertNormalWavePauseEvidence(
				ctx,
				pauseTx,
				fixture,
				waveID,
				uuid.New(),
				uuid.New(),
				suspendedAt,
			)
		}()
		waitReconnectBackendLock(ctx, t, pausePID)

		rootID := uuid.New()
		openedAt := suspendedAt.Add(-10 * time.Second)
		require.NoError(t, insertReconnectRootEvidence(
			ctx,
			rootTx,
			fixture,
			participantID,
			rootID,
			openedAt,
			suspendedAt.Add(time.Minute),
		))
		require.NoError(t, rootTx.Commit(ctx))
		require.NoError(t, <-pauseResult)
		requireReconnectExactCheckViolation(
			t,
			pauseTx.Commit(ctx),
			"normal Wave pause requires atomic reconnect suspension",
		)

		root := loadReconnectIntervalSnapshot(ctx, t, rootID)
		require.Equal(t, "open", root.state)
	})

	t.Run("holds the roster fence against concurrent pause topology insertion", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixtureWithSlotLimit(ctx, t, 1)
		waveID := createCoveringWave(ctx, t, fixture, fixture.pausedAt)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)
		pauseTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = pauseTx.Rollback(ctx) }()
		require.NoError(t, insertNormalWavePauseEvidence(
			ctx,
			pauseTx,
			fixture,
			waveID,
			uuid.New(),
			uuid.New(),
			suspendedAt,
		))

		topologyTx := beginReconnectLockProbe(ctx, t)
		defer func() { _ = topologyTx.Rollback(ctx) }()
		_, err = topologyTx.Exec(
			ctx, `
			INSERT INTO pauses (
				id, tournament_id, roster_id, scope_kind, scope_id,
				depth, reason, paused_from_state, current_revision_id,
				started_at, created_at, updated_at
			)
			VALUES (
				$1, $2, $3, 'tournament', $2,
				0, 'operator', 'active', $4,
				$5, $5, $5
			)`,
			uuid.New(),
			fixture.draft.tournamentID,
			fixture.draft.rosterID,
			uuid.New(),
			suspendedAt,
		)
		requireReconnectPostgresError(t, err, "55P03", "", "lock timeout")
	})
}
