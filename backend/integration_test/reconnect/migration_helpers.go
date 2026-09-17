//go:build integration

package reconnect

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func insertResumeDecision(
	ctx context.Context,
	tb testing.TB,
	fixture reconnectMigrationFixture,
	pauseID uuid.UUID,
	decisionNumber int,
	firstIntervalID *uuid.UUID,
	secondIntervalID *uuid.UUID,
	action string,
	decidedAt time.Time,
) {
	tb.Helper()
	type presenceEvidence struct {
		state    string
		epoch    int64
		revision int64
	}
	presence := make([]presenceEvidence, 2)
	for i, participantID := range fixture.draft.participantIDs {
		err := migrationPool.QueryRow(ctx, `
			SELECT state, presence_epoch, revision
			FROM presence_states
			WHERE series_id = $1 AND participant_id = $2`, fixture.draft.seriesID, participantID).
			Scan(&presence[i].state, &presence[i].epoch, &presence[i].revision)
		require.NoError(tb, err)
	}
	_, err := migrationPool.Exec(ctx, `
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
			$1, $2, $3, $4,
			'connected', 'connected', $5, $6,
			$7, $8, $9, $10, $11, $12, $13, $14, $14
		)`,
		pauseID, decisionNumber, fixture.draft.participantIDs[0], fixture.draft.participantIDs[1],
		presence[0].state, presence[1].state, presence[0].epoch, presence[1].epoch,
		presence[0].revision, presence[1].revision, firstIntervalID, secondIntervalID, action, decidedAt,
	)
	require.NoError(tb, err)
}

func disconnectParticipant(
	ctx context.Context,
	tb testing.TB,
	fixture reconnectMigrationFixture,
	participantID uuid.UUID,
	disconnectedAt time.Time,
	window time.Duration,
) (uuid.UUID, time.Time) {
	tb.Helper()
	return disconnectParticipantAtInterval(ctx, tb, fixture, participantID, 1, disconnectedAt, window)
}

func disconnectParticipantAtInterval(
	ctx context.Context,
	tb testing.TB,
	fixture reconnectMigrationFixture,
	participantID uuid.UUID,
	intervalNumber int,
	disconnectedAt time.Time,
	window time.Duration,
) (uuid.UUID, time.Time) {
	tb.Helper()
	intervalID := uuid.New()
	deadline := disconnectedAt.Add(window)
	tx, err := migrationPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `SELECT 1 FROM pauses WHERE id = $1 FOR UPDATE`, fixture.gamePauseID)
	require.NoError(tb, err)
	var presenceEpoch int64
	err = tx.QueryRow(ctx, `
		UPDATE presence_states
		SET state = 'disconnected', presence_epoch = presence_epoch + 1,
			revision = revision + 1, disconnected_at = $3, updated_at = $3
		WHERE series_id = $1 AND participant_id = $2
		RETURNING presence_epoch`, fixture.draft.seriesID, participantID, disconnectedAt).Scan(&presenceEpoch)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO reconnect_intervals (
			id, pause_id, roster_id, series_id, game_attempt_id,
			participant_id, presence_epoch, interval_number,
			opened_at, deadline_at, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $9, $9)`,
		intervalID, fixture.gamePauseID, fixture.draft.rosterID, fixture.draft.seriesID,
		fixture.attemptID, participantID, presenceEpoch, intervalNumber, disconnectedAt, deadline)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, `
		UPDATE reconnect_slot_counters
		SET slots_used = slots_used + 1, revision = revision + 1, updated_at = $3
		WHERE pause_id = $1 AND participant_id = $2`, fixture.gamePauseID, participantID, disconnectedAt)
	require.NoError(tb, err)
	require.NoError(tb, tx.Commit(ctx))
	return intervalID, deadline
}

func reconnectParticipant(
	ctx context.Context,
	tb testing.TB,
	fixture reconnectMigrationFixture,
	participantID uuid.UUID,
	intervalID uuid.UUID,
	reconnectedAt time.Time,
) {
	tb.Helper()
	tx, err := migrationPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		UPDATE reconnect_intervals
		SET state = 'reconnected', closed_at = $2, revision = revision + 1, updated_at = $2
		WHERE id = $1`, intervalID, reconnectedAt)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, `
		UPDATE presence_states
		SET state = 'connected', presence_epoch = presence_epoch + 1,
			revision = revision + 1, connected_at = $3, disconnected_at = NULL, updated_at = $3
		WHERE series_id = $1 AND participant_id = $2`, fixture.draft.seriesID, participantID, reconnectedAt)
	require.NoError(tb, err)
	require.NoError(tb, tx.Commit(ctx))
}

func assertParentPauseWaitsForChild(ctx context.Context, tb testing.TB, fixture reconnectMigrationFixture) {
	tb.Helper()
	tx, err := migrationPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()
	revisionID := uuid.New()
	resolvedAt := fixture.pausedAt.Add(time.Second)
	_, err = tx.Exec(ctx, `
		INSERT INTO pause_revisions (
			id, pause_id, previous_revision_id, revision_number,
			state, transition_reason, created_at
		)
		VALUES ($1, $2, $3, 2, 'resumed', 'operator resume', $4)`,
		revisionID, fixture.rootPauseID, fixture.rootPauseRevisionID, resolvedAt)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, `
		UPDATE pauses
		SET state = 'resumed', current_revision_id = $2, revision = revision + 1,
			resolved_at = $3, updated_at = $3
		WHERE id = $1`, fixture.rootPauseID, revisionID, resolvedAt)
	require.Error(tb, err)
}

func resumeMigrationPause(ctx context.Context, tb testing.TB, fixture reconnectMigrationFixture, gamePause bool, resumedAt time.Time) {
	tb.Helper()
	pauseID := fixture.rootPauseID
	previousRevisionID := fixture.rootPauseRevisionID
	if gamePause {
		pauseID = fixture.gamePauseID
		previousRevisionID = fixture.gamePauseRevisionID
	}
	revisionID := uuid.New()
	tx, err := migrationPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		INSERT INTO pause_revisions (
			id, pause_id, previous_revision_id, revision_number,
			state, transition_reason, created_at
		)
		VALUES ($1, $2, $3, 2, 'resumed', 'presence restored', $4)`,
		revisionID, pauseID, previousRevisionID, resumedAt)
	require.NoError(tb, err)
	if gamePause {
		_, err = tx.Exec(ctx, `
			UPDATE pause_clocks
			SET resumed_at = $2, resumed_deadline = $2::TIMESTAMPTZ + INTERVAL '5 minutes',
				revision = revision + 1, updated_at = $2
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
		SET state = 'resumed', current_revision_id = $2, revision = revision + 1,
			resolved_at = $3, updated_at = $3
		WHERE id = $1`, pauseID, revisionID, resumedAt)
	require.NoError(tb, err)
	require.NoError(tb, tx.Commit(ctx))
}

func assertReconnectPersistence(ctx context.Context, tb testing.TB, fixture reconnectMigrationFixture) {
	tb.Helper()
	var intervalCount, usedSlots, decisionCount int
	var remainingMS int64
	var resumedAt, deadline time.Time
	err := migrationPool.QueryRow(ctx, `SELECT COUNT(*) FROM reconnect_intervals WHERE pause_id = $1`, fixture.gamePauseID).Scan(&intervalCount)
	require.NoError(tb, err)
	require.Equal(tb, 2, intervalCount)
	err = migrationPool.QueryRow(ctx, `SELECT SUM(slots_used) FROM reconnect_slot_counters WHERE pause_id = $1`, fixture.gamePauseID).Scan(&usedSlots)
	require.NoError(tb, err)
	require.Equal(tb, 2, usedSlots)
	err = migrationPool.QueryRow(ctx, `SELECT COUNT(*) FROM resume_decisions WHERE pause_id = $1`, fixture.gamePauseID).Scan(&decisionCount)
	require.NoError(tb, err)
	require.Equal(tb, 5, decisionCount)
	err = migrationPool.QueryRow(ctx, `SELECT frozen_remaining_ms, resumed_at, resumed_deadline FROM pause_clocks WHERE pause_id = $1`, fixture.gamePauseID).Scan(&remainingMS, &resumedAt, &deadline)
	require.NoError(tb, err)
	require.EqualValues(tb, 300000, remainingMS)
	require.Equal(tb, resumedAt.Add(5*time.Minute), deadline)
}

func assertReconnectCAS(ctx context.Context, tb testing.TB, fixture reconnectMigrationFixture) {
	tb.Helper()
	commandTag, err := migrationPool.Exec(ctx, `
		UPDATE presence_states
		SET state = 'disconnected', presence_epoch = presence_epoch + 1,
			revision = revision + 1, disconnected_at = $3, updated_at = $3
		WHERE series_id = $1 AND participant_id = $2 AND revision = 1`,
		fixture.draft.seriesID, fixture.draft.participantIDs[0], fixture.pausedAt.Add(10*time.Second))
	require.NoError(tb, err)
	require.Zero(tb, commandTag.RowsAffected())
	commandTag, err = migrationPool.Exec(ctx, `
		UPDATE reconnect_intervals
		SET state = 'expired', closed_at = $2, revision = revision + 1, updated_at = $2
		WHERE id = $1 AND revision = 1`, fixture.firstIntervalID, fixture.pausedAt.Add(10*time.Second))
	require.NoError(tb, err)
	require.Zero(tb, commandTag.RowsAffected())
	commandTag, err = migrationPool.Exec(ctx, `
		UPDATE pauses SET updated_at = $2 WHERE id = $1 AND revision = 1`, fixture.gamePauseID, fixture.pausedAt.Add(10*time.Second))
	require.NoError(tb, err)
	require.Zero(tb, commandTag.RowsAffected())
	_, err = migrationPool.Exec(ctx, `UPDATE game_attempts SET updated_at = $2 WHERE id = $1`, fixture.attemptID, fixture.pausedAt.Add(10*time.Second))
	require.Error(tb, err)
	_, err = migrationPool.Exec(ctx, `UPDATE resume_decisions SET action = 'wait_both' WHERE pause_id = $1 AND decision_number = 5`, fixture.gamePauseID)
	require.Error(tb, err)
}
