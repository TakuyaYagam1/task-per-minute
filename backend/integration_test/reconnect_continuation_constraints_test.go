//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/stretchr/testify/require"
)

func testReconnectContinuationIdentityAndCounters(t *testing.T) {
	ctx := context.Background()

	for _, testCase := range []struct {
		name   string
		mutate func(testing.TB, reconnectMigrationFixture, *reconnectContinuationInput)
	}{
		{
			name: "rejects cross-participant predecessor",
			mutate: func(
				tb testing.TB,
				fixture reconnectMigrationFixture,
				input *reconnectContinuationInput,
			) {
				tb.Helper()
				participantID := fixture.draft.participantIDs[1]
				disconnectPresence(ctx, tb, fixture, participantID, input.openedAt)
				input.participantID = participantID
			},
		},
		{
			name: "rejects wrong logical interval number",
			mutate: func(
				_ testing.TB,
				_ reconnectMigrationFixture,
				input *reconnectContinuationInput,
			) {
				input.intervalNumber = 2
			},
		},
		{
			name: "rejects wrong owning Game pause",
			mutate: func(
				_ testing.TB,
				fixture reconnectMigrationFixture,
				input *reconnectContinuationInput,
			) {
				input.owningPauseID = &fixture.rootPauseID
			},
		},
		{
			name: "rejects wrong Game attempt for owning pause",
			mutate: func(
				tb testing.TB,
				fixture reconnectMigrationFixture,
				input *reconnectContinuationInput,
			) {
				tb.Helper()
				slotID := createMigrationGameSlot(
					ctx, tb,
					fixture.draft.seriesID,
					fixture.draft.rosterID,
					2,
					"crypto",
				)
				attemptID := createActiveMigrationAttempt(
					ctx, tb,
					slotID,
					fixture.draft.seriesID,
					fixture.draft.rosterID,
					input.openedAt.Add(-time.Second),
				)
				input.gameAttemptID = &attemptID
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetMigrationTables(ctx, t)
			t.Cleanup(func() { resetMigrationTables(ctx, t) })

			fixture, sourceID, sourceDeadline, suspendedAt := createCancelledReconnectRoot(ctx, t, 1)
			openedAt := suspendedAt.Add(30 * time.Second)
			input := reconnectContinuationInput{
				id:                 uuid.New(),
				continuedFromID:    &sourceID,
				suspendedByPauseID: nil,
				participantID:      fixture.draft.participantIDs[0],
				presenceEpoch:      2,
				intervalNumber:     1,
				continuationNumber: 1,
				openedAt:           openedAt,
				deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
			}
			testCase.mutate(t, fixture, &input)

			err := insertReconnectContinuation(ctx, fixture, input)
			requireReconnectExactCheckViolation(
				t,
				err,
				"reconnect continuation does not match predecessor identity",
			)
		})
	}

	t.Run("defines predecessor no-fork as a unique partial index", func(t *testing.T) {
		var (
			isUnique   bool
			indexDef   string
			predicate  string
			keyColumns string
		)
		err := sharedPool.QueryRow(ctx, `
			SELECT
				index_row.indisunique,
				pg_get_indexdef(index_row.indexrelid),
				pg_get_expr(index_row.indpred, index_row.indrelid),
				STRING_AGG(attribute.attname, ',' ORDER BY key_column.ordinality)
			FROM pg_index AS index_row
			JOIN pg_class AS index_relation
				ON index_relation.oid = index_row.indexrelid
			JOIN pg_class AS table_relation
				ON table_relation.oid = index_row.indrelid
			JOIN pg_namespace AS namespace
				ON namespace.oid = table_relation.relnamespace
			JOIN LATERAL UNNEST(index_row.indkey)
				WITH ORDINALITY AS key_column(attnum, ordinality) ON TRUE
			JOIN pg_attribute AS attribute
				ON attribute.attrelid = table_relation.oid
				AND attribute.attnum = key_column.attnum
			WHERE namespace.nspname = 'public'
				AND table_relation.relname = 'reconnect_intervals'
				AND index_relation.relname = 'reconnect_intervals_continued_from_key'
			GROUP BY index_row.indisunique, index_row.indexrelid,
				index_row.indpred, index_row.indrelid`).Scan(
			&isUnique,
			&indexDef,
			&predicate,
			&keyColumns,
		)
		require.NoError(t, err)
		require.True(t, isUnique)
		require.Equal(t, "continued_from_id", keyColumns)
		require.Equal(t, "(continued_from_id IS NOT NULL)", predicate)
		require.Contains(t, indexDef, "UNIQUE INDEX")
	})

	t.Run("rejects source cancellation by a non-covering normal Wave pause", func(t *testing.T) {
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
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)
		otherParticipantID := fixture.draft.participantIDs[1]
		waveID := createWaveWithMembers(
			ctx, t,
			fixture,
			[]uuid.UUID{otherParticipantID},
			fixture.pausedAt,
		)
		err := attemptNormalWavePauseAndSuspendInterval(
			ctx, t,
			fixture,
			waveID,
			[]uuid.UUID{otherParticipantID},
			sourceID,
			suspendedAt,
		)
		requireReconnectCheckViolation(
			t,
			err,
			"reconnect cancellation requires a covering active normal Wave pause",
		)
	})

	t.Run("rejects suspension provenance on a newly open continuation", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture, sourceID, sourceDeadline, suspendedAt := createCancelledReconnectRoot(ctx, t, 1)
		openedAt := suspendedAt.Add(30 * time.Second)
		err := insertReconnectContinuation(ctx, fixture, reconnectContinuationInput{
			id:                 uuid.New(),
			continuedFromID:    &sourceID,
			suspendedByPauseID: &fixture.gamePauseID,
			participantID:      fixture.draft.participantIDs[0],
			presenceEpoch:      2,
			intervalNumber:     1,
			continuationNumber: 1,
			openedAt:           openedAt,
			deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
		})
		requireReconnectCheckViolation(
			t,
			err,
			"open reconnect continuation cannot carry suspension provenance",
		)
	})

	t.Run("rejects wrong presence epoch", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture, sourceID, sourceDeadline, suspendedAt := createCancelledReconnectRoot(ctx, t, 1)
		openedAt := suspendedAt.Add(30 * time.Second)
		err := insertReconnectContinuation(ctx, fixture, reconnectContinuationInput{
			id:                 uuid.New(),
			continuedFromID:    &sourceID,
			suspendedByPauseID: nil,
			participantID:      fixture.draft.participantIDs[0],
			presenceEpoch:      3,
			intervalNumber:     1,
			continuationNumber: 1,
			openedAt:           openedAt,
			deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
		})
		requireReconnectCheckViolation(
			t,
			err,
			"reconnect continuation does not match predecessor identity",
		)
	})

	t.Run("rejects reconnected predecessor", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixtureWithSlotLimit(ctx, t, 1)
		participantID := fixture.draft.participantIDs[0]
		sourceID, sourceDeadline := disconnectParticipant(
			ctx, t,
			fixture,
			participantID,
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)
		reconnectedAt := fixture.pausedAt.Add(20 * time.Second)
		reconnectParticipant(ctx, t, fixture, participantID, sourceID, reconnectedAt)
		openedAt := fixture.pausedAt.Add(30 * time.Second)
		err := insertReconnectContinuation(ctx, fixture, reconnectContinuationInput{
			id:                 uuid.New(),
			continuedFromID:    &sourceID,
			suspendedByPauseID: nil,
			participantID:      participantID,
			presenceEpoch:      2,
			intervalNumber:     1,
			continuationNumber: 1,
			openedAt:           openedAt,
			deadlineAt:         openedAt.Add(sourceDeadline.Sub(reconnectedAt)),
		})
		requireReconnectCheckViolation(
			t,
			err,
			"reconnect continuation requires a cancelled predecessor with suspension provenance",
		)
	})

	t.Run("rejects expired predecessor", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixtureWithSlotLimit(ctx, t, 1)
		participantID := fixture.draft.participantIDs[0]
		sourceID, sourceDeadline := disconnectParticipant(
			ctx, t,
			fixture,
			participantID,
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)
		_, err := sharedPool.Exec(ctx, `
			UPDATE reconnect_intervals
			SET state = 'expired',
				closed_at = $2,
				revision = revision + 1,
				updated_at = $2
			WHERE id = $1`, sourceID, sourceDeadline)
		require.NoError(t, err)
		openedAt := sourceDeadline.Add(30 * time.Second)
		err = insertReconnectContinuation(ctx, fixture, reconnectContinuationInput{
			id:                 uuid.New(),
			continuedFromID:    &sourceID,
			suspendedByPauseID: nil,
			participantID:      participantID,
			presenceEpoch:      2,
			intervalNumber:     1,
			continuationNumber: 1,
			openedAt:           openedAt,
			deadlineAt:         openedAt.Add(time.Minute),
		})
		requireReconnectCheckViolation(
			t,
			err,
			"reconnect continuation requires a cancelled predecessor with suspension provenance",
		)
	})

	t.Run("retains one open interval per participant", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture, sourceID, sourceDeadline, suspendedAt := createCancelledReconnectRoot(ctx, t, 2)
		participantID := fixture.draft.participantIDs[0]
		openedAt := suspendedAt.Add(30 * time.Second)
		require.NoError(t, insertReconnectContinuation(
			ctx,
			fixture,
			reconnectContinuationInput{
				id:                 uuid.New(),
				continuedFromID:    &sourceID,
				suspendedByPauseID: nil,
				participantID:      participantID,
				presenceEpoch:      2,
				intervalNumber:     1,
				continuationNumber: 1,
				openedAt:           openedAt,
				deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
			},
		))

		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = tx.Exec(
			ctx, `
			INSERT INTO reconnect_intervals (
				id, pause_id, roster_id, series_id, game_attempt_id,
				participant_id, presence_epoch, interval_number,
				opened_at, deadline_at, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, 2, 2, $7, $8, $7, $7)`,
			uuid.New(),
			fixture.gamePauseID,
			fixture.draft.rosterID,
			fixture.draft.seriesID,
			fixture.attemptID,
			participantID,
			openedAt.Add(time.Second),
			openedAt.Add(2*time.Minute),
		)
		require.ErrorContains(t, err, "reconnect_intervals_one_open_idx")
	})

	t.Run("rejects root count without counter advance", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixtureWithSlotLimit(ctx, t, 2)
		participantID := fixture.draft.participantIDs[0]
		disconnectedAt := fixture.pausedAt.Add(time.Second)
		disconnectPresence(ctx, t, fixture, participantID, disconnectedAt)

		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		_, err = tx.Exec(
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
			disconnectedAt,
			disconnectedAt.Add(2*time.Minute),
		)
		require.NoError(t, err)
		err = tx.Commit(ctx)
		requireReconnectExactCheckViolation(
			t,
			err,
			"reconnect slot count differs from retained root intervals",
		)
	})

	t.Run("rejects a gap in root interval numbers", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixtureWithSlotLimit(ctx, t, 2)
		participantID := fixture.draft.participantIDs[0]
		disconnectedAt := fixture.pausedAt.Add(time.Second)
		disconnectPresence(ctx, t, fixture, participantID, disconnectedAt)

		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		_, err = tx.Exec(
			ctx, `
			UPDATE reconnect_slot_counters
			SET slots_used = slots_used + 1,
				revision = revision + 1,
				updated_at = $3
			WHERE pause_id = $1 AND participant_id = $2`,
			fixture.gamePauseID,
			participantID,
			disconnectedAt,
		)
		require.NoError(t, err)
		_, err = tx.Exec(
			ctx, `
			INSERT INTO reconnect_intervals (
				id, pause_id, roster_id, series_id, game_attempt_id,
				participant_id, presence_epoch, interval_number,
				opened_at, deadline_at, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, 2, 2, $7, $8, $7, $7)`,
			uuid.New(),
			fixture.gamePauseID,
			fixture.draft.rosterID,
			fixture.draft.seriesID,
			fixture.attemptID,
			participantID,
			disconnectedAt,
			disconnectedAt.Add(2*time.Minute),
		)
		require.NoError(t, err)
		err = tx.Commit(ctx)
		requireReconnectExactCheckViolation(
			t,
			err,
			"reconnect root interval numbers must be contiguous",
		)
	})
}
