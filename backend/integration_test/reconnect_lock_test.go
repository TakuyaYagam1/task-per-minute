//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/stretchr/testify/require"
)

func TestReconnectContinuationLocks(t *testing.T) {
	ctx := context.Background()

	for _, lockTarget := range []string{
		"owning Game pause",
		"predecessor interval",
		"suspending Wave pause",
		"live Presence",
		"slot counter",
	} {
		t.Run(lockTarget, func(t *testing.T) {
			resetMigrationTables(ctx, t)
			t.Cleanup(func() { resetMigrationTables(ctx, t) })

			fixture, sourceID, sourceDeadline, suspendedAt :=
				createCancelledReconnectRoot(ctx, t, 1)
			participantID := fixture.draft.participantIDs[0]
			holder, err := sharedPool.Begin(ctx)
			require.NoError(t, err)
			defer func() { _ = holder.Rollback(ctx) }()

			switch lockTarget {
			case "owning Game pause":
				_, err = holder.Exec(ctx, `
					SELECT 1 FROM pauses
					WHERE id = $1 FOR NO KEY UPDATE`, fixture.gamePauseID)
			case "predecessor interval":
				_, err = holder.Exec(ctx, `
					SELECT 1 FROM reconnect_intervals
					WHERE id = $1 FOR NO KEY UPDATE`, sourceID)
			case "suspending Wave pause":
				_, err = holder.Exec(ctx, `
					SELECT 1 FROM pauses
					WHERE id = $1 FOR NO KEY UPDATE`, fixture.normalPauseID)
			case "live Presence":
				_, err = holder.Exec(ctx, `
					SELECT 1 FROM presence_states
					WHERE series_id = $1 AND participant_id = $2
					FOR NO KEY UPDATE`, fixture.draft.seriesID, participantID)
			case "slot counter":
				_, err = holder.Exec(ctx, `
					SELECT 1 FROM reconnect_slot_counters
					WHERE pause_id = $1 AND participant_id = $2
					FOR NO KEY UPDATE`, fixture.gamePauseID, participantID)
			}
			require.NoError(t, err)

			probe := beginReconnectLockProbe(ctx, t)
			defer func() { _ = probe.Rollback(ctx) }()
			openedAt := suspendedAt.Add(30 * time.Second)
			err = insertReconnectContinuationWith(
				ctx,
				probe,
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
			)
			requireReconnectPostgresError(t, err, "55P03", "", "lock timeout")
		})
	}
}

func TestReconnectContinuationDeadlockOrder(t *testing.T) {
	ctx := context.Background()

	t.Run("Game cancellation loses without deadlock to Wave suspension", func(t *testing.T) {
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

		cancelTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = cancelTx.Rollback(ctx) }()
		require.NoError(t, setReconnectDeadlockTimeouts(ctx, cancelTx))
		_, err = cancelTx.Exec(ctx, `
			SELECT 1 FROM pauses WHERE id = $1 FOR UPDATE`, fixture.gamePauseID)
		require.NoError(t, err)
		cancelRevisionID := uuid.New()
		_, err = cancelTx.Exec(ctx, `
			INSERT INTO pause_revisions (
				id, pause_id, previous_revision_id, revision_number,
				state, transition_reason, created_at
			)
			VALUES ($1, $2, $3, 2, 'cancelled', 'operator cancel', $4)`,
			cancelRevisionID,
			fixture.gamePauseID,
			fixture.gamePauseRevisionID,
			suspendedAt,
		)
		require.NoError(t, err)

		suspensionTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = suspensionTx.Rollback(ctx) }()
		require.NoError(t, setReconnectDeadlockTimeouts(ctx, suspensionTx))
		var suspensionPID int
		require.NoError(t, suspensionTx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&suspensionPID))
		pauseID := uuid.New()
		suspensionResult := make(chan error, 1)
		go func() {
			suspensionErr := insertNormalWavePauseEvidence(
				ctx,
				suspensionTx,
				fixture,
				waveID,
				pauseID,
				uuid.New(),
				suspendedAt,
			)
			if suspensionErr == nil {
				_, suspensionErr = suspensionTx.Exec(ctx, `
					UPDATE reconnect_intervals
					SET state = 'cancelled',
						closed_at = $2,
						suspended_by_pause_id = $3,
						revision = revision + 1,
						updated_at = $2
					WHERE id = $1`, sourceID, suspendedAt, pauseID)
			}
			suspensionResult <- suspensionErr
		}()
		waitReconnectBackendLock(ctx, t, suspensionPID)

		_, cancelErr := cancelTx.Exec(ctx, `
			UPDATE pauses
			SET state = 'cancelled',
				current_revision_id = $2,
				revision = revision + 1,
				resolved_at = $3,
				updated_at = $3
			WHERE id = $1`, fixture.gamePauseID, cancelRevisionID, suspendedAt)
		requireReconnectExactCheckViolation(
			t,
			cancelErr,
			"pause cancellation requires closed reconnect intervals",
		)
		require.NoError(t, cancelTx.Rollback(ctx))
		require.NoError(t, <-suspensionResult)
		require.NoError(t, suspensionTx.Commit(ctx))
	})

	t.Run("resume decision loses without deadlock to continuation insertion", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture, sourceID, sourceDeadline, suspendedAt :=
			createCancelledReconnectRoot(ctx, t, 1)
		participantID := fixture.draft.participantIDs[0]
		openedAt := suspendedAt.Add(30 * time.Second)

		decisionTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = decisionTx.Rollback(ctx) }()
		require.NoError(t, setReconnectDeadlockTimeouts(ctx, decisionTx))
		_, err = decisionTx.Exec(ctx, `
			SELECT 1 FROM pauses WHERE id = $1 FOR UPDATE`, fixture.gamePauseID)
		require.NoError(t, err)

		continuationTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = continuationTx.Rollback(ctx) }()
		require.NoError(t, setReconnectDeadlockTimeouts(ctx, continuationTx))
		var continuationPID int
		require.NoError(t, continuationTx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&continuationPID))
		continuationResult := make(chan error, 1)
		go func() {
			continuationResult <- insertReconnectContinuationWith(
				ctx,
				continuationTx,
				fixture,
				reconnectContinuationInput{
					id:                 uuid.New(),
					continuedFromID:    &sourceID,
					participantID:      participantID,
					presenceEpoch:      2,
					intervalNumber:     1,
					continuationNumber: 1,
					openedAt:           openedAt,
					deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
				},
			)
		}()
		waitReconnectBackendLock(ctx, t, continuationPID)

		decisionErr := insertResumeDecisionEvidence(
			ctx,
			decisionTx,
			fixture,
			fixture.gamePauseID,
			&sourceID,
			"disconnected",
			2,
			2,
			"wait_first",
			openedAt,
		)
		requireReconnectExactCheckViolation(
			t,
			decisionErr,
			"resume decision does not match durable presence evidence",
		)
		require.NoError(t, decisionTx.Rollback(ctx))
		require.NoError(t, <-continuationResult)
		require.NoError(t, continuationTx.Commit(ctx))
	})

	t.Run("Wave suspension completes before a waiting Game cancellation", func(t *testing.T) {
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
		suspensionTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = suspensionTx.Rollback(ctx) }()
		require.NoError(t, setReconnectDeadlockTimeouts(ctx, suspensionTx))
		require.NoError(t, insertNormalWavePauseEvidence(
			ctx,
			suspensionTx,
			fixture,
			waveID,
			pauseID,
			uuid.New(),
			suspendedAt,
		))

		cancelTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = cancelTx.Rollback(ctx) }()
		require.NoError(t, setReconnectDeadlockTimeouts(ctx, cancelTx))
		var cancelPID int
		require.NoError(t, cancelTx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&cancelPID))
		cancelResult := make(chan error, 1)
		go func() {
			_, cancelErr := cancelTx.Exec(ctx, `
				SELECT 1 FROM pauses WHERE id = $1 FOR UPDATE`, fixture.gamePauseID)
			if cancelErr == nil {
				cancelRevisionID := uuid.New()
				_, cancelErr = cancelTx.Exec(ctx, `
					INSERT INTO pause_revisions (
						id, pause_id, previous_revision_id, revision_number,
						state, transition_reason, created_at
					)
					VALUES ($1, $2, $3, 2, 'cancelled', 'operator cancel', $4)`,
					cancelRevisionID,
					fixture.gamePauseID,
					fixture.gamePauseRevisionID,
					suspendedAt.Add(time.Second),
				)
				if cancelErr == nil {
					_, cancelErr = cancelTx.Exec(ctx, `
						UPDATE pauses
						SET state = 'cancelled',
							current_revision_id = $2,
							revision = revision + 1,
							resolved_at = $3,
							updated_at = $3
						WHERE id = $1`,
						fixture.gamePauseID,
						cancelRevisionID,
						suspendedAt.Add(time.Second),
					)
				}
			}
			cancelResult <- cancelErr
		}()
		waitReconnectBackendLock(ctx, t, cancelPID)

		_, err = suspensionTx.Exec(ctx, `
			UPDATE reconnect_intervals
			SET state = 'cancelled',
				closed_at = $2,
				suspended_by_pause_id = $3,
				revision = revision + 1,
				updated_at = $2
			WHERE id = $1`, sourceID, suspendedAt, pauseID)
		require.NoError(t, err)
		require.NoError(t, suspensionTx.Commit(ctx))
		require.NoError(t, <-cancelResult)
		require.NoError(t, cancelTx.Commit(ctx))
	})

	t.Run("continuation completes before a waiting resume decision", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture, sourceID, sourceDeadline, suspendedAt :=
			createCancelledReconnectRoot(ctx, t, 1)
		participantID := fixture.draft.participantIDs[0]
		openedAt := suspendedAt.Add(30 * time.Second)
		continuationID := uuid.New()
		continuationTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = continuationTx.Rollback(ctx) }()
		require.NoError(t, setReconnectDeadlockTimeouts(ctx, continuationTx))
		require.NoError(t, insertReconnectContinuationWith(
			ctx,
			continuationTx,
			fixture,
			reconnectContinuationInput{
				id:                 continuationID,
				continuedFromID:    &sourceID,
				participantID:      participantID,
				presenceEpoch:      2,
				intervalNumber:     1,
				continuationNumber: 1,
				openedAt:           openedAt,
				deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
			},
		))

		decisionTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = decisionTx.Rollback(ctx) }()
		require.NoError(t, setReconnectDeadlockTimeouts(ctx, decisionTx))
		var decisionPID int
		require.NoError(t, decisionTx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&decisionPID))
		decisionResult := make(chan error, 1)
		go func() {
			decisionResult <- insertResumeDecisionEvidence(
				ctx,
				decisionTx,
				fixture,
				fixture.gamePauseID,
				&continuationID,
				"disconnected",
				2,
				2,
				"wait_first",
				openedAt,
			)
		}()
		waitReconnectBackendLock(ctx, t, decisionPID)
		require.NoError(t, continuationTx.Commit(ctx))
		require.NoError(t, <-decisionResult)
		require.NoError(t, decisionTx.Commit(ctx))
	})

	t.Run("normal Wave entry completes before a later continuation", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture, sourceID, sourceDeadline, suspendedAt :=
			createCancelledReconnectRoot(ctx, t, 1)
		participantID := fixture.draft.participantIDs[0]
		waveID := createCoveringWave(ctx, t, fixture, fixture.pausedAt)
		waveStartedAt := suspendedAt.Add(30 * time.Second)
		waveTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = waveTx.Rollback(ctx) }()
		require.NoError(t, setReconnectDeadlockTimeouts(ctx, waveTx))
		require.NoError(t, insertNormalWavePauseEvidence(
			ctx,
			waveTx,
			fixture,
			waveID,
			uuid.New(),
			uuid.New(),
			waveStartedAt,
		))

		continuationTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = continuationTx.Rollback(ctx) }()
		require.NoError(t, setReconnectDeadlockTimeouts(ctx, continuationTx))
		var continuationPID int
		require.NoError(t, continuationTx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&continuationPID))
		continuationResult := make(chan error, 1)
		go func() {
			openedAt := waveStartedAt.Add(time.Second)
			continuationResult <- insertReconnectContinuationWith(
				ctx,
				continuationTx,
				fixture,
				reconnectContinuationInput{
					id:                 uuid.New(),
					continuedFromID:    &sourceID,
					participantID:      participantID,
					presenceEpoch:      2,
					intervalNumber:     1,
					continuationNumber: 1,
					openedAt:           openedAt,
					deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
				},
			)
		}()
		waitReconnectBackendLock(ctx, t, continuationPID)
		require.NoError(t, waveTx.Commit(ctx))
		require.NoError(t, <-continuationResult)
		require.NoError(t, continuationTx.Commit(ctx))
	})

	t.Run("continuation commits before a covering normal Wave entry is rejected", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture, sourceID, sourceDeadline, suspendedAt :=
			createCancelledReconnectRoot(ctx, t, 1)
		participantID := fixture.draft.participantIDs[0]
		openedAt := suspendedAt.Add(30 * time.Second)
		continuationTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = continuationTx.Rollback(ctx) }()
		require.NoError(t, setReconnectDeadlockTimeouts(ctx, continuationTx))
		require.NoError(t, insertReconnectContinuationWith(
			ctx,
			continuationTx,
			fixture,
			reconnectContinuationInput{
				id:                 uuid.New(),
				continuedFromID:    &sourceID,
				participantID:      participantID,
				presenceEpoch:      2,
				intervalNumber:     1,
				continuationNumber: 1,
				openedAt:           openedAt,
				deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
			},
		))

		waveID := createCoveringWave(ctx, t, fixture, fixture.pausedAt)
		waveStartedAt := openedAt.Add(time.Second)
		waveTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = waveTx.Rollback(ctx) }()
		require.NoError(t, setReconnectDeadlockTimeouts(ctx, waveTx))
		var wavePID int
		require.NoError(t, waveTx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&wavePID))
		waveResult := make(chan error, 1)
		go func() {
			waveResult <- insertNormalWavePauseEvidence(
				ctx,
				waveTx,
				fixture,
				waveID,
				uuid.New(),
				uuid.New(),
				waveStartedAt,
			)
		}()
		waitReconnectBackendLock(ctx, t, wavePID)
		require.NoError(t, continuationTx.Commit(ctx))
		waveErr := <-waveResult
		if waveErr == nil {
			waveErr = waveTx.Commit(ctx)
		}
		requireReconnectExactCheckViolation(
			t,
			waveErr,
			"normal Wave pause requires atomic reconnect suspension",
		)
	})
}
