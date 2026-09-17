package replay

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReplayReserveAuthoritySQLContract(t *testing.T) {
	t.Parallel()

	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "..", "db", "migrations", "000007_assignment_schema.sql"))
	require.NoError(t, err)
	query, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "..", "db", "queries", "tournament_admin_replay.sql"))
	require.NoError(t, err)

	for _, fragment := range []string{
		"CREATE TABLE public.replay_reserve_authorities",
		"CREATE TABLE public.replay_reserve_authority_pool_versions",
		"replay_reserve_authorities_revision_check",
		"replay_reserve_authorities_scope_key",
		"CREATE FUNCTION public.replay_reserve_authority_assignment_guard()",
		"CREATE CONSTRAINT TRIGGER replay_reserve_authority_assignment_guard",
		"replay authority pool does not match the immutable exact child source",
	} {
		require.Contains(t, string(migration), fragment)
	}
	for _, fragment := range []string{
		"-- name: LockReplayReserveAuthority :one",
		"-- name: LockReplayReserveAuthorityPool :many",
		"-- name: AdvanceReplayReserveAuthorityCAS :one",
		"FOR UPDATE OF authority",
		"AND authority.assignment_revision = sqlc.arg(expected_assignment_revision)",
		"JOIN replay_reserve_authority_pool_versions AS authority_pool",
		"candidate_version.content_digest AS candidate_content_digest",
		"candidate_version.category = authority.required_category",
		"AND candidate_task.enabled",
		"AND COALESCE(health.healthy, false)",
		"FROM task_delivery_receipts AS receipt",
	} {
		require.Contains(t, string(query), fragment)
	}
}

func TestReplayReserveAuthorityHasServerOwnedProducerContract(t *testing.T) {
	t.Parallel()

	query, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "..", "db", "queries", "tournament_admin_replay.sql"))
	require.NoError(t, err)

	for _, fragment := range []string{
		"-- name: CreateReplayReserveAuthority :one",
		"-- name: CreateReplayReserveAuthorityPoolVersion :exec",
		"INSERT INTO replay_reserve_authorities",
		"INSERT INTO replay_reserve_authority_pool_versions",
		"JOIN exact_draft_assignment_child_sources AS source",
		"JOIN exact_draft_assignment_child_candidates AS candidate",
		"source revisions, category, and reservation head all",
	} {
		require.Contains(t, string(query), fragment)
	}
}

func TestReplayWorkflowSourceLocksTerminalEvidenceWithDatabaseClock(t *testing.T) {
	t.Parallel()

	query, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "..", "db", "queries", "tournament_admin_replay.sql"))
	require.NoError(t, err)

	for _, fragment := range []string{
		"-- name: GetTournamentAdminReplayTime :one",
		"clock_timestamp()::TIMESTAMPTZ",
		"-- name: LockReplayWorkflowOldWaveExecution :many",
		"FOR UPDATE OF ready_window, member, readiness",
		"-- name: LockReplayWorkflowGameResultHeads :many",
		"FOR UPDATE OF result_head, result_revision, event",
		"-- name: LockReplayWorkflowScoreHead :one",
		"FOR UPDATE OF score_head, score_revision",
	} {
		require.Contains(t, string(query), fragment)
	}
}
