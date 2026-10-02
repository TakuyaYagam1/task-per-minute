//go:build integration

package integration_test

import (
	"strings"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
	"github.com/TakuyaYagam1/task-per-minute/internal/bootstrap"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

func TestNormalCandidateCountMigration(t *testing.T) {
	parallelDatabaseMigrationMu.Lock()
	defer parallelDatabaseMigrationMu.Unlock()
	ctx := t.Context()
	pool, database := testkit.CreateIsolatedDatabase(ctx, t, sharedPool, "normal_candidates")
	dir := bootstrap.ResolveMigrationsDir("db/migrations")
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpToContext(ctx, database, dir, 40))
	constraint := func() string {
		var definition string
		require.NoError(t, pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint
			WHERE conrelid = 'public.exact_normal_assignment_sources'::regclass
			AND conname = 'exact_normal_assignment_sources_check'`).Scan(&definition))
		return definition
	}
	before := constraint()
	require.Contains(t, before, "jsonb_array_length(candidates) >= 3")
	require.NoError(t, goose.UpToContext(ctx, database, dir, 41))
	require.Equal(t, strings.Replace(before, "jsonb_array_length(candidates) >= 3", "jsonb_array_length(candidates) >= 1", 1), constraint(), "all other evidence guards must remain unchanged")
	require.NoError(t, goose.DownContext(ctx, database, dir))
	require.Equal(t, before, constraint())
	require.NoError(t, goose.UpToContext(ctx, database, dir, 41))
	// Exercise the actual migrated CHECK in isolation from assignment-plan
	// authority triggers. No live tournament evidence is edited for this test.
	_, err := pool.Exec(ctx, `CREATE TABLE migration_candidate_fixture
		(LIKE public.exact_normal_assignment_sources INCLUDING CONSTRAINTS)`)
	require.NoError(t, err)
	insert := `INSERT INTO migration_candidate_fixture (
		plan_id, tournament_id, roster_id, series_id, slot_id, category_lock_id,
		category, series_revision, pool_revision_id, pool_revision,
		history_revision_id, history_revision, roster_revision, artifact_revision_id,
		artifact_revision, category_revision_id, category_revision, pool,
		participant_ids, participant_reservations, history, candidates,
		graph_digest, artifact_digest, proof_hash, created_at)
		SELECT id, id, id, id, id, id, 'crypto', 1, id, 1, id, 1, 1, id, 1, id, 1,
		'{}'::jsonb, '[{},{}]'::jsonb, '[{},{}]'::jsonb, '[]'::jsonb, $1::jsonb,
		decode(repeat('01', 32), 'hex'), decode(repeat('02', 32), 'hex'),
		repeat('a', 64), now() FROM (SELECT gen_random_uuid() AS id) AS fixture`
	for _, candidates := range []string{`[{}]`, `[{},{}]`, `[{},{},{}]`} {
		_, err = pool.Exec(ctx, insert, candidates)
		require.NoError(t, err)
	}
	_, err = pool.Exec(ctx, insert, `[]`)
	require.ErrorContains(t, err, "exact_normal_assignment_sources_check")
	// The old minimum cannot be restored over rows containing one or two
	// candidates. ALTER validation fails without changing those rows.
	_, err = pool.Exec(ctx, `ALTER TABLE migration_candidate_fixture ADD CONSTRAINT
		previous_candidate_minimum CHECK (jsonb_array_length(candidates) >= 3)`)
	require.ErrorContains(t, err, "previous_candidate_minimum")
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM migration_candidate_fixture`).Scan(&count))
	require.Equal(t, 3, count)
}
