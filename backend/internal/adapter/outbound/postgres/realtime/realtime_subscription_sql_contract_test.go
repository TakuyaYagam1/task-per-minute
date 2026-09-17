package realtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRealtimeSubscriptionSQLContractUsesScopeBoundDurableResume(t *testing.T) {
	t.Parallel()

	query := string(readRealtimeSubscriptionQuery(t))
	for _, fragment := range []string{
		"-- name: OpenRealtimeSubscription :one",
		"WHERE subscriber.id = sqlc.narg(resume_id)::UUID",
		"subscriber.tournament_id = sqlc.arg(tournament_id)",
		"subscriber.role = sqlc.arg(role)",
		"subscriber.principal_id IS NOT DISTINCT FROM sqlc.narg(principal_id)::UUID",
		"FOR UPDATE OF subscriber",
		"connection_id = sqlc.arg(connection_id)",
		"connection_generation = subscriber.connection_generation + 1",
		"snapshot_sequence = GREATEST(",
		"subscriber.snapshot_sequence AS after_sequence",
		"WHERE sqlc.narg(resume_id)::UUID IS NULL",
		"CASE\n        WHEN terminal_event.id IS NULL THEN 'none'",
		"WHEN receipt.outcome = 'written' THEN 'written'",
		"ELSE 'pending'",
		"ORDER BY outbox_event.sequence DESC",
		"AND connection_id = sqlc.arg(connection_id)",
		"AND connection_generation = sqlc.arg(connection_generation)",
		"sqlc.arg(close_reason) IN ('tournament_terminal', 'terminal_already_written')",
	} {
		require.Contains(t, query, fragment)
	}
	require.NotContains(t, query, "-- name: OpenRealtimeSubscriber :one")
}

func readRealtimeSubscriptionQuery(t *testing.T) []byte {
	t.Helper()

	contents, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "db", "queries", "realtime_outbox.sql"))
	require.NoError(t, err)
	return contents
}

func TestRealtimeSubscriptionSchemaFencesCurrentConnection(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "db", "migrations", "000011_projection_schema.sql"))
	require.NoError(t, err)

	for _, fragment := range []string{
		"connection_id uuid NOT NULL",
		"connection_generation bigint NOT NULL",
		"snapshot_sequence bigint NOT NULL",
		"realtime_subscribers_identity_check CHECK",
		"connection_id <> '00000000-0000-0000-0000-000000000000'::uuid",
		"NEW.connection_generation = OLD.connection_generation + 1",
		"NEW.connection_id IS DISTINCT FROM OLD.connection_id",
		"NEW.snapshot_sequence = GREATEST(",
		"OLD.snapshot_sequence,",
		"NEW.last_acknowledged_sequence",
	} {
		require.Contains(t, string(schema), fragment)
	}
}
