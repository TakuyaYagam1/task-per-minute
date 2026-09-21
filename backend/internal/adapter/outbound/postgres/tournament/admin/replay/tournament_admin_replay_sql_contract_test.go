package replay

import (
	"os"
	"path/filepath"
	"strings"
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
		"contingency_draft_branch_id",
		"sqlc.narg(contingency_draft_branch_id)::UUID",
	} {
		require.Contains(t, string(query), fragment)
	}
}

func TestReplayReserveSnapshotDigestMigrationKeepsContentBinding(t *testing.T) {
	t.Parallel()

	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "..", "db", "migrations", "000026_replay_reserve_snapshot_digest.sql"))
	require.NoError(t, err)

	up, _, found := strings.Cut(string(migration), "-- +goose Down")
	require.True(t, found)
	require.Contains(t, up, "CREATE OR REPLACE FUNCTION public.replay_command_source_guard()")
	require.Contains(t, up, "CREATE OR REPLACE FUNCTION public.replay_command_target_guard()")
	require.NotContains(t, up, "candidate_version.content_digest = NEW.content_digest")
	require.Contains(t, up, "snapshot.content_digest IS NOT DISTINCT FROM NEW.content_digest")
	for _, field := range []string{
		"snapshot.task_id = NEW.proposed_task_id",
		"snapshot.task_version = NEW.proposed_version",
		"snapshot.title IS NOT DISTINCT FROM candidate_version.title",
		"snapshot.description IS NOT DISTINCT FROM candidate_version.description",
		"snapshot.category IS NOT DISTINCT FROM candidate_version.category",
		"snapshot.difficulty IS NOT DISTINCT FROM candidate_version.difficulty",
		"snapshot.time_limit IS NOT DISTINCT FROM candidate_version.time_limit",
		"snapshot.flag IS NOT DISTINCT FROM candidate_version.flag",
		"snapshot.task_url IS NOT DISTINCT FROM candidate_version.task_url",
		"snapshot.source_file_url IS NOT DISTINCT FROM candidate_version.source_file_url",
		"COALESCE(candidate_version.hint_1, '')",
		"COALESCE(candidate_version.hint_2, '')",
		"COALESCE(candidate_version.hint_3, '')",
	} {
		require.Contains(t, up, field)
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
		"-- name: SupersedeReplaySourceWaveCAS :one",
		"AND source_wave.state = 'completed'",
	} {
		require.Contains(t, string(query), fragment)
	}
}
