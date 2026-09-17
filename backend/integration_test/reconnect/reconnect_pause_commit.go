//go:build integration

package reconnect

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/stretchr/testify/require"
)

func testReconnectContinuationPauseCommit(t *testing.T) {
	ctx := context.Background()

	t.Run("rejects a normal Wave pause committed over an open covered source", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixtureWithSlotLimit(ctx, t, 1)
		disconnectParticipant(
			ctx, t,
			fixture,
			fixture.draft.participantIDs[0],
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)
		waveID := createCoveringWave(ctx, t, fixture, fixture.pausedAt)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		require.NoError(t, insertNormalWavePauseEvidence(
			ctx,
			tx,
			fixture,
			waveID,
			uuid.New(),
			uuid.New(),
			suspendedAt,
		))

		err = tx.Commit(ctx)
		requireReconnectExactCheckViolation(
			t,
			err,
			"normal Wave pause requires atomic reconnect suspension",
		)
	})

	t.Run("commits a normal Wave pause with exact source cancellation", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixtureWithSlotLimit(ctx, t, 1)
		participantID := fixture.draft.participantIDs[0]
		sourceID, _ := disconnectParticipant(
			ctx, t,
			fixture,
			participantID,
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)
		waveID := createCoveringWave(ctx, t, fixture, fixture.pausedAt)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)
		pauseID := uuid.New()
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		require.NoError(t, insertNormalWavePauseEvidence(
			ctx,
			tx,
			fixture,
			waveID,
			pauseID,
			uuid.New(),
			suspendedAt,
		))
		_, err = tx.Exec(ctx, `
			UPDATE reconnect_intervals
			SET state = 'cancelled',
				closed_at = $2,
				suspended_by_pause_id = $3,
				revision = revision + 1,
				updated_at = $2
			WHERE id = $1`, sourceID, suspendedAt, pauseID)
		require.NoError(t, err)
		require.NoError(t, tx.Commit(ctx))

		source := loadReconnectIntervalSnapshot(ctx, t, sourceID)
		require.Equal(t, "cancelled", source.state)
		require.Equal(t, pauseID, uuid.UUID(source.suspendedByPauseID.Bytes))
		require.True(t, source.closedAt.Valid)
		require.True(t, suspendedAt.Equal(source.closedAt.Time))
	})

	t.Run("commits source insertion Wave pause and exact suspension in one transaction", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixtureWithSlotLimit(ctx, t, 1)
		participantID := fixture.draft.participantIDs[0]
		openedAt := fixture.pausedAt.Add(time.Second)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)
		waveID := createCoveringWave(ctx, t, fixture, fixture.pausedAt)
		sourceID := uuid.New()
		pauseID := uuid.New()
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		require.NoError(t, insertReconnectRootEvidence(
			ctx,
			tx,
			fixture,
			participantID,
			sourceID,
			openedAt,
			openedAt.Add(2*time.Minute),
		))
		require.NoError(t, insertNormalWavePauseEvidence(
			ctx,
			tx,
			fixture,
			waveID,
			pauseID,
			uuid.New(),
			suspendedAt,
		))
		_, err = tx.Exec(ctx, `
			UPDATE reconnect_intervals
			SET state = 'cancelled',
				closed_at = $2,
				suspended_by_pause_id = $3,
				revision = revision + 1,
				updated_at = $2
			WHERE id = $1`, sourceID, suspendedAt, pauseID)
		require.NoError(t, err)
		require.NoError(t, tx.Commit(ctx))

		source := loadReconnectIntervalSnapshot(ctx, t, sourceID)
		require.Equal(t, "cancelled", source.state)
		require.Equal(t, pauseID, uuid.UUID(source.suspendedByPauseID.Bytes))
		require.True(t, source.closedAt.Valid)
		require.True(t, suspendedAt.Equal(source.closedAt.Time))
	})

	t.Run("allows a normal Wave pause exactly at the source deadline", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixtureWithSlotLimit(ctx, t, 1)
		participantID := fixture.draft.participantIDs[0]
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)
		sourceID, sourceDeadline := disconnectParticipant(
			ctx, t,
			fixture,
			participantID,
			fixture.pausedAt.Add(time.Second),
			19*time.Second,
		)
		require.True(t, suspendedAt.Equal(sourceDeadline))
		waveID := createCoveringWave(ctx, t, fixture, fixture.pausedAt)
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		require.NoError(t, insertNormalWavePauseEvidence(
			ctx,
			tx,
			fixture,
			waveID,
			uuid.New(),
			uuid.New(),
			suspendedAt,
		))
		require.NoError(t, tx.Commit(ctx))

		source := loadReconnectIntervalSnapshot(ctx, t, sourceID)
		require.Equal(t, "open", source.state)
		require.False(t, source.closedAt.Valid)
		require.False(t, source.suspendedByPauseID.Valid)
	})

	t.Run("allows a retained root that closed before the normal Wave pause", func(t *testing.T) {
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

		openedAt := suspendedAt.Add(-15 * time.Second)
		closedAt := suspendedAt.Add(-5 * time.Second)
		disconnectPresence(ctx, t, fixture, participantID, openedAt)
		rootID := uuid.New()
		rootTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = rootTx.Rollback(ctx) }()
		err = insertReconnectContinuationWith(
			ctx,
			rootTx,
			fixture,
			reconnectContinuationInput{
				id:                 rootID,
				participantID:      participantID,
				presenceEpoch:      2,
				intervalNumber:     1,
				continuationNumber: 0,
				state:              "cancelled",
				openedAt:           openedAt,
				deadlineAt:         suspendedAt.Add(time.Minute),
				closedAt:           &closedAt,
				updatedAt:          closedAt,
			},
		)
		if err == nil {
			_, err = rootTx.Exec(
				ctx, `
				UPDATE reconnect_slot_counters
				SET slots_used = slots_used + 1,
					revision = revision + 1,
					updated_at = $3
				WHERE pause_id = $1 AND participant_id = $2`,
				fixture.gamePauseID,
				participantID,
				closedAt,
			)
		}
		require.NoError(t, err)
		require.NoError(t, rootTx.Commit(ctx))

		root := loadReconnectIntervalSnapshot(ctx, t, rootID)
		require.Equal(t, "cancelled", root.state)
		require.True(t, root.closedAt.Valid)
		require.True(t, closedAt.Equal(root.closedAt.Time))
		require.False(t, root.suspendedByPauseID.Valid)
	})
}
