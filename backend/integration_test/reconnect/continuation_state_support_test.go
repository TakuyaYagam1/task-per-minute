//go:build integration

package reconnect

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

func disconnectPresence(
	ctx context.Context,
	tb testing.TB,
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

func requireReconnectCheckViolation(tb testing.TB, err error, diagnostic string) {
	tb.Helper()

	var postgresError *pgconn.PgError
	require.ErrorAs(tb, err, &postgresError)
	require.Equal(tb, "23514", postgresError.Code)
	require.Contains(tb, postgresError.Message, diagnostic)
}

func requireReconnectExactCheckViolation(tb testing.TB, err error, diagnostic string) {
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
