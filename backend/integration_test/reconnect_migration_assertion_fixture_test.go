//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/stretchr/testify/require"
)

func assertParentPauseWaitsForChild(
	ctx context.Context, tb testing.TB,
	fixture reconnectMigrationFixture,
) {
	tb.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()
	revisionID := uuid.New()
	resolvedAt := fixture.pausedAt.Add(time.Second)
	_, err = tx.Exec(
		ctx, `
		INSERT INTO pause_revisions (
			id, pause_id, previous_revision_id, revision_number,
			state, transition_reason, created_at
		)
		VALUES ($1, $2, $3, 2, 'resumed', 'operator resume', $4)`,
		revisionID,
		fixture.rootPauseID,
		fixture.rootPauseRevisionID,
		resolvedAt,
	)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, `
		UPDATE pauses
		SET state = 'resumed',
			current_revision_id = $2,
			revision = revision + 1,
			resolved_at = $3,
			updated_at = $3
		WHERE id = $1`, fixture.rootPauseID, revisionID, resolvedAt)
	require.Error(tb, err)
}

func resumeMigrationPause(
	ctx context.Context, tb testing.TB,
	fixture reconnectMigrationFixture,
	gamePause bool,
	resumedAt time.Time,
) {
	tb.Helper()

	pauseID := fixture.rootPauseID
	previousRevisionID := fixture.rootPauseRevisionID
	if gamePause {
		pauseID = fixture.gamePauseID
		previousRevisionID = fixture.gamePauseRevisionID
	}
	revisionID := uuid.New()
	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(
		ctx, `
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
	if gamePause {
		_, err = tx.Exec(ctx, `
			UPDATE pause_clocks
			SET resumed_at = $2,
				resumed_deadline = $2::TIMESTAMPTZ + INTERVAL '5 minutes',
				revision = revision + 1,
				updated_at = $2
			WHERE pause_id = $1`, pauseID, resumedAt)
		require.NoError(tb, err)
		_, err = tx.Exec(ctx, `
			UPDATE game_attempts
			SET state = 'active', revision = revision + 1, updated_at = $2
			WHERE id = $1`, fixture.attemptID, resumedAt)
		require.NoError(tb, err)
	}
	_, err = tx.Exec(ctx, `
		UPDATE pauses
		SET state = 'resumed',
			current_revision_id = $2,
			revision = revision + 1,
			resolved_at = $3,
			updated_at = $3
		WHERE id = $1`, pauseID, revisionID, resumedAt)
	require.NoError(tb, err)
	require.NoError(tb, tx.Commit(ctx))
}

func assertReconnectPersistence(
	ctx context.Context, tb testing.TB,
	fixture reconnectMigrationFixture,
) {
	tb.Helper()

	var (
		intervalCount int
		usedSlots     int
		decisionCount int
		remainingMS   int64
		resumedAt     time.Time
		deadline      time.Time
	)
	err := sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM reconnect_intervals
		WHERE pause_id = $1`, fixture.gamePauseID).Scan(&intervalCount)
	require.NoError(tb, err)
	require.Equal(tb, 2, intervalCount)
	err = sharedPool.QueryRow(ctx, `
		SELECT SUM(slots_used)
		FROM reconnect_slot_counters
		WHERE pause_id = $1`, fixture.gamePauseID).Scan(&usedSlots)
	require.NoError(tb, err)
	require.Equal(tb, 2, usedSlots)
	err = sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM resume_decisions
		WHERE pause_id = $1`, fixture.gamePauseID).Scan(&decisionCount)
	require.NoError(tb, err)
	require.Equal(tb, 5, decisionCount)
	err = sharedPool.QueryRow(ctx, `
		SELECT frozen_remaining_ms, resumed_at, resumed_deadline
		FROM pause_clocks
		WHERE pause_id = $1`, fixture.gamePauseID).Scan(&remainingMS, &resumedAt, &deadline)
	require.NoError(tb, err)
	require.EqualValues(tb, 300000, remainingMS)
	require.Equal(tb, resumedAt.Add(5*time.Minute), deadline)
}

func assertReconnectCAS(
	ctx context.Context, tb testing.TB,
	fixture reconnectMigrationFixture,
) {
	tb.Helper()

	commandTag, err := sharedPool.Exec(
		ctx, `
		UPDATE presence_states
		SET state = 'disconnected',
			presence_epoch = presence_epoch + 1,
			revision = revision + 1,
			disconnected_at = $3,
			updated_at = $3
		WHERE series_id = $1 AND participant_id = $2 AND revision = 1`,
		fixture.draft.seriesID,
		fixture.draft.participantIDs[0],
		fixture.pausedAt.Add(10*time.Second),
	)
	require.NoError(tb, err)
	require.Zero(tb, commandTag.RowsAffected())

	commandTag, err = sharedPool.Exec(
		ctx, `
		UPDATE reconnect_intervals
		SET state = 'expired',
			closed_at = $2,
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1 AND revision = 1`,
		fixture.firstIntervalID,
		fixture.pausedAt.Add(10*time.Second),
	)
	require.NoError(tb, err)
	require.Zero(tb, commandTag.RowsAffected())

	commandTag, err = sharedPool.Exec(
		ctx, `
		UPDATE pauses
		SET updated_at = $2
		WHERE id = $1 AND revision = 1`,
		fixture.gamePauseID,
		fixture.pausedAt.Add(10*time.Second),
	)
	require.NoError(tb, err)
	require.Zero(tb, commandTag.RowsAffected())

	_, err = sharedPool.Exec(ctx, `
		UPDATE game_attempts
		SET updated_at = $2
		WHERE id = $1`, fixture.attemptID, fixture.pausedAt.Add(10*time.Second))
	require.Error(tb, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE resume_decisions
		SET action = 'wait_both'
		WHERE pause_id = $1 AND decision_number = 5`, fixture.gamePauseID)
	require.Error(tb, err)
}
