package postgres

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTaskContentHealthAuthoritySQLContract(t *testing.T) {
	t.Parallel()

	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "migrations", "000002_task_schema.sql"))
	require.NoError(t, err)
	query, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "queries", "task_content.sql"))
	require.NoError(t, err)
	taskSchema, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "api", "components", "schemas", "task_schemas.yml"))
	require.NoError(t, err)

	for _, fragment := range []string{
		"CREATE TABLE public.task_version_health_attestations",
		"task_version_health_attestations_revision_key",
		"task_version_health_attestations_immutable_guard",
		"CREATE FUNCTION public.task_version_health_attestation_guard()",
	} {
		require.Contains(t, string(migration), fragment)
	}
	for _, fragment := range []string{
		"-- name: ListTaskPoolVersionHealth :many",
		"LEFT JOIN LATERAL",
		"COALESCE(health.healthy, false)",
		"FROM task_public_exposures AS exposure",
		"-- name: CreateTaskVersionContentValidationAttestation :one",
		"-- name: RecordUnhealthyTaskVersionProbeAttestation :one",
		"-- name: RecordHealthyTaskVersionProbeAttestation :one",
	} {
		require.Contains(t, string(query), fragment)
	}
	require.NotContains(t, string(query), "sqlc.arg(healthy)")
	require.NotContains(t, string(taskSchema), "healthy:")
	require.NotContains(t, string(taskSchema), "mutation_locked:")
	require.NotContains(t, string(taskSchema), "publicly_exposed:")
}

func TestCurrentTaskPoolPublicationLocksBaseRelationBeforePoolRows(t *testing.T) {
	t.Parallel()

	query, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "queries", "task_content.sql"))
	require.NoError(t, err)

	require.Contains(t, string(query), "LIMIT 1\n    FOR KEY SHARE OF publication")
	require.Contains(t, string(query), "FOR KEY SHARE OF pool;")
	require.NotContains(t, string(query), "FOR KEY SHARE OF publication, pool;")
}

func TestTournamentContentSchemaKeepsPublishedOwners(t *testing.T) {
	t.Parallel()

	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "migrations", "000002_task_schema.sql"))
	require.NoError(t, err)
	tournamentMigration, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "migrations", "000003_tournament_schema.sql"))
	require.NoError(t, err)

	for _, fragment := range []string{
		"published tournament content cannot move to another configuration",
		"published tournament category memberships cannot move",
		"FOR NO KEY UPDATE OF task",
		"ORDER BY task.id",
		"tournament content cannot publish disabled task versions",
		"tournament content cannot publish unvalidated task versions",
	} {
		require.Contains(t, string(migration), fragment)
	}
	require.Contains(t, string(tournamentMigration), "tournament_content_configurations_tournament_id_fkey")
}

func TestAssignmentEdgeSourceLocksPublishedHealthyTaskVersion(t *testing.T) {
	t.Parallel()

	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "migrations", "000007_assignment_schema.sql"))
	require.NoError(t, err)
	query, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "queries", "assignment.sql"))
	require.NoError(t, err)

	for _, fragment := range []string{
		"CREATE FUNCTION public.assignment_edge_guard()",
		"JOIN tournament_content_configurations AS configuration",
		"JOIN task_pool_version_memberships AS membership",
		"membership.task_version = NEW.task_version",
		"FOR NO KEY UPDATE OF task",
		"normal pool is deliberately shared by Swiss, semifinal, and final",
		"COALESCE(health.healthy, false)",
		"assignment edges require a healthy version in the published tournament content pool",
	} {
		require.Contains(t, string(migration), fragment)
	}
	for _, fragment := range []string{
		"-- name: CreateAssignmentPlanEdge :one",
		"WITH locked_task_version AS",
		"membership.task_version = sqlc.arg(task_version)",
		"configuration.state = 'published'",
		"Assignment plans are pool-scoped",
		"normal pool is intentionally shared",
		"FOR NO KEY UPDATE OF task",
	} {
		require.Contains(t, string(query), fragment)
	}
	require.NotContains(t, string(migration), "task.current_version")
	require.NotContains(t, string(query), "task.current_version")
	require.NotContains(t, string(migration), "tournament_content_stage_defaults AS stage_default")
	require.NotContains(t, string(query), "tournament_content_stage_defaults AS stage_default")
}

func TestTaskDeletionSourceLocksThenChecksPublishedReferences(t *testing.T) {
	t.Parallel()

	query, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "queries", "tasks.sql"))
	require.NoError(t, err)

	for _, fragment := range []string{
		"-- name: LockTaskForContentMutation :one",
		"FOR UPDATE",
		"-- name: TaskReferencedByTournament :one",
		"configuration.state = 'published'",
	} {
		require.Contains(t, string(query), fragment)
	}
}

func TestTaskPostgresWritesValidationInsideTheHeadTransaction(t *testing.T) {
	t.Parallel()

	adapter, err := os.ReadFile("task_postgres.go")
	require.NoError(t, err)
	mapping, err := os.ReadFile("mapping.go")
	require.NoError(t, err)

	for _, fragment := range []string{
		"r.tx.Do(ctx, func(txCtx context.Context) error",
		"querier.CreateTaskVersionContentValidationAttestation",
		"TaskVersion: row.CurrentVersion",
		"querier.LockTaskForContentMutation",
		"querier.TaskReferencedByTournament",
		"return domain.ErrTaskInUse",
		"Kind:          string(in.Kind)",
		"Enabled:       in.Enabled",
	} {
		require.Contains(t, string(adapter), fragment)
	}
	for _, fragment := range []string{
		"Kind:           domain.TaskKind(kind)",
		"Enabled:        enabled",
		"CurrentVersion: int(currentVersion)",
	} {
		require.Contains(t, string(mapping), fragment)
	}
}

func TestTournamentPreflightReadsPublishedContentAuthority(t *testing.T) {
	t.Parallel()

	adapter, err := os.ReadFile("tournament_admin_roster_preflight.go")
	require.NoError(t, err)

	for _, fragment := range []string{
		"GetCurrentTournamentContentConfiguration",
		"ListTournamentContentCategoryPoolRevisions",
		"ListTournamentContentCategoryPoolMemberships",
		"ListTournamentContentStageDefaults",
		"ListTaskPoolVersionHealth",
		"domain.CreateContentConfiguration",
		"TaskHealth: loadedContent.taskHealth",
	} {
		require.Contains(t, string(adapter), fragment)
	}
	require.NotContains(t, string(adapter), "NormalPool: domain.TaskPoolRevision{Kind:")
}
