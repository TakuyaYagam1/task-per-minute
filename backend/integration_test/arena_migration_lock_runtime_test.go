//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

type arenaMigrationHeapRow struct {
	id   uuid.UUID
	ctid string
	xmin string
}

type arenaMigrationHeapSnapshot struct {
	relationFileNode int64
	rowCount         int
	rows             []arenaMigrationHeapRow
}

const (
	arenaMigrationMaxStepDuration       = 15 * time.Second
	arenaMigrationMaxLockObservation    = 5 * time.Second
	arenaMigrationMaxLegacyOperation    = 2 * time.Second
	arenaMigrationMaxCompletionOnUnlock = 15 * time.Second
)

func TestArenaMigrationLockRuntime(t *testing.T) {
	t.Run("records each forward migration duration", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		pool, database := createArenaMigrationIsolatedDatabase(t, ctx, "duration")
		durations := make([]time.Duration, 0, arenaMigrationHeadVersion)
		for version := int64(1); version <= arenaMigrationHeadVersion; version++ {
			startedAt := time.Now()
			require.NoError(t, goose.UpToContext(ctx, database, migrationsDirAbs(), version))
			duration := time.Since(startedAt)
			durations = append(durations, duration)
			t.Logf("migration version=%06d duration=%s", version, duration)
			require.LessOrEqual(t, duration, arenaMigrationMaxStepDuration,
				"migration version %06d exceeded the reviewed step threshold", version)
			requireArenaMigrationVersion(t, ctx, pool, version)
		}
		require.Len(t, durations, int(arenaMigrationHeadVersion))
	})

	t.Run("version 31 exposes bounded lock wait without heap rewrite", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		pool, database := createArenaMigrationIsolatedDatabase(t, ctx, "lock")
		require.NoError(t, goose.UpToContext(ctx, database, migrationsDirAbs(), 30))
		requireArenaMigrationVersion(t, ctx, pool, 30)

		seedArenaMigrationReconnectRow(t, ctx, pool)
		before := loadArenaMigrationHeapSnapshot(t, ctx, pool)
		require.Positive(t, before.rowCount)
		legacyPlayerID := uuid.New()
		legacyUsername := uniq("arena_migration_legacy_probe")
		_, err := pool.Exec(ctx, `
			INSERT INTO players (id, username, status, created_at)
			VALUES ($1, $2, 'idle', $3)`,
			legacyPlayerID,
			legacyUsername,
			time.Now().UTC().Truncate(time.Microsecond),
		)
		require.NoError(t, err)

		var applicationName string
		require.NoError(t, database.QueryRowContext(ctx, `
			SELECT set_config('application_name', $1, false)`,
			uniq("arena_migration_up"),
		).Scan(&applicationName))
		require.NotEmpty(t, applicationName)

		var migrationPID int
		require.NoError(t, database.QueryRowContext(ctx,
			`SELECT pg_backend_pid()`,
		).Scan(&migrationPID))

		blockerTx, err := pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = blockerTx.Rollback(context.Background()) }()

		var blockerPID int
		require.NoError(t, blockerTx.QueryRow(ctx,
			`SELECT pg_backend_pid()`,
		).Scan(&blockerPID))
		_, err = blockerTx.Exec(ctx,
			`LOCK TABLE arena_reconnect_intervals IN ROW EXCLUSIVE MODE`,
		)
		require.NoError(t, err)

		migrationResult := make(chan error, 1)
		migrationStartedAt := time.Now()
		go func() {
			migrationResult <- goose.UpToContext(
				ctx,
				database,
				migrationsDirAbs(),
				arenaMigrationHeadVersion,
			)
		}()

		observedAfter, observeErr := observeArenaMigrationLockWait(
			ctx,
			pool,
			migrationPID,
			blockerPID,
			arenaMigrationMaxLockObservation,
		)
		legacyOperationStartedAt := time.Now()
		var observedLegacyUsername string
		legacyReadErr := pool.QueryRow(ctx, `
			SELECT username
			FROM players
			WHERE id = $1`, legacyPlayerID).Scan(&observedLegacyUsername)
		legacyWriteTag, legacyWriteErr := pool.Exec(ctx, `
			UPDATE players
			SET username = username
			WHERE id = $1`, legacyPlayerID)
		legacyOperationDuration := time.Since(legacyOperationStartedAt)
		releaseStartedAt := time.Now()
		releaseErr := blockerTx.Commit(ctx)

		var migrationErr error
		select {
		case migrationErr = <-migrationResult:
		case <-time.After(30 * time.Second):
			cancel()
			select {
			case migrationErr = <-migrationResult:
			case <-time.After(5 * time.Second):
				t.Fatal("version 31 migration did not stop after timeout cancellation")
			}
		}

		require.NoError(t, observeErr)
		require.LessOrEqual(t, observedAfter, arenaMigrationMaxLockObservation)
		require.NoError(t, legacyReadErr)
		require.Equal(t, legacyUsername, observedLegacyUsername)
		require.NoError(t, legacyWriteErr)
		require.EqualValues(t, 1, legacyWriteTag.RowsAffected())
		require.LessOrEqual(t, legacyOperationDuration, arenaMigrationMaxLegacyOperation,
			"unrelated legacy reads and writes must remain available during the Arena lock wait")
		require.NoError(t, releaseErr)
		require.NoError(t, migrationErr)
		require.LessOrEqual(t, time.Since(releaseStartedAt), arenaMigrationMaxCompletionOnUnlock,
			"migration must complete within the reviewed threshold after blocker release")
		requireArenaMigrationVersion(t, ctx, pool, arenaMigrationHeadVersion)
		t.Logf(
			"migration version=000031 lock_observed_after=%s blocker_release=%s total_duration=%s",
			observedAfter,
			time.Since(releaseStartedAt),
			time.Since(migrationStartedAt),
		)

		after := loadArenaMigrationHeapSnapshot(t, ctx, pool)
		require.Equal(t, before.relationFileNode, after.relationFileNode,
			"version 31 must not rewrite the reconnect heap")
		require.Equal(t, before.rowCount, after.rowCount,
			"version 31 must preserve every reconnect interval")
		require.Equal(t, before.rows, after.rows,
			"unchanged ctid/xmin proves that version 31 did not row-update existing intervals")

		var (
			hasMissing   bool
			missingValue string
			visibleRoots int
		)
		require.NoError(t, pool.QueryRow(ctx, `
			SELECT atthasmissing, COALESCE(attmissingval::text, '')
			FROM pg_attribute
			WHERE attrelid = 'public.arena_reconnect_intervals'::regclass
				AND attname = 'continuation_number'
				AND NOT attisdropped`,
		).Scan(&hasMissing, &missingValue))
		require.True(t, hasMissing,
			"constant default must be represented as PostgreSQL missing-value metadata")
		require.Equal(t, "{0}", missingValue)

		require.NoError(t, pool.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM arena_reconnect_intervals
			WHERE continuation_number = 0
				AND continued_from_id IS NULL
				AND suspended_by_pause_id IS NULL`,
		).Scan(&visibleRoots))
		require.Equal(t, before.rowCount, visibleRoots,
			"retained version 30 intervals must expose the version 31 root defaults")
	})
}

func seedArenaMigrationReconnectRow(
	t testing.TB,
	ctx context.Context,
	pool *pgxpool.Pool,
) {
	t.Helper()

	// Existing reconnect fixture builders use the package pool. This test is
	// intentionally sequential, and the swap is restored before migration work.
	originalPool := sharedPool
	sharedPool = pool
	defer func() { sharedPool = originalPool }()

	fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
	disconnectArenaParticipant(
		t,
		ctx,
		fixture,
		fixture.draft.participantIDs[0],
		fixture.pausedAt.Add(time.Second),
		2*time.Minute,
	)
}

func loadArenaMigrationHeapSnapshot(
	t testing.TB,
	ctx context.Context,
	pool *pgxpool.Pool,
) arenaMigrationHeapSnapshot {
	t.Helper()

	var snapshot arenaMigrationHeapSnapshot
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT pg_relation_filenode('public.arena_reconnect_intervals'::regclass)::bigint,
			COUNT(*)
		FROM arena_reconnect_intervals`,
	).Scan(&snapshot.relationFileNode, &snapshot.rowCount))

	rows, err := pool.Query(ctx, `
		SELECT id, ctid::text, xmin::text
		FROM arena_reconnect_intervals
		ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()

	snapshot.rows = make([]arenaMigrationHeapRow, 0, snapshot.rowCount)
	for rows.Next() {
		var row arenaMigrationHeapRow
		require.NoError(t, rows.Scan(&row.id, &row.ctid, &row.xmin))
		snapshot.rows = append(snapshot.rows, row)
	}
	require.NoError(t, rows.Err())
	return snapshot
}

func observeArenaMigrationLockWait(
	ctx context.Context,
	pool *pgxpool.Pool,
	migrationPID int,
	blockerPID int,
	timeout time.Duration,
) (time.Duration, error) {
	startedAt := time.Now()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	var lastErr error
	for {
		var (
			waitEventType  string
			blockedByProbe bool
		)
		err := pool.QueryRow(ctx, `
			SELECT
				COALESCE(wait_event_type, ''),
				$2::integer = ANY(pg_blocking_pids($1))
			FROM pg_stat_activity
			WHERE pid = $1`, migrationPID, blockerPID,
		).Scan(&waitEventType, &blockedByProbe)
		if err == nil && waitEventType == "Lock" && blockedByProbe {
			return time.Since(startedAt), nil
		}
		if err != nil {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-deadline.C:
			if lastErr != nil {
				return 0, fmt.Errorf(
					"migration pid %d did not expose lock wait on blocker pid %d: %w",
					migrationPID,
					blockerPID,
					lastErr,
				)
			}
			return 0, fmt.Errorf(
				"migration pid %d did not expose lock wait on blocker pid %d",
				migrationPID,
				blockerPID,
			)
		case <-ticker.C:
		}
	}
}
