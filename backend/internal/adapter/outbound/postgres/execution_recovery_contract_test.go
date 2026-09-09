package postgres

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExecutionRecoveryRetainsImmutableEpochEvidenceAndLatestFence(t *testing.T) {
	t.Parallel()

	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "migrations", "000005_game_schema.sql"))
	require.NoError(t, err)
	queries, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "queries", "execution_recovery.sql"))
	require.NoError(t, err)

	for _, fragment := range []string{
		"CREATE TABLE public.execution_game_epochs",
		"CREATE TABLE public.execution_game_epoch_rebinds",
		"CREATE TABLE public.execution_epoch_replays",
		"authority_holder_id uuid NOT NULL",
		"authority_lease_id uuid NOT NULL",
		"authority_epoch bigint NOT NULL",
		"authority_revision bigint NOT NULL",
		"expected_lease_revision bigint NOT NULL",
		"command_digest bytea NOT NULL",
		"record_document jsonb NOT NULL",
		"execution_epoch_replays_game_attempt_key UNIQUE (game_attempt_id)",
		"execution_game_epochs_guard",
		"execution_game_epoch_rebinds_guard",
		"execution_epoch_replays_guard",
		"Execution epoch evidence is immutable",
		"Execution epoch rebind evidence is immutable",
		"execution_game_epochs_authority_fk",
		"execution_game_epoch_rebinds_previous_authority_fk",
		"execution_game_epoch_rebinds_authority_fk",
		"execution_game_epoch_rebinds_command_fk",
		"wave_control_commands_command_tournament_key",
		"execution_epoch_rebind_command_guard",
		"Execution epoch rebind requires its durable resume command",
		"execution_epoch_replays_current_authority_fk",
	} {
		require.Contains(t, string(migration), fragment)
	}
	for _, fragment := range []string{
		"-- name: ListExecutionRecoveryTournaments :many",
		"-- name: ListExecutionRecoveryGames :many",
		"-- name: LockExecutionEpochReplayFence :one",
		"-- name: CreateExecutionEpochReplay :one",
		"lease.revision = (\n            SELECT MAX(latest.revision)",
		"ORDER BY lease.revision DESC\n    LIMIT 1\n    FOR UPDATE",
		"attempt.state IN ('active', 'paused')",
		"attempt.state = 'active'",
		"tournament.state = 'swiss'",
		"execution_game_epoch_rebinds AS successor",
		"clock_timestamp() < current_authority.expires_at",
		"FOR UPDATE OF attempt, epoch, assignment",
	} {
		require.Contains(t, string(queries), fragment)
	}
}
