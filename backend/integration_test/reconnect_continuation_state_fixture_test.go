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

func disconnectPresence(
	ctx context.Context, tb testing.TB,
	fixture reconnectMigrationFixture,
	participantID uuid.UUID,
	disconnectedAt time.Time,
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
		WHERE series_id = $1 AND participant_id = $2`,
		fixture.draft.seriesID,
		participantID,
		disconnectedAt,
	)
	require.NoError(tb, err)
	require.EqualValues(tb, 1, commandTag.RowsAffected())
}

func loadReconnectIntervalSnapshot(
	ctx context.Context, tb testing.TB,
	intervalID uuid.UUID,
) reconnectIntervalSnapshot {
	tb.Helper()

	var snapshot reconnectIntervalSnapshot
	err := sharedPool.QueryRow(ctx, `
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

func requireReconnectCheckViolation(
	tb testing.TB,
	err error,
	diagnostic string,
) {
	tb.Helper()

	var postgresError *pgconn.PgError
	require.ErrorAs(tb, err, &postgresError)
	require.Equal(tb, "23514", postgresError.Code)
	require.Contains(tb, postgresError.Message, diagnostic)
}

func requireReconnectExactCheckViolation(
	tb testing.TB,
	err error,
	diagnostic string,
) {
	tb.Helper()

	var postgresError *pgconn.PgError
	require.ErrorAs(tb, err, &postgresError)
	require.Equal(tb, "23514", postgresError.Code)
	require.Equal(tb, diagnostic, postgresError.Message)
}

func requireReconnectPostgresError(
	tb testing.TB,
	err error,
	code string,
	constraint string,
	diagnostic string,
) {
	tb.Helper()

	var postgresError *pgconn.PgError
	require.ErrorAs(tb, err, &postgresError)
	require.Equal(tb, code, postgresError.Code)
	if constraint != "" {
		require.Equal(tb, constraint, postgresError.ConstraintName)
	}
	if diagnostic != "" {
		require.Contains(tb, postgresError.Message, diagnostic)
	}
}
