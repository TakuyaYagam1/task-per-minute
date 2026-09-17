//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/stretchr/testify/require"
)

func TestReconnectContinuationRootPresenceEpoch(t *testing.T) {
	ctx := context.Background()

	t.Run("rejects a second fresh root in the same presence epoch", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixtureWithSlotLimit(ctx, t, 2)
		participantID := fixture.draft.participantIDs[0]
		openedAt := fixture.pausedAt.Add(time.Second)
		firstID, firstDeadline := disconnectParticipant(
			ctx, t, fixture, participantID, openedAt, 2*time.Minute,
		)
		expireReconnectInterval(ctx, t, firstID, firstDeadline)

		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		err = insertReconnectRootAtEpoch(
			ctx, tx, fixture, participantID, uuid.New(), 2, 2, firstDeadline.Add(time.Second),
		)
		if err == nil {
			err = tx.Commit(ctx)
		}
		requireReconnectPostgresError(
			t,
			err,
			"23505",
			"reconnect_intervals_root_presence_epoch_key",
			"",
		)
	})

	t.Run("allows a fresh root after presence advances to a new epoch", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixtureWithSlotLimit(ctx, t, 2)
		participantID := fixture.draft.participantIDs[0]
		openedAt := fixture.pausedAt.Add(time.Second)
		firstID, firstDeadline := disconnectParticipant(
			ctx, t, fixture, participantID, openedAt, 2*time.Minute,
		)
		expireReconnectInterval(ctx, t, firstID, firstDeadline)
		connectedAt := firstDeadline.Add(time.Second)
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
			participantID,
			connectedAt,
		)
		require.NoError(t, err)

		secondID, _ := disconnectParticipantAtInterval(
			ctx, t,
			fixture,
			participantID,
			2,
			connectedAt.Add(time.Second),
			2*time.Minute,
		)
		second := loadReconnectIntervalSnapshot(ctx, t, secondID)
		require.EqualValues(t, 4, second.presenceEpoch)
		require.Equal(t, 2, second.intervalNumber)
		require.Zero(t, second.continuationNumber)
	})

	t.Run("serializes concurrent roots in one new presence epoch", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixtureWithSlotLimit(ctx, t, 2)
		participantID := fixture.draft.participantIDs[0]
		openedAt := fixture.pausedAt.Add(time.Second)
		disconnectPresence(ctx, t, fixture, participantID, openedAt)

		winnerTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = winnerTx.Rollback(ctx) }()
		require.NoError(t, setReconnectDeadlockTimeouts(ctx, winnerTx))
		require.NoError(t, insertReconnectRootAtEpoch(
			ctx, winnerTx, fixture, participantID, uuid.New(), 2, 1, openedAt,
		))

		loserTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = loserTx.Rollback(ctx) }()
		require.NoError(t, setReconnectDeadlockTimeouts(ctx, loserTx))
		var loserPID int
		require.NoError(t, loserTx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&loserPID))
		loserResult := make(chan error, 1)
		go func() {
			loserErr := insertReconnectRootAtEpoch(
				ctx, loserTx, fixture, participantID, uuid.New(), 2, 1, openedAt,
			)
			if loserErr == nil {
				loserErr = loserTx.Commit(ctx)
			}
			loserResult <- loserErr
		}()
		waitReconnectBackendLock(ctx, t, loserPID)
		require.NoError(t, winnerTx.Commit(ctx))
		loserErr := <-loserResult
		require.Error(t, loserErr)
		var postgresError *pgconn.PgError
		require.ErrorAs(t, loserErr, &postgresError)
		require.Contains(t, []string{"23505", "23514"}, postgresError.Code)

		var (
			rootCount int
			slotsUsed int
		)
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM reconnect_intervals
			WHERE pause_id = $1
				AND participant_id = $2
				AND presence_epoch = 2
				AND continuation_number = 0`,
			fixture.gamePauseID,
			participantID,
		).Scan(&rootCount))
		require.Equal(t, 1, rootCount)
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT slots_used
			FROM reconnect_slot_counters
			WHERE pause_id = $1 AND participant_id = $2`,
			fixture.gamePauseID,
			participantID,
		).Scan(&slotsUsed))
		require.Equal(t, 1, slotsUsed)
	})
}
