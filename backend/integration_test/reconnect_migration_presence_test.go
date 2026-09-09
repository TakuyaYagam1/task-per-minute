//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/stretchr/testify/require"
)

func insertResumeDecision(
	ctx context.Context, tb testing.TB,
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
		err := sharedPool.QueryRow(
			ctx, `
			SELECT state, presence_epoch, revision
			FROM presence_states
			WHERE series_id = $1 AND participant_id = $2`,
			fixture.draft.seriesID,
			participantID,
		).Scan(&presence[i].state, &presence[i].epoch, &presence[i].revision)
		require.NoError(tb, err)
	}

	_, err := sharedPool.Exec(
		ctx, `
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
		decisionNumber,
		fixture.draft.participantIDs[0],
		fixture.draft.participantIDs[1],
		presence[0].state,
		presence[1].state,
		presence[0].epoch,
		presence[1].epoch,
		presence[0].revision,
		presence[1].revision,
		firstIntervalID,
		secondIntervalID,
		action,
		decidedAt,
	)
	require.NoError(tb, err)
}

func disconnectParticipant(
	ctx context.Context, tb testing.TB,
	fixture reconnectMigrationFixture,
	participantID uuid.UUID,
	disconnectedAt time.Time,
	window time.Duration,
) (uuid.UUID, time.Time) {
	tb.Helper()
	return disconnectParticipantAtInterval(
		ctx, tb,
		fixture,
		participantID,
		1,
		disconnectedAt,
		window,
	)
}

func disconnectParticipantAtInterval(
	ctx context.Context, tb testing.TB,
	fixture reconnectMigrationFixture,
	participantID uuid.UUID,
	intervalNumber int,
	disconnectedAt time.Time,
	window time.Duration,
) (uuid.UUID, time.Time) {
	tb.Helper()

	intervalID := uuid.New()
	deadline := disconnectedAt.Add(window)
	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		SELECT 1
		FROM pauses
		WHERE id = $1
		FOR UPDATE`, fixture.gamePauseID)
	require.NoError(tb, err)
	var presenceEpoch int64
	err = tx.QueryRow(
		ctx, `
		UPDATE presence_states
		SET state = 'disconnected',
			presence_epoch = presence_epoch + 1,
			revision = revision + 1,
			disconnected_at = $3,
			updated_at = $3
		WHERE series_id = $1 AND participant_id = $2
		RETURNING presence_epoch`,
		fixture.draft.seriesID,
		participantID,
		disconnectedAt,
	).Scan(&presenceEpoch)
	require.NoError(tb, err)
	_, err = tx.Exec(
		ctx, `
		INSERT INTO reconnect_intervals (
			id, pause_id, roster_id, series_id, game_attempt_id,
			participant_id, presence_epoch, interval_number,
			opened_at, deadline_at, created_at, updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8,
			$9, $10, $9, $9
		)`,
		intervalID,
		fixture.gamePauseID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		fixture.attemptID,
		participantID,
		presenceEpoch,
		intervalNumber,
		disconnectedAt,
		deadline,
	)
	require.NoError(tb, err)
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
	require.NoError(tb, err)
	require.NoError(tb, tx.Commit(ctx))
	return intervalID, deadline
}

func reconnectParticipant(
	ctx context.Context, tb testing.TB,
	fixture reconnectMigrationFixture,
	participantID uuid.UUID,
	intervalID uuid.UUID,
	reconnectedAt time.Time,
) {
	tb.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		UPDATE reconnect_intervals
		SET state = 'reconnected',
			closed_at = $2,
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, intervalID, reconnectedAt)
	require.NoError(tb, err)
	_, err = tx.Exec(
		ctx, `
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
		reconnectedAt,
	)
	require.NoError(tb, err)
	require.NoError(tb, tx.Commit(ctx))
}
