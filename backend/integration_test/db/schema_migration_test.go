//go:build integration

package db_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
	"github.com/TakuyaYagam1/task-per-minute/internal/bootstrap"
)

const schemaHeadVersion int64 = 25

func TestSchemaMigration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	migrationLock := testkit.MigrationLock{}
	migrationLock.Lock()
	defer migrationLock.Unlock()

	pool, database := testkit.CreateIsolatedDatabase(ctx, t, postgresPool, "empty")
	migrator := bootstrap.NewMigrator(testkit.MigrationDSN(t, pool, "public"), migrationsDirAbs())

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

	for version := int64(1); version <= schemaHeadVersion; version++ {
		require.NoError(t, goose.UpToContext(ctx, database, migrationsDirAbs(), version))
		requireMigrationVersion(ctx, t, pool, version)
		require.NoError(t, migrator.Status(ctx))
	}
	require.NoError(t, migrator.Up(ctx), "the current domain head must be accepted")
	requireProjectionEvidencePayloadStorage(ctx, t, pool)

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
	require.Equal(t, int(schemaHeadVersion), appliedCount)
	require.EqualValues(t, 1, firstVersion)
	require.Equal(t, schemaHeadVersion, lastVersion)

	var versionsAreContinuous bool
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT NOT EXISTS (
			SELECT expected.version
			FROM generate_series(1, $1::bigint) AS expected(version)
			LEFT JOIN goose_db_version AS applied
				ON applied.version_id = expected.version AND applied.is_applied
			WHERE applied.version_id IS NULL
		)`, schemaHeadVersion).Scan(&versionsAreContinuous))
	require.True(t, versionsAreContinuous,
		"the domain baseline must apply every version through the head")

	var (
		applicationTables    int
		applicationFunctions int
		triggers             int
		explicitIndexes      int
		foreignKeys          int
		legacyIdentifiers    int
		removedLegacy        int
	)
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM pg_class AS relation
		JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
		WHERE namespace.nspname = 'public'
			AND relation.relkind = 'r'
			AND relation.relname <> 'goose_db_version'`,
	).Scan(&applicationTables))
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM pg_proc AS routine
		JOIN pg_namespace AS namespace ON namespace.oid = routine.pronamespace
		WHERE namespace.nspname = 'public'
			AND NOT EXISTS (
				SELECT 1
				FROM pg_depend AS dependency
				WHERE dependency.classid = 'pg_proc'::regclass
					AND dependency.objid = routine.oid
					AND dependency.deptype = 'e'
			)`,
	).Scan(&applicationFunctions))
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM pg_trigger AS trigger_definition
		JOIN pg_class AS relation ON relation.oid = trigger_definition.tgrelid
		JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
		WHERE namespace.nspname = 'public'
			AND NOT trigger_definition.tgisinternal`,
	).Scan(&triggers))
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM pg_index AS index_definition
		JOIN pg_class AS index_relation ON index_relation.oid = index_definition.indexrelid
		JOIN pg_namespace AS namespace ON namespace.oid = index_relation.relnamespace
		WHERE namespace.nspname = 'public'
			AND NOT EXISTS (
				SELECT 1
				FROM pg_constraint AS constraint_definition
				WHERE constraint_definition.conindid = index_definition.indexrelid
					AND constraint_definition.contype IN ('p', 'u', 'x')
			)`,
	).Scan(&explicitIndexes))
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM pg_constraint AS constraint_definition
		JOIN pg_namespace AS namespace ON namespace.oid = constraint_definition.connamespace
		WHERE namespace.nspname = 'public'
			AND constraint_definition.contype = 'f'`,
	).Scan(&foreignKeys))
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT SUM(matches)
		FROM (
			SELECT COUNT(*) AS matches
			FROM pg_class AS relation
			JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
			WHERE namespace.nspname = 'public'
				AND relation.relname LIKE '%arena\_%' ESCAPE '\'
			UNION ALL
			SELECT COUNT(*)
			FROM pg_proc AS routine
			JOIN pg_namespace AS namespace ON namespace.oid = routine.pronamespace
			WHERE namespace.nspname = 'public'
				AND routine.proname LIKE '%arena\_%' ESCAPE '\'
			UNION ALL
			SELECT COUNT(*)
			FROM pg_constraint AS constraint_definition
			JOIN pg_namespace AS namespace
				ON namespace.oid = constraint_definition.connamespace
			WHERE namespace.nspname = 'public'
				AND constraint_definition.conname LIKE '%arena\_%' ESCAPE '\'
			UNION ALL
			SELECT COUNT(*)
			FROM pg_trigger AS trigger_definition
			JOIN pg_class AS relation ON relation.oid = trigger_definition.tgrelid
			JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
			WHERE namespace.nspname = 'public'
				AND NOT trigger_definition.tgisinternal
				AND trigger_definition.tgname LIKE '%arena\_%' ESCAPE '\'
			UNION ALL
			SELECT COUNT(*)
			FROM pg_attribute AS attribute
			JOIN pg_class AS relation ON relation.oid = attribute.attrelid
			JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
			WHERE namespace.nspname = 'public'
				AND attribute.attnum > 0
				AND NOT attribute.attisdropped
				AND attribute.attname LIKE '%arena\_%' ESCAPE '\'
		) AS legacy_names`,
	).Scan(&legacyIdentifiers))
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT
			(to_regclass('public.duels') IS NOT NULL)::INT
			+ (to_regclass('public.duel_player_tasks') IS NOT NULL)::INT
			+ (to_regclass('public.player_task_history') IS NOT NULL)::INT
			+ (EXISTS (
				SELECT 1
				FROM information_schema.columns
				WHERE table_schema = 'public'
					AND table_name = 'players'
					AND column_name = 'status'
			))::INT
			+ (EXISTS (
				SELECT 1
				FROM information_schema.columns
				WHERE table_schema = 'public'
					AND table_name = 'participant_reservations'
					AND column_name = 'casual_duel_id'
			))::INT
			+ (EXISTS (
				SELECT 1
				FROM information_schema.columns
				WHERE table_schema = 'public'
					AND table_name = 'participant_reservations'
					AND column_name IN ('owner_kind', 'owner_id')
			))::INT`,
	).Scan(&removedLegacy))
	require.Equal(t, 200, applicationTables)
	require.Equal(t, 200, applicationFunctions)
	require.Equal(t, 295, triggers)
	require.Equal(t, 102, explicitIndexes)
	require.Equal(t, 631, foreignKeys)
	require.Zero(t, legacyIdentifiers,
		"domain baseline must not expose legacy-prefixed schema identifiers")
	require.Zero(t, removedLegacy,
		"domain baseline must not retain duel, history or player-status persistence")

	requiredTables := []string{
		"players",
		"tasks",
		"task_public_exposures",
		"admin_player_audit_events",
		"tournaments",
		"participant_reservations",
		"submission_events",
		"result_events",
		"result_commits",
		"audit_events",
		"outbox_events",
		"outbox_wave_control_sources",
		"presence_states",
		"pauses",
		"reconnect_intervals",
		"projection_revisions",
		"swiss_draft_delivery_history_heads",
	}
	requiredConstraints := []string{
		"tournaments_state_check",
		"submission_events_identity_key",
		"submission_events_idempotency_key_key",
		"result_events_identity_key",
		"result_events_idempotency_key_key",
		"result_commits_idempotency_key_key",
		"audit_events_identity_key",
		"outbox_events_target_identity_key",
		"outbox_events_idempotency_key_key",
		"outbox_wave_control_sources_command_key",
		"outbox_wave_control_sources_target_event_fk",
		"reconnect_intervals_segment_key",
		"reconnect_intervals_lineage_check",
	}
	requiredIndexes := []string{
		"tournaments_single_active_idx",
		"outbox_events_claim_idx",
		"outbox_wave_control_sources_scope_idx",
		"reconnect_intervals_root_presence_epoch_key",
		"reconnect_intervals_continued_from_key",
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

	var baselineObjectsPresent bool
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT
			to_regclass('public.golden_attempts') IS NOT NULL
			AND to_regclass('public.projection_revisions') IS NOT NULL
			AND to_regclass('public.reconnect_intervals_root_presence_epoch_key') IS NOT NULL
			AND to_regclass('public.reconnect_intervals_continued_from_key') IS NOT NULL
			AND to_regprocedure('public.normal_wave_reconnect_lock()') IS NOT NULL
			AND (
				SELECT COUNT(*) = 3
				FROM information_schema.columns
				WHERE table_schema = 'public'
					AND table_name = 'reconnect_intervals'
					AND column_name = ANY(ARRAY[
						'continuation_number',
						'continued_from_id',
						'suspended_by_pause_id'
					])
			)`,
	).Scan(&baselineObjectsPresent))
	require.True(t, baselineObjectsPresent, "domain baseline objects must be complete")

	for expectedVersion := schemaHeadVersion - 1; expectedVersion >= 0; expectedVersion-- {
		require.NoError(t, migrator.Down(ctx))
		requireMigrationVersion(ctx, t, pool, expectedVersion)
	}

	var applicationObjectsRemain bool
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT
			to_regclass('public.players') IS NOT NULL
			OR to_regclass('public.tournaments') IS NOT NULL
			OR to_regclass('public.projection_revisions') IS NOT NULL
			OR to_regprocedure('public.append_only_guard()') IS NOT NULL`,
	).Scan(&applicationObjectsRemain))
	require.False(t, applicationObjectsRemain,
		"down to version zero must remove application schema objects")
	requireProjectionEvidencePayloadColumnsAbsent(ctx, t, pool)

	require.NoError(t, migrator.Up(ctx))
	requireMigrationVersion(ctx, t, pool, schemaHeadVersion)

	var reappliedObjectsPresent bool
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT
			to_regclass('public.players') IS NOT NULL
			AND to_regclass('public.tournaments') IS NOT NULL
			AND to_regclass('public.projection_revisions') IS NOT NULL
			AND to_regprocedure('public.append_only_guard()') IS NOT NULL`,
	).Scan(&reappliedObjectsPresent))
	require.True(t, reappliedObjectsPresent,
		"domain baseline must reapply after a complete down migration")
}

func requireProjectionEvidencePayloadStorage(
	ctx context.Context,
	tb testing.TB,
	pool *pgxpool.Pool,
) {
	tb.Helper()

	tables := []string{
		"projection_artifacts",
		"result_projection_nodes",
		"correction_projection_decisions",
	}
	var (
		payloadColumns int
		jsonColumns    int
	)
	require.NoError(tb, pool.QueryRow(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE data_type = 'json')
		FROM information_schema.columns
		WHERE table_schema = 'public'
			AND table_name = ANY($1::text[])
			AND column_name = 'payload'`, tables).Scan(&payloadColumns, &jsonColumns))
	require.Equal(tb, len(tables), payloadColumns)
	require.Equal(tb, len(tables), jsonColumns,
		"byte-digested projection evidence must use serialization-preserving json storage")

	tx, err := pool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	const exactPayload = `{ "z": 1.0, "a": [1e0, {"nested": true}] }`
	for index, table := range tables {
		tempTable := pgx.Identifier{fmt.Sprintf("projection_payload_roundtrip_%d", index)}.Sanitize()
		sourceTable := pgx.Identifier{"public", table}.Sanitize()
		_, err = tx.Exec(ctx, "CREATE TEMP TABLE "+tempTable+
			" ON COMMIT DROP AS SELECT payload FROM "+sourceTable+" WITH NO DATA")
		require.NoError(tb, err)

		var storedPayload string
		err = tx.QueryRow(ctx, "INSERT INTO "+tempTable+
			" (payload) VALUES ($1::json) RETURNING payload::text", exactPayload).Scan(&storedPayload)
		require.NoError(tb, err)
		require.Zero(tb, strings.Compare(exactPayload, storedPayload),
			table+" must round-trip whitespace, key order, and numeric lexemes")
	}

	_, err = tx.Exec(ctx, `
		CREATE TEMP TABLE projection_artifact_guard_probe (
			payload json NOT NULL
		) ON COMMIT DROP`)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO projection_artifact_guard_probe (payload)
		VALUES ($1::json)`, exactPayload)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, `
		CREATE TRIGGER projection_artifact_guard_probe
		BEFORE UPDATE ON projection_artifact_guard_probe
		FOR EACH ROW EXECUTE FUNCTION public.projection_artifact_guard()`)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, "SAVEPOINT projection_artifact_guard_probe_update")
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, `
		UPDATE projection_artifact_guard_probe
		SET payload = '{"a":[1.0,{"nested":true}],"z":1e0}'::json`)
	require.ErrorContains(tb, err, "projection artifacts are immutable evidence")
	_, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT projection_artifact_guard_probe_update")
	require.NoError(tb, err)

	var guardedPayload string
	require.NoError(tb, tx.QueryRow(ctx, `
		SELECT payload::text
		FROM projection_artifact_guard_probe`,
	).Scan(&guardedPayload))
	require.Zero(tb, strings.Compare(exactPayload, guardedPayload),
		"the artifact guard must reject a textually different payload")
	require.NoError(tb, tx.Rollback(ctx))
}

func requireProjectionEvidencePayloadColumnsAbsent(
	ctx context.Context,
	tb testing.TB,
	pool *pgxpool.Pool,
) {
	tb.Helper()

	var payloadColumns int
	require.NoError(tb, pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_schema = 'public'
			AND table_name = ANY($1::text[])
			AND column_name = 'payload'`, []string{
		"projection_artifacts",
		"result_projection_nodes",
		"correction_projection_decisions",
	}).Scan(&payloadColumns))
	require.Zero(tb, payloadColumns,
		"full Down must remove every serialization-preserving projection evidence column")
}

func TestMigrationAtomicRollback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	migrationLock := testkit.MigrationLock{}
	migrationLock.Lock()
	defer migrationLock.Unlock()

	pool, database := testkit.CreateIsolatedDatabase(ctx, t, postgresPool, "rollback")
	require.NoError(t, goose.UpToContext(ctx, database, migrationsDirAbs(), 6))
	requireMigrationVersion(ctx, t, pool, 6)

	_, err := pool.Exec(ctx, `
		CREATE TABLE task_snapshots (
			marker TEXT NOT NULL
		)`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO task_snapshots (marker)
		VALUES ('preexisting')`)
	require.NoError(t, err)

	err = goose.UpToContext(ctx, database, migrationsDirAbs(), 7)
	require.Error(t, err, "conflicting object must reject the assignment migration")

	var marker string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT marker
		FROM task_snapshots`,
	).Scan(&marker))
	require.Equal(t, "preexisting", marker,
		"the object that caused the conflict must survive migration rollback")

	var earlierObjectsPresent bool
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT
			to_regclass('public.players') IS NOT NULL
			AND to_regclass('public.tournaments') IS NOT NULL
			AND to_regclass('public.category_revisions') IS NOT NULL`,
	).Scan(&earlierObjectsPresent))
	require.True(t, earlierObjectsPresent,
		"successfully applied domain migrations must survive a later rollback")

	var failedMigrationObjectsPresent bool
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT
			to_regprocedure('public.assignment_branch_guard()') IS NOT NULL
			OR to_regclass('public.assignment_plans') IS NOT NULL
			OR to_regclass('public.assignments') IS NOT NULL`,
	).Scan(&failedMigrationObjectsPresent))
	require.False(t, failedMigrationObjectsPresent,
		"objects created before the conflict in version seven must roll back atomically")

	var (
		appliedCount        int
		lastAppliedVersion  int64
		versionSevenApplied bool
		laterObjectsPresent bool
	)
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COUNT(*), MAX(version_id)
		FROM goose_db_version
		WHERE is_applied AND version_id > 0`,
	).Scan(&appliedCount, &lastAppliedVersion))
	require.Equal(t, 6, appliedCount)
	require.EqualValues(t, 6, lastAppliedVersion)
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM goose_db_version
			WHERE version_id = 7 AND is_applied
		)`).Scan(&versionSevenApplied))
	require.False(t, versionSevenApplied,
		"failed assignment migration must not record version seven as applied")
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT
			to_regclass('public.result_events') IS NOT NULL
			OR to_regclass('public.projection_revisions') IS NOT NULL`,
	).Scan(&laterObjectsPresent))
	require.False(t, laterObjectsPresent,
		"migrations after the failed version must not run")
}

func TestMigrationRejectsForeignLineage(t *testing.T) {
	migrationLock := testkit.MigrationLock{}
	migrationLock.Lock()
	defer migrationLock.Unlock()

	for _, version := range []int64{1, 11, 31} {
		for _, searchPath := range []string{"public", "shadow,public"} {
			t.Run(fmt.Sprintf("version_%d/%s", version, searchPath), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				pool, _ := testkit.CreateIsolatedDatabase(ctx, t, postgresPool, "foreign")
				_, err := pool.Exec(ctx, `
					CREATE TABLE public.goose_db_version (
						id integer GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
						version_id bigint NOT NULL,
						is_applied boolean NOT NULL,
						tstamp timestamp NOT NULL DEFAULT now()
					);
					CREATE TABLE public.migration_sentinel (id integer PRIMARY KEY, value text NOT NULL);
					INSERT INTO public.migration_sentinel VALUES (1, 'keep');
					CREATE SCHEMA shadow;
					CREATE TABLE shadow.goose_db_version (version_id bigint);
					INSERT INTO shadow.goose_db_version VALUES (0);
				`)
				require.NoError(t, err)
				if version == schemaHeadVersion {
					_, err = pool.Exec(ctx, `
						INSERT INTO public.goose_db_version (version_id, is_applied)
						SELECT version, true FROM generate_series(0, $1::bigint) AS version`, version)
				} else {
					_, err = pool.Exec(ctx, `
						INSERT INTO public.goose_db_version (version_id, is_applied) VALUES (0, true), ($1, true)`, version)
				}
				require.NoError(t, err)

				migrator := bootstrap.NewMigrator(testkit.MigrationDSN(t, pool, searchPath), migrationsDirAbs())
				before := foreignMigrationSnapshot(ctx, t, pool)
				for _, operation := range []struct {
					name string
					run  func(context.Context) error
				}{{"status", migrator.Status}, {"up", migrator.Up}, {"down", migrator.Down}} {
					require.ErrorIs(t, operation.run(ctx), bootstrap.ErrIncompatibleMigrationLineage, operation.name)
					require.Equal(t, before, foreignMigrationSnapshot(ctx, t, pool), operation.name+" must not change foreign data or history")
				}
				var domainTableExists bool
				require.NoError(t, pool.QueryRow(ctx, `SELECT to_regclass('public.players') IS NOT NULL`).Scan(&domainTableExists))
				require.False(t, domainTableExists)
			})
		}
	}
}

func TestMigrationStatusWithoutMetadata(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	migrationLock := testkit.MigrationLock{}
	migrationLock.Lock()
	defer migrationLock.Unlock()

	pool, _ := testkit.CreateIsolatedDatabase(ctx, t, postgresPool, "status")
	_, err := pool.Exec(ctx, `
		CREATE SCHEMA shadow;
		CREATE TABLE shadow.goose_db_version (version_id bigint);
		INSERT INTO shadow.goose_db_version VALUES (31);
	`)
	require.NoError(t, err)
	migrator := bootstrap.NewMigrator(testkit.MigrationDSN(t, pool, "shadow,public"), migrationsDirAbs())
	var before int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM pg_class WHERE relnamespace = 'public'::regnamespace`).Scan(&before))
	require.NoError(t, migrator.Status(ctx))
	var after int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM pg_class WHERE relnamespace = 'public'::regnamespace`).Scan(&after))
	require.Equal(t, before, after)
	var metadataExists bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT to_regclass('public.goose_db_version') IS NOT NULL`).Scan(&metadataExists))
	require.False(t, metadataExists, "status must not create metadata")
	var shadowVersion int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT version_id FROM shadow.goose_db_version`).Scan(&shadowVersion))
	require.EqualValues(t, 31, shadowVersion)
}

func TestMigrationUsesPublicMetadata(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	migrationLock := testkit.MigrationLock{}
	migrationLock.Lock()
	defer migrationLock.Unlock()

	pool, _ := testkit.CreateIsolatedDatabase(ctx, t, postgresPool, "search_path")
	_, err := pool.Exec(ctx, `
		CREATE SCHEMA shadow;
		CREATE TABLE shadow.goose_db_version (version_id bigint);
		INSERT INTO shadow.goose_db_version VALUES (31);
	`)
	require.NoError(t, err)
	migrator := bootstrap.NewMigrator(testkit.MigrationDSN(t, pool, "shadow,public"), migrationsDirAbs())
	require.NoError(t, migrator.Up(ctx))
	requireMigrationVersion(ctx, t, pool, schemaHeadVersion)
	require.NoError(t, migrator.Status(ctx))
	var shadowVersion int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT version_id FROM shadow.goose_db_version`).Scan(&shadowVersion))
	require.EqualValues(t, 31, shadowVersion)
}

func TestMigrationChecksLineageAfterAcquiringLock(t *testing.T) {
	migrationLock := testkit.MigrationLock{}
	migrationLock.Lock()
	defer migrationLock.Unlock()

	for _, direction := range []string{"up", "down"} {
		t.Run(direction, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			pool, database := testkit.CreateIsolatedDatabase(ctx, t, postgresPool, "lock")
			conn, err := database.Conn(ctx)
			require.NoError(t, err)
			defer conn.Close()
			const lockID int64 = 0x74706d5f6d696772
			_, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, lockID)
			require.NoError(t, err)
			defer func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cleanupCancel()
				_, _ = conn.ExecContext(cleanupCtx, `SELECT pg_advisory_unlock($1)`, lockID)
			}()

			migrator := bootstrap.NewMigrator(testkit.MigrationDSN(t, pool, "public"), migrationsDirAbs())
			operation := migrator.Up
			if direction == "down" {
				operation = migrator.Down
			}
			result := make(chan error, 1)
			go func() { result <- operation(ctx) }()
			require.Eventually(t, func() bool {
				var waiting bool
				err := pool.QueryRow(ctx, `
					SELECT EXISTS (
						SELECT 1 FROM pg_stat_activity
						WHERE datname = current_database() AND wait_event = 'advisory'
							AND query LIKE '%pg_advisory_lock%'
					)`).Scan(&waiting)
				return err == nil && waiting
			}, 5*time.Second, 10*time.Millisecond)
			var metadataExists bool
			require.NoError(t, pool.QueryRow(ctx, `SELECT to_regclass('public.goose_db_version') IS NOT NULL`).Scan(&metadataExists))
			require.False(t, metadataExists, "waiting for the lock must not create metadata")

			_, err = pool.Exec(ctx, `
				CREATE TABLE public.goose_db_version (
					id integer PRIMARY KEY, version_id bigint NOT NULL, is_applied boolean NOT NULL,
					tstamp timestamp NOT NULL DEFAULT now()
				);
				INSERT INTO public.goose_db_version (id, version_id, is_applied) VALUES (1, 0, true), (2, 1, true);
			`)
			require.NoError(t, err)
			_, err = conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, lockID)
			require.NoError(t, err)
			select {
			case err := <-result:
				require.ErrorIs(t, err, bootstrap.ErrIncompatibleMigrationLineage)
			case <-ctx.Done():
				t.Fatal("migrator did not finish after the lock was released")
			}
			var count int
			require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM public.goose_db_version`).Scan(&count))
			require.Equal(t, 2, count)
			var reacquired bool
			require.NoError(t, conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, lockID).Scan(&reacquired))
			require.True(t, reacquired, "rejected history must release the lock")
		})
	}
}

func foreignMigrationSnapshot(ctx context.Context, tb testing.TB, pool *pgxpool.Pool) string {
	tb.Helper()
	var snapshot string
	require.NoError(tb, pool.QueryRow(ctx, `
		SELECT jsonb_build_object(
			'history', (SELECT jsonb_agg(to_jsonb(history) ORDER BY id) FROM public.goose_db_version AS history),
			'sentinel', (SELECT jsonb_agg(to_jsonb(sentinel) ORDER BY id) FROM public.migration_sentinel AS sentinel),
			'shadow', (SELECT jsonb_agg(version_id) FROM shadow.goose_db_version),
			'marker', obj_description('public.goose_db_version'::regclass, 'pg_class'),
			'relations', (SELECT jsonb_agg(relname ORDER BY relname) FROM pg_class WHERE relnamespace = 'public'::regnamespace)
		)::text`).Scan(&snapshot))
	return snapshot
}
