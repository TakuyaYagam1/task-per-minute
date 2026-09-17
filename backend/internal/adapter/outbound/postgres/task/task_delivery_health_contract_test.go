package task

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrivateTaskAvailabilityQueryUsesOnlyDurableReceiptAvailability(t *testing.T) {
	t.Parallel()

	query, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "db", "queries", "task_delivery_health.sql"))
	require.NoError(t, err)
	text := string(query)
	for _, fragment := range []string{
		"-- name: GetPrivateTaskDeliveryBacklog :one",
		"task_delivery_receipts AS receipt",
		"assignment.task_version",
		"public.participant_task_instance_id(assignment.id, participant.participant_id) AS instance_id",
		"receipt.task_version = expected.task_version",
		"receipt.instance_id = expected.instance_id",
		"wave.state = 'active'",
		"series.state = 'active'",
		"attempt.state = 'active'",
		"assignment.state = 'active'",
		"COUNT(*)::BIGINT AS pending_count",
		"MIN(started_at)::TIMESTAMPTZ AS oldest_pending_at",
	} {
		require.Contains(t, text, fragment)
	}
	require.NotContains(t, text, "outbox_events")
	require.NotContains(t, text, "realtime_delivery_receipts")
	require.NotContains(t, text, "tournament.state")
	require.NotContains(t, text, "flag")
	require.NotContains(t, text, "source_file_url")

	migration, err := os.ReadFile(filepath.Join(
		"..", "..", "..", "..", "..", "db", "migrations", "000007_assignment_schema.sql",
	))
	require.NoError(t, err)
	for _, fragment := range []string{
		"CREATE FUNCTION public.participant_task_instance_id(assignment_id UUID, participant_id UUID)",
		"digest(uuid_send($1) || uuid_send($2), 'sha1')",
		"NEW.instance_id IS DISTINCT FROM expected_instance_id",
		"delivery receipt must match assignment and deterministic instance identity",
	} {
		require.Contains(t, string(migration), fragment)
	}
}
