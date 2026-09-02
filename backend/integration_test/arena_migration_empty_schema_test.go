//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

const (
	arenaMigrationHeadVersion     int64 = 31
	arenaMigrationPreArenaVersion int64 = 6
)

func TestArenaMigrationEmptySchema(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	pool, database := createArenaMigrationIsolatedDatabase(t, ctx, "empty")

	var (
		publicNamespaceExists       bool
		publicNamespaceDependencies int
		publicRelations             int
		publicRoutines              int
		versionTable                sql.NullString
	)
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM pg_namespace
			WHERE nspname = 'public'
		)`).Scan(&publicNamespaceExists))
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM pg_depend AS dependency
		JOIN pg_namespace AS namespace
			ON dependency.refclassid = 'pg_namespace'::regclass
			AND dependency.refobjid = namespace.oid
			AND dependency.refobjsubid = 0
		WHERE namespace.nspname = 'public'`).Scan(&publicNamespaceDependencies))
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM pg_class AS relation
		JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
		WHERE namespace.nspname = 'public'`).Scan(&publicRelations))
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM pg_proc AS routine
		JOIN pg_namespace AS namespace ON namespace.oid = routine.pronamespace
		WHERE namespace.nspname = 'public'`).Scan(&publicRoutines))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT to_regclass('public.goose_db_version')::text`,
	).Scan(&versionTable))
	require.True(t, publicNamespaceExists, "template0 database must expose the public schema")
	require.Zero(t, publicNamespaceDependencies,
		"new database must not inherit namespace-owned objects from any catalog")
	require.Zero(t, publicRelations, "new database must not inherit public relations")
	require.Zero(t, publicRoutines, "new database must not inherit public routines")
	require.False(t, versionTable.Valid, "Goose metadata must not pre-exist")

	require.NoError(t, goose.UpContext(ctx, database, migrationsDirAbs()))

	var (
		appliedCount int
		firstVersion int64
		lastVersion  int64
	)
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COUNT(*), MIN(version_id), MAX(version_id)
		FROM goose_db_version
		WHERE is_applied AND version_id > 0`,
	).Scan(&appliedCount, &firstVersion, &lastVersion))
	require.Equal(t, int(arenaMigrationHeadVersion), appliedCount)
	require.EqualValues(t, 1, firstVersion)
	require.Equal(t, arenaMigrationHeadVersion, lastVersion)

	var versionsAreContinuous bool
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT NOT EXISTS (
			SELECT expected.version
			FROM generate_series(1, $1::bigint) AS expected(version)
			LEFT JOIN goose_db_version AS applied
				ON applied.version_id = expected.version AND applied.is_applied
			WHERE applied.version_id IS NULL
		)`, arenaMigrationHeadVersion).Scan(&versionsAreContinuous))
	require.True(t, versionsAreContinuous,
		"the canonical migration chain must apply every version through the head")

	requiredTables := []string{
		"arena_tournaments",
		"participant_reservations",
		"arena_submission_events",
		"arena_result_events",
		"arena_result_commits",
		"arena_audit_events",
		"arena_outbox_events",
		"arena_presence_states",
		"arena_pauses",
		"arena_reconnect_intervals",
		"arena_projection_revisions",
	}
	requiredConstraints := []string{
		"arena_tournaments_state_check",
		"participant_reservations_owner_shape_check",
		"arena_submission_events_identity_key",
		"arena_submission_events_idempotency_key_key",
		"arena_result_events_identity_key",
		"arena_result_events_idempotency_key_key",
		"arena_result_commits_idempotency_key_key",
		"arena_audit_events_identity_key",
		"arena_outbox_events_identity_key",
		"arena_outbox_events_idempotency_key_key",
		"arena_reconnect_intervals_segment_key",
		"arena_reconnect_intervals_lineage_check",
	}
	requiredIndexes := []string{
		"arena_tournaments_single_active_idx",
		"arena_outbox_events_unpublished_idx",
		"arena_reconnect_intervals_root_presence_epoch_key",
		"arena_reconnect_intervals_continued_from_key",
	}

	var presentTables int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM unnest($1::text[]) AS expected(name)
		WHERE to_regclass(format('public.%I', expected.name)) IS NOT NULL`,
		requiredTables,
	).Scan(&presentTables))
	require.Equal(t, len(requiredTables), presentTables,
		"authority, evidence, idempotency and recovery tables must all exist")

	var presentConstraints int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM unnest($1::text[]) AS expected(name)
		WHERE EXISTS (
			SELECT 1
			FROM pg_constraint AS constraint_definition
			JOIN pg_namespace AS namespace
				ON namespace.oid = constraint_definition.connamespace
			WHERE namespace.nspname = 'public'
				AND constraint_definition.conname = expected.name
		)`, requiredConstraints).Scan(&presentConstraints))
	require.Equal(t, len(requiredConstraints), presentConstraints,
		"authority, audit, result, idempotency and recovery constraints must all exist")

	var presentIndexes int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM unnest($1::text[]) AS expected(name)
		WHERE to_regclass(format('public.%I', expected.name)) IS NOT NULL`,
		requiredIndexes,
	).Scan(&presentIndexes))
	require.Equal(t, len(requiredIndexes), presentIndexes,
		"reviewed authority, outbox and recovery indexes must all exist")

	var headObjectsPresent bool
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT
			to_regclass('public.arena_golden_attempts') IS NOT NULL
			AND to_regclass('public.arena_projection_revisions') IS NOT NULL
			AND to_regclass('public.arena_reconnect_intervals_root_presence_epoch_key') IS NOT NULL
			AND to_regclass('public.arena_reconnect_intervals_continued_from_key') IS NOT NULL
			AND to_regprocedure('public.arena_normal_wave_reconnect_lock()') IS NOT NULL
			AND (
				SELECT COUNT(*) = 3
				FROM information_schema.columns
				WHERE table_schema = 'public'
					AND table_name = 'arena_reconnect_intervals'
					AND column_name = ANY(ARRAY[
						'continuation_number',
						'continued_from_id',
						'suspended_by_pause_id'
					])
			)`,
	).Scan(&headObjectsPresent))
	require.True(t, headObjectsPresent, "version 31 head objects must be complete")
}

func createArenaMigrationIsolatedDatabase(
	t testing.TB,
	ctx context.Context,
	label string,
) (*pgxpool.Pool, *sql.DB) {
	t.Helper()

	adminPool := sharedPool
	databaseName := uniq("arena_migration_" + label)
	identifier := pgx.Identifier{databaseName}.Sanitize()
	_, err := adminPool.Exec(ctx, "CREATE DATABASE "+identifier+" TEMPLATE template0")
	require.NoError(t, err)

	var (
		pool     *pgxpool.Pool
		database *sql.DB
	)
	t.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}
		if database != nil {
			_ = database.Close()
		}

		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, dropErr := adminPool.Exec(cleanupCtx, "DROP DATABASE "+identifier+" WITH (FORCE)")
		require.NoError(t, dropErr)
	})

	config := adminPool.Config().Copy()
	config.ConnConfig.Database = databaseName
	database = stdlib.OpenDB(*config.ConnConfig)
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	require.NoError(t, database.PingContext(ctx))

	pool, err = pgxpool.NewWithConfig(ctx, config)
	require.NoError(t, err)
	require.NoError(t, pool.Ping(ctx))
	return pool, database
}
