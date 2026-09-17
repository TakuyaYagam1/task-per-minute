//go:build integration

package reconnect

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/stretchr/testify/require"
)

func TestReconnectMigrationCASLocks(t *testing.T) {
	ctx := context.Background()

	t.Run("pause snapshot locks live presence", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixture(ctx, t)
		participantID := fixture.draft.participantIDs[0]
		lockTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = lockTx.Rollback(ctx) }()
		_, err = lockTx.Exec(ctx, `
			SELECT 1
			FROM presence_states
			WHERE series_id = $1 AND participant_id = $2
			FOR NO KEY UPDATE`, fixture.draft.seriesID, participantID)
		require.NoError(t, err)

		probeTx := beginReconnectLockProbe(ctx, t)
		defer func() { _ = probeTx.Rollback(ctx) }()
		pauseID := uuid.New()
		pausedAt := fixture.pausedAt.Add(10 * time.Second)
		_, err = probeTx.Exec(ctx, `
			INSERT INTO pauses (
				id, tournament_id, roster_id, scope_kind, scope_id,
				reason, paused_from_state, current_revision_id,
				started_at, created_at, updated_at
			)
			VALUES (
				$1, $2, $3, 'tournament', $2,
				'operator', 'active', $4,
				$5, $5, $5
			)`,
			pauseID,
			fixture.draft.tournamentID,
			fixture.draft.rosterID,
			uuid.New(),
			pausedAt,
		)
		require.NoError(t, err)
		_, err = probeTx.Exec(ctx, `
			INSERT INTO pause_presence_snapshots (
				pause_id, roster_id, series_id, participant_id,
				presence_state, presence_epoch, presence_revision,
				captured_at, created_at
			)
			VALUES ($1, $2, $3, $4, 'connected', 1, 1, $5, $5)`,
			pauseID,
			fixture.draft.rosterID,
			fixture.draft.seriesID,
			participantID,
			pausedAt,
		)
		require.ErrorContains(t, err, "lock timeout")
	})

	t.Run("interval terminal transition locks live presence", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixture(ctx, t)
		participantID := fixture.draft.participantIDs[0]
		intervalID, deadline := disconnectParticipant(
			ctx, t,
			fixture,
			participantID,
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)

		lockTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = lockTx.Rollback(ctx) }()
		_, err = lockTx.Exec(ctx, `
			SELECT 1
			FROM presence_states
			WHERE series_id = $1 AND participant_id = $2
			FOR NO KEY UPDATE`, fixture.draft.seriesID, participantID)
		require.NoError(t, err)

		probeTx := beginReconnectLockProbe(ctx, t)
		defer func() { _ = probeTx.Rollback(ctx) }()
		_, err = probeTx.Exec(ctx, `
			UPDATE reconnect_intervals
			SET state = 'expired',
				closed_at = $2,
				revision = revision + 1,
				updated_at = $2
			WHERE id = $1`, intervalID, deadline)
		require.ErrorContains(t, err, "lock timeout")
	})

	t.Run("interval creation locks owning pause", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixture(ctx, t)
		participantID := fixture.draft.participantIDs[0]
		disconnectedAt := fixture.pausedAt.Add(time.Second)
		_, err := sharedPool.Exec(ctx, `
			UPDATE presence_states
			SET state = 'disconnected',
				presence_epoch = presence_epoch + 1,
				revision = revision + 1,
				disconnected_at = $3,
				updated_at = $3
			WHERE series_id = $1 AND participant_id = $2`,
			fixture.draft.seriesID,
			participantID,
			disconnectedAt,
		)
		require.NoError(t, err)

		lockTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = lockTx.Rollback(ctx) }()
		_, err = lockTx.Exec(ctx, `
			SELECT 1
			FROM pauses
			WHERE id = $1
			FOR NO KEY UPDATE`, fixture.gamePauseID)
		require.NoError(t, err)

		probeTx := beginReconnectLockProbe(ctx, t)
		defer func() { _ = probeTx.Rollback(ctx) }()
		_, err = probeTx.Exec(ctx, `
			INSERT INTO reconnect_intervals (
				id, pause_id, roster_id, series_id, game_attempt_id,
				participant_id, presence_epoch, interval_number,
				opened_at, deadline_at, created_at, updated_at
			)
			VALUES (
				$1, $2, $3, $4, $5,
				$6, 2, 1,
				$7, $8, $7, $7
			)`,
			uuid.New(),
			fixture.gamePauseID,
			fixture.draft.rosterID,
			fixture.draft.seriesID,
			fixture.attemptID,
			participantID,
			disconnectedAt,
			disconnectedAt.Add(2*time.Minute),
		)
		require.ErrorContains(t, err, "lock timeout")
	})

	t.Run("interval creation locks live presence", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixture(ctx, t)
		participantID := fixture.draft.participantIDs[0]
		disconnectedAt := fixture.pausedAt.Add(time.Second)
		_, err := sharedPool.Exec(ctx, `
			UPDATE presence_states
			SET state = 'disconnected',
				presence_epoch = presence_epoch + 1,
				revision = revision + 1,
				disconnected_at = $3,
				updated_at = $3
			WHERE series_id = $1 AND participant_id = $2`,
			fixture.draft.seriesID,
			participantID,
			disconnectedAt,
		)
		require.NoError(t, err)

		lockTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = lockTx.Rollback(ctx) }()
		_, err = lockTx.Exec(ctx, `
			SELECT 1
			FROM presence_states
			WHERE series_id = $1 AND participant_id = $2
			FOR NO KEY UPDATE`, fixture.draft.seriesID, participantID)
		require.NoError(t, err)

		probeTx := beginReconnectLockProbe(ctx, t)
		defer func() { _ = probeTx.Rollback(ctx) }()
		_, err = probeTx.Exec(ctx, `
			INSERT INTO reconnect_intervals (
				id, pause_id, roster_id, series_id, game_attempt_id,
				participant_id, presence_epoch, interval_number,
				opened_at, deadline_at, created_at, updated_at
			)
			VALUES (
				$1, $2, $3, $4, $5,
				$6, 2, 1,
				$7, $8, $7, $7
			)`,
			uuid.New(),
			fixture.gamePauseID,
			fixture.draft.rosterID,
			fixture.draft.seriesID,
			fixture.attemptID,
			participantID,
			disconnectedAt,
			disconnectedAt.Add(2*time.Minute),
		)
		require.ErrorContains(t, err, "lock timeout")
	})

	t.Run("resume decision locks live presence", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixture(ctx, t)
		lockTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = lockTx.Rollback(ctx) }()
		_, err = lockTx.Exec(ctx, `
			SELECT 1
			FROM presence_states
			WHERE series_id = $1 AND participant_id = $2
			FOR NO KEY UPDATE`, fixture.draft.seriesID, fixture.draft.participantIDs[0])
		require.NoError(t, err)

		probeTx := beginReconnectLockProbe(ctx, t)
		defer func() { _ = probeTx.Rollback(ctx) }()
		err = insertResumeDecisionEvidence(
			ctx,
			probeTx,
			fixture,
			fixture.gamePauseID,
			nil,
			"connected",
			1,
			1,
			"resume",
			fixture.pausedAt,
		)
		require.ErrorContains(t, err, "lock timeout")
	})

	t.Run("resume decision locks reconnect interval", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixture(ctx, t)
		intervalID, _ := disconnectParticipant(
			ctx, t,
			fixture,
			fixture.draft.participantIDs[0],
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)

		lockTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = lockTx.Rollback(ctx) }()
		_, err = lockTx.Exec(ctx, `
			SELECT 1
			FROM reconnect_intervals
			WHERE id = $1
			FOR NO KEY UPDATE`, intervalID)
		require.NoError(t, err)

		probeTx := beginReconnectLockProbe(ctx, t)
		defer func() { _ = probeTx.Rollback(ctx) }()
		err = insertResumeDecisionEvidence(
			ctx,
			probeTx,
			fixture,
			fixture.gamePauseID,
			&intervalID,
			"disconnected",
			2,
			2,
			"wait_first",
			fixture.pausedAt.Add(time.Second),
		)
		require.ErrorContains(t, err, "lock timeout")
	})

	t.Run("pause resume locks live presence", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixture(ctx, t)
		insertResumeDecision(
			ctx, t,
			fixture,
			fixture.gamePauseID,
			1,
			nil,
			nil,
			"resume",
			fixture.pausedAt,
		)

		lockTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = lockTx.Rollback(ctx) }()
		_, err = lockTx.Exec(ctx, `
			SELECT 1
			FROM presence_states
			WHERE series_id = $1 AND participant_id = $2
			FOR NO KEY UPDATE`, fixture.draft.seriesID, fixture.draft.participantIDs[0])
		require.NoError(t, err)

		probeTx := beginReconnectLockProbe(ctx, t)
		defer func() { _ = probeTx.Rollback(ctx) }()
		_, err = probeTx.Exec(ctx, `
			UPDATE pauses
			SET state = 'resumed',
				current_revision_id = $2,
				revision = revision + 1,
				resolved_at = $3,
				updated_at = $3
			WHERE id = $1`,
			fixture.gamePauseID,
			uuid.New(),
			fixture.pausedAt.Add(time.Second),
		)
		require.ErrorContains(t, err, "lock timeout")
	})

	t.Run("pause cancellation locks reconnect intervals", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixture(ctx, t)
		intervalID, _ := disconnectParticipant(
			ctx, t,
			fixture,
			fixture.draft.participantIDs[0],
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)

		lockTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = lockTx.Rollback(ctx) }()
		_, err = lockTx.Exec(ctx, `
			SELECT 1
			FROM reconnect_intervals
			WHERE id = $1
			FOR NO KEY UPDATE`, intervalID)
		require.NoError(t, err)

		probeTx := beginReconnectLockProbe(ctx, t)
		defer func() { _ = probeTx.Rollback(ctx) }()
		revisionID := uuid.New()
		cancelledAt := fixture.pausedAt.Add(2 * time.Second)
		_, err = probeTx.Exec(ctx, `
			INSERT INTO pause_revisions (
				id, pause_id, previous_revision_id, revision_number,
				state, transition_reason, created_at
			)
			VALUES ($1, $2, $3, 2, 'cancelled', 'operator cancel', $4)`,
			revisionID,
			fixture.gamePauseID,
			fixture.gamePauseRevisionID,
			cancelledAt,
		)
		require.NoError(t, err)
		_, err = probeTx.Exec(ctx, `
			UPDATE pauses
			SET state = 'cancelled',
				current_revision_id = $2,
				revision = revision + 1,
				resolved_at = $3,
				updated_at = $3
			WHERE id = $1`, fixture.gamePauseID, revisionID, cancelledAt)
		require.ErrorContains(t, err, "lock timeout")
	})
}
