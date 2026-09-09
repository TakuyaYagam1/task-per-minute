package postgres

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTerminalSwissNormalizationPinsOrdinaryOperatorForfeit(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "queries", "projection.sql"))
	require.NoError(t, err)
	query := strings.Split(strings.Split(string(data), "-- name: LockTerminalProjectionCommit :many")[1], "-- name:")[0]
	for _, exact := range []string{
		"FROM result_commits AS commit", "event.attempt_id = commit.attempt_id", "audit.result_event_id = event.id",
		"commit.series_result_revision_id IS NOT NULL", "audit.actor_kind = 'operator'", "event.result_reason = 'operator_forfeit'",
		"commit.projection_evidence_id = sqlc.arg(projection_revision_id)::uuid",
	} {
		require.Contains(t, query, exact)
	}
}

func TestFinalSwissReceiptChildrenRevalidateAtCommit(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "migrations", "000011_projection_schema.sql"))
	require.NoError(t, err)
	for _, child := range []string{"participants", "rounds", "series", "games", "ledger_entries"} {
		require.Contains(t, string(data), "CREATE CONSTRAINT TRIGGER final_swiss_projection_receipt_"+child+"_complete\n"+
			"AFTER INSERT ON public.final_swiss_projection_receipt_"+child+"\nDEFERRABLE INITIALLY DEFERRED\n"+
			"FOR EACH ROW EXECUTE FUNCTION public.validate_final_swiss_projection_receipt();")
	}
}
