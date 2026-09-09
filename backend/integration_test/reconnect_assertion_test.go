//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stretchr/testify/require"
)

func assertReconnectIntervalRejected(
	ctx context.Context, tb testing.TB,
	fixture reconnectMigrationFixture,
	pauseID uuid.UUID,
	disconnectedAt time.Time,
) {
	tb.Helper()

	participantID := fixture.draft.participantIDs[0]
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
	require.NoError(tb, err)

	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
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
		pauseID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		fixture.attemptID,
		participantID,
		disconnectedAt,
		disconnectedAt.Add(2*time.Minute),
	)
	require.ErrorContains(tb, err, "active Game pause")
}

func assertPauseResumeRejected(
	ctx context.Context, tb testing.TB,
	pauseID uuid.UUID,
	previousRevisionID uuid.UUID,
	resumedAt time.Time,
	expected string,
) {
	tb.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()
	revisionID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO pause_revisions (
			id, pause_id, previous_revision_id, revision_number,
			state, transition_reason, created_at
		)
		VALUES ($1, $2, $3, 2, 'resumed', 'presence restored', $4)`,
		revisionID,
		pauseID,
		previousRevisionID,
		resumedAt,
	)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, `
		UPDATE pauses
		SET state = 'resumed',
			current_revision_id = $2,
			revision = revision + 1,
			resolved_at = $3,
			updated_at = $3
		WHERE id = $1`, pauseID, revisionID, resumedAt)
	require.ErrorContains(tb, err, expected)
}

func assertPauseCancellationRejected(
	ctx context.Context, tb testing.TB,
	pauseID uuid.UUID,
	previousRevisionID uuid.UUID,
	cancelledAt time.Time,
	expected string,
) {
	tb.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()
	revisionID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO pause_revisions (
			id, pause_id, previous_revision_id, revision_number,
			state, transition_reason, created_at
		)
		VALUES ($1, $2, $3, 2, 'cancelled', 'operator cancel', $4)`,
		revisionID,
		pauseID,
		previousRevisionID,
		cancelledAt,
	)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, `
		UPDATE pauses
		SET state = 'cancelled',
			current_revision_id = $2,
			revision = revision + 1,
			resolved_at = $3,
			updated_at = $3
		WHERE id = $1`, pauseID, revisionID, cancelledAt)
	require.ErrorContains(tb, err, expected)
}

func beginReconnectLockProbe(ctx context.Context, tb testing.TB) pgx.Tx {
	tb.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, "SET LOCAL lock_timeout = '100ms'")
	require.NoError(tb, err)
	return tx
}

func setReconnectDeadlockTimeouts(
	ctx context.Context,
	executor reconnectExecutor,
) error {
	for _, statement := range []string{
		"SET LOCAL deadlock_timeout = '100ms'",
		"SET LOCAL lock_timeout = '2s'",
		"SET LOCAL statement_timeout = '3s'",
	} {
		if _, err := executor.Exec(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func waitReconnectBackendLock(ctx context.Context, tb testing.TB, backendPID int) {
	tb.Helper()

	require.Eventually(tb, func() bool {
		var waitEventType string
		err := sharedPool.QueryRow(ctx, `
			SELECT COALESCE(wait_event_type, '')
			FROM pg_stat_activity
			WHERE pid = $1`, backendPID).Scan(&waitEventType)
		return err == nil && waitEventType == "Lock"
	}, 2*time.Second, 10*time.Millisecond)
}

func insertResumeDecisionEvidence(
	ctx context.Context,
	tx pgx.Tx,
	fixture reconnectMigrationFixture,
	pauseID uuid.UUID,
	firstIntervalID *uuid.UUID,
	firstLiveState string,
	firstPresenceEpoch int64,
	firstPresenceRevision int64,
	action string,
	decidedAt time.Time,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO resume_decisions (
			pause_id, decision_number,
			first_participant_id, second_participant_id,
			first_pre_pause_state, second_pre_pause_state,
			first_live_state, second_live_state,
			first_presence_epoch, second_presence_epoch,
			first_presence_revision, second_presence_revision,
			first_reconnect_interval_id, second_reconnect_interval_id,
			action, decided_at, created_at
		)
		VALUES (
			$1, $2,
			$3, $4,
			'connected', 'connected',
			$5, $6,
			$7, $8,
			$9, $10,
			$11, $12,
			$13, $14, $14
		)`,
		pauseID,
		1,
		fixture.draft.participantIDs[0],
		fixture.draft.participantIDs[1],
		firstLiveState,
		"connected",
		firstPresenceEpoch,
		int64(1),
		firstPresenceRevision,
		int64(1),
		firstIntervalID,
		(*uuid.UUID)(nil),
		action,
		decidedAt,
	)
	return err
}
