//go:build integration

package reconnect

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReconnectMigrationIntervalPauseIntegrity(t *testing.T) {
	ctx := context.Background()

	t.Run("rejects interval after Game pause resolution", func(t *testing.T) {
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
		resumeMigrationPause(ctx, t, fixture, true, fixture.pausedAt.Add(time.Second))
		assertReconnectIntervalRejected(
			ctx, t,
			fixture,
			fixture.gamePauseID,
			fixture.pausedAt.Add(2*time.Second),
		)
	})

	t.Run("rejects interval outside Game pause scope", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixture(ctx, t)
		assertReconnectIntervalRejected(
			ctx, t,
			fixture,
			fixture.rootPauseID,
			fixture.pausedAt.Add(time.Second),
		)
	})

	t.Run("allows terminal cancellation without continuation provenance", func(t *testing.T) {
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
		cancelledAt := fixture.pausedAt.Add(2 * time.Second)
		commandTag, err := sharedPool.Exec(ctx, `
			UPDATE reconnect_intervals
			SET state = 'cancelled',
				closed_at = $2,
				revision = revision + 1,
				updated_at = $2
			WHERE id = $1`, intervalID, cancelledAt)
		require.NoError(t, err)
		require.EqualValues(t, 1, commandTag.RowsAffected())

		interval := loadReconnectIntervalSnapshot(ctx, t, intervalID)
		require.Equal(t, "cancelled", interval.state)
		require.False(t, interval.suspendedByPauseID.Valid)
	})

	t.Run("rejects cancellation with open interval", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixture(ctx, t)
		_, _ = disconnectParticipant(
			ctx, t,
			fixture,
			fixture.draft.participantIDs[0],
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)
		assertPauseCancellationRejected(
			ctx, t,
			fixture.gamePauseID,
			fixture.gamePauseRevisionID,
			fixture.pausedAt.Add(2*time.Second),
			"closed reconnect intervals",
		)
	})

	t.Run("rejects cancellation with active child pause", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixture(ctx, t)
		assertPauseCancellationRejected(
			ctx, t,
			fixture.rootPauseID,
			fixture.rootPauseRevisionID,
			fixture.pausedAt.Add(time.Second),
			"active descendants",
		)
	})

	t.Run("rejects interval expiry before deadline", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixture(ctx, t)
		intervalID, deadline := disconnectParticipant(
			ctx, t,
			fixture,
			fixture.draft.participantIDs[0],
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)
		closedAt := deadline.Add(-time.Second)
		_, err := sharedPool.Exec(ctx, `
			UPDATE reconnect_intervals
			SET state = 'expired',
				closed_at = $2,
				revision = revision + 1,
				updated_at = $2
			WHERE id = $1`, intervalID, closedAt)
		require.ErrorContains(t, err, "reconnect_intervals_terminal_time_check")
	})

	t.Run("rejects interval reconnect after deadline", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixture(ctx, t)
		intervalID, deadline := disconnectParticipant(
			ctx, t,
			fixture,
			fixture.draft.participantIDs[0],
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)
		closedAt := deadline.Add(time.Second)
		_, err := sharedPool.Exec(ctx, `
			UPDATE reconnect_intervals
			SET state = 'reconnected',
				closed_at = $2,
				revision = revision + 1,
				updated_at = $2
			WHERE id = $1`, intervalID, closedAt)
		require.ErrorContains(t, err, "reconnect_intervals_terminal_time_check")
	})

	t.Run("rejects interval close after update evidence", func(t *testing.T) {
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
		updatedAt := fixture.pausedAt.Add(2 * time.Second)
		closedAt := updatedAt.Add(time.Second)
		_, err := sharedPool.Exec(ctx, `
			UPDATE reconnect_intervals
			SET state = 'expired',
				closed_at = $2,
				revision = revision + 1,
				updated_at = $3
			WHERE id = $1`, intervalID, closedAt, updatedAt)
		require.ErrorContains(t, err, "reconnect_intervals_terminal_time_check")
	})
}

func TestReconnectMigrationTerminalPresenceCAS(t *testing.T) {
	ctx := context.Background()

	t.Run("rejects reconnect without presence CAS", func(t *testing.T) {
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
		reconnectedAt := fixture.pausedAt.Add(2 * time.Second)
		_, err := sharedPool.Exec(ctx, `
			UPDATE reconnect_intervals
			SET state = 'reconnected',
				closed_at = $2,
				revision = revision + 1,
				updated_at = $2
			WHERE id = $1`, intervalID, reconnectedAt)
		require.ErrorContains(t, err, "terminal state differs from live presence CAS")
	})

	t.Run("rejects presence reconnect with open interval", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixture(ctx, t)
		_, _ = disconnectParticipant(
			ctx, t,
			fixture,
			fixture.draft.participantIDs[0],
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)
		reconnectedAt := fixture.pausedAt.Add(2 * time.Second)
		_, err := sharedPool.Exec(ctx, `
			UPDATE presence_states
			SET state = 'connected',
				presence_epoch = presence_epoch + 1,
				revision = revision + 1,
				connected_at = $3,
				disconnected_at = NULL,
				updated_at = $3
			WHERE series_id = $1 AND participant_id = $2`,
			fixture.draft.seriesID,
			fixture.draft.participantIDs[0],
			reconnectedAt,
		)
		require.ErrorContains(t, err, "presence retains an open interval")
	})

	t.Run("rejects expiry with reconnected presence", func(t *testing.T) {
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
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = tx.Exec(ctx, `
			UPDATE reconnect_intervals
			SET state = 'expired',
				closed_at = $2,
				revision = revision + 1,
				updated_at = $2
			WHERE id = $1`, intervalID, deadline)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			UPDATE presence_states
			SET state = 'connected',
				presence_epoch = presence_epoch + 1,
				revision = revision + 1,
				connected_at = $3,
				disconnected_at = NULL,
				updated_at = $3
			WHERE series_id = $1 AND participant_id = $2`,
			fixture.draft.seriesID,
			participantID,
			deadline,
		)
		require.NoError(t, err)
		err = tx.Commit(ctx)
		require.ErrorContains(t, err, "terminal state differs from live presence CAS")
	})
}
