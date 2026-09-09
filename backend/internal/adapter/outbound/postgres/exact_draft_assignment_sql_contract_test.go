package postgres

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExactDraftAssignmentStorageRetainsEveryBranchAndCategoryChild(t *testing.T) {
	t.Parallel()

	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "migrations", "000007_assignment_schema.sql"))
	require.NoError(t, err)
	projectionMigration, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "migrations", "000011_projection_schema.sql"))
	require.NoError(t, err)
	assignmentQueries, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "queries", "assignment.sql"))
	require.NoError(t, err)
	queries, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "queries", "exact_draft_assignment.sql"))
	require.NoError(t, err)

	for _, fragment := range []string{
		"CREATE TABLE public.exact_draft_assignment_branches",
		"CREATE TABLE public.exact_draft_assignment_child_sources",
		"CREATE TABLE public.exact_draft_assignment_child_participants",
		"CREATE TABLE public.exact_draft_assignment_child_history",
		"CREATE TABLE public.exact_draft_assignment_child_candidates",
		"exact draft child source is outside its final draft authority",
		"exact draft child participant is outside its draft authority",
		"exact draft child history must match a delivered task",
		"exact_draft_assignment_child_participant_guard",
		"exact_draft_assignment_child_history_guard",
		"kind)::text = ANY ((ARRAY['conservative'::character varying, 'exact'::character varying, 'exact_draft'::character varying])::text[])",
		"assignment_branches_exact_draft_position_key UNIQUE",
		"exact_draft_branch_id,\n        exact_draft_position",
		"assignment_branches_exact_draft_group_fk",
		"assignment_plans_active_draft_branch_fk",
		"assignment_plans_completion_draft_revision_fk",
		"assignment_plans_activation_command_key",
		"completion_draft_revision_id",
		"activation_command_id",
		"completed_categories",
		"proof_hash character varying(64)",
		"exact_draft_assignment_branches_one_active_idx",
		"exact_draft_assignment_child_sources_plan_idx",
		"exact_draft_assignment_child_participants_plan_idx",
		"exact_draft_assignment_child_history_plan_idx",
		"exact_draft_assignment_child_candidates_plan_idx",
		"exact draft plan must commit one full category branch and release every losing branch",
		"exact draft branch must retain one child for each category position",
		"active exact draft branch requires every child reservation committed",
		"released exact draft branch must release every undisclosed child reservation",
		"contingency_draft_branch_id uuid",
		"task_version_reservations_contingency_draft_branch_fk",
		"FOREIGN KEY (contingency_draft_branch_id, plan_id)",
		"assignment_plan_edges_branch_version_key UNIQUE (branch_id, task_id, task_version)",
		"task_version_reservations_contingency_active_idx",
		"pg_advisory_xact_lock",
		"reservation contingency group must match its assignment branch",
		"task version conflicts with a non-contingent or committed reservation",
		"committed task version cannot retain another live reservation",
		"delivery receipt participant is outside its roster",
		"FOR UPDATE;\n\n    IF NOT FOUND THEN\n        RAISE EXCEPTION 'delivery receipt participant is outside its roster'",
		"series_revision bigint NOT NULL",
		"roster_revision bigint NOT NULL",
	} {
		require.Contains(t, string(migration), fragment)
	}
	require.NotContains(t, string(migration), "slot_revision_id")
	require.NotContains(t, string(migration), "graph_revision_id")
	require.NotContains(t, string(migration), "slot_initial_revision")
	for _, fragment := range []string{
		"-- name: CreateAssignmentTaskVersionReservation :one",
		"contingency_draft_branch_id",
		"FROM assignment_branches AS branch",
		"branch.exact_draft_branch_id",
	} {
		require.Contains(t, string(assignmentQueries), fragment)
	}
	for _, fragment := range []string{
		"CREATE TABLE public.final_draft_delivery_history_heads",
		"final_draft_delivery_history_heads_stage_fk",
		"tournament_stage_playoff_finals_draft_scope_key",
		"CREATE FUNCTION public.final_draft_delivery_history_head_guard()",
		"CREATE TRIGGER final_draft_delivery_history_head_guard",
		"AFTER INSERT ON public.task_delivery_receipts",
		"revision_id = gen_random_uuid()",
	} {
		require.Contains(t, string(projectionMigration), fragment)
	}

	for _, fragment := range []string{
		"-- name: LockExactDraftPlanningStage :one",
		"-- name: LockExactDraftPlanningParticipants :many",
		"-- name: LockExactDraftPlanningHistory :many",
		"-- name: EnsureExactDraftPlanningHistoryHead :exec",
		"-- name: LockExactDraftPlanningHistoryHead :one",
		"INSERT INTO final_draft_delivery_history_heads",
		"FOR UPDATE OF head",
		"-- name: LockExactDraftPlanningCandidates :many",
		"FOR UPDATE OF stage, evidence, final_series, roster, category, pool, projection, bracket",
		"FOR UPDATE OF participant, reservation",
		"FOR KEY SHARE OF receipt",
		"FOR UPDATE OF membership, task_version, task",
		"-- name: LockExactDraftAssignmentSource :one",
		"FOR UPDATE OF plan, draft, stage, final_series, roster",
		"FOR UPDATE",
		"-- name: CommitExactDraftChildReservations :many",
		"-- name: CreateExactDraftAssignmentChildSource :exec",
		"-- name: CreateExactDraftAssignmentChildParticipant :exec",
		"-- name: CreateExactDraftAssignmentChildHistory :exec",
		"-- name: CreateExactDraftAssignmentChildCandidate :exec",
		"-- name: LockExactDraftAssignmentChildSources :many",
		"-- name: LockExactDraftAssignmentChildParticipants :many",
		"-- name: LockExactDraftAssignmentChildHistory :many",
		"-- name: LockExactDraftAssignmentChildCandidates :many",
		"-- name: ReleaseLosingExactDraftChildReservations :many",
		"-- name: ActivateExactDraftChildren :many",
		"-- name: ReleaseLosingExactDraftChildren :many",
		"-- name: CommitExactDraftAssignmentPlan :execrows",
		"AND plan.source_draft_revision_id = sqlc.arg(expected_draft_revision_id)",
		"completion_draft_revision_id = sqlc.arg(completion_draft_revision_id)",
		"activation_command_id = sqlc.arg(activation_command_id)",
		"completion.selected_categories = sqlc.arg(completed_categories)",
		"NOT EXISTS (\n                SELECT 1\n                FROM draft_revisions AS later",
	} {
		require.Contains(t, string(queries), fragment)
	}
}
