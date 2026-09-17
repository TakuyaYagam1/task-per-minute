//go:build integration

package reconnect

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

func beginReconnectLockProbe(ctx context.Context, tb testing.TB) pgx.Tx {
	tb.Helper()

	tx, err := migrationPool.Begin(ctx)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, "SET LOCAL lock_timeout = '100ms'")
	require.NoError(tb, err)
	return tx
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

func assertReconnectIntervalRejected(
	ctx context.Context,
	tb testing.TB,
	fixture reconnectMigrationFixture,
	pauseID uuid.UUID,
	disconnectedAt time.Time,
) {
	tb.Helper()

	participantID := fixture.draft.participantIDs[0]
	_, err := migrationPool.Exec(ctx, `
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

	tx, err := migrationPool.Begin(ctx)
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

func assertPauseCancellationRejected(
	ctx context.Context,
	tb testing.TB,
	pauseID uuid.UUID,
	previousRevisionID uuid.UUID,
	cancelledAt time.Time,
	expected string,
) {
	tb.Helper()

	tx, err := migrationPool.Begin(ctx)
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

type reconnectIntervalSnapshot struct {
	id                 uuid.UUID
	pauseID            uuid.UUID
	rosterID           uuid.UUID
	seriesID           uuid.UUID
	gameAttemptID      uuid.UUID
	participantID      uuid.UUID
	presenceEpoch      int64
	intervalNumber     int
	continuationNumber int
	continuedFromID    pgtype.UUID
	suspendedByPauseID pgtype.UUID
	state              string
	openedAt           time.Time
	deadlineAt         time.Time
	closedAt           pgtype.Timestamptz
	revision           int64
	createdAt          time.Time
	updatedAt          time.Time
}

func loadReconnectIntervalSnapshot(
	ctx context.Context,
	tb testing.TB,
	intervalID uuid.UUID,
) reconnectIntervalSnapshot {
	tb.Helper()

	var snapshot reconnectIntervalSnapshot
	err := migrationPool.QueryRow(ctx, `
		SELECT
			id, pause_id, roster_id, series_id, game_attempt_id,
			participant_id, presence_epoch, interval_number,
			continuation_number, continued_from_id, suspended_by_pause_id,
			state, opened_at, deadline_at, closed_at,
			revision, created_at, updated_at
		FROM reconnect_intervals
		WHERE id = $1`, intervalID).Scan(
		&snapshot.id,
		&snapshot.pauseID,
		&snapshot.rosterID,
		&snapshot.seriesID,
		&snapshot.gameAttemptID,
		&snapshot.participantID,
		&snapshot.presenceEpoch,
		&snapshot.intervalNumber,
		&snapshot.continuationNumber,
		&snapshot.continuedFromID,
		&snapshot.suspendedByPauseID,
		&snapshot.state,
		&snapshot.openedAt,
		&snapshot.deadlineAt,
		&snapshot.closedAt,
		&snapshot.revision,
		&snapshot.createdAt,
		&snapshot.updatedAt,
	)
	require.NoError(tb, err)
	return snapshot
}
