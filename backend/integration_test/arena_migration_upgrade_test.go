//go:build integration

package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

type arenaLegacySnapshot struct {
	players              []byte
	tasks                []byte
	duels                []byte
	duelPlayerTasks      []byte
	playerTaskHistory    []byte
	leaderboardOverrides []byte
}

type arenaDurableResultSnapshot struct {
	submissions []byte
	results     []byte
	commits     []byte
	audit       []byte
	outbox      []byte
}

type arenaLegacyDuelState struct {
	id         uuid.UUID
	status     string
	winnerID   pgtype.UUID
	deadline   time.Time
	startedAt  time.Time
	finishedAt pgtype.Timestamptz
}

func TestArenaMigrationUpgrade(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	pool, database := createArenaMigrationIsolatedDatabase(t, ctx, "legacy")
	require.NoError(t, goose.UpToContext(
		ctx,
		database,
		migrationsDirAbs(),
		arenaMigrationPreArenaVersion,
	))
	requireArenaMigrationVersion(t, ctx, pool, arenaMigrationPreArenaVersion)

	fixtureSQL, err := os.ReadFile(arenaLegacyFixturePath())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(fixtureSQL))
	require.NoError(t, err)

	beforeSnapshot := loadArenaLegacySnapshot(t, ctx, pool)
	beforeDuels := loadArenaLegacyDuelStates(t, ctx, pool)
	require.Len(t, beforeDuels, 2)
	require.Equal(t, "active", beforeDuels[0].status)
	require.False(t, beforeDuels[0].winnerID.Valid)
	require.False(t, beforeDuels[0].finishedAt.Valid)
	require.Equal(t, "finished", beforeDuels[1].status)
	require.True(t, beforeDuels[1].winnerID.Valid)
	require.True(t, beforeDuels[1].finishedAt.Valid)

	require.NoError(t, goose.UpToContext(
		ctx,
		database,
		migrationsDirAbs(),
		16,
	))
	requireArenaMigrationVersion(t, ctx, pool, 16)

	var (
		submissionID uuid.UUID
		resultCommit arenaResultAuditCommit
	)
	func() {
		originalPool := sharedPool
		sharedPool = pool
		defer func() { sharedPool = originalPool }()

		resultFixture := createArenaResultAuditMigrationFixture(t, ctx)
		submissionID, _ = createAcceptedArenaSubmission(t, ctx, resultFixture)
		resultCommit = createAtomicArenaResultCommit(t, ctx, resultFixture, submissionID)
	}()

	beforeDurableResults := loadArenaDurableResultSnapshot(
		t,
		ctx,
		pool,
		submissionID,
		resultCommit,
	)
	require.NoError(t, goose.UpToContext(
		ctx,
		database,
		migrationsDirAbs(),
		17,
	))
	requireArenaMigrationVersion(t, ctx, pool, 17)
	require.Equal(t, beforeSnapshot, loadArenaLegacySnapshot(t, ctx, pool),
		"schema 000017 must preserve active and finished casual rows")
	require.Equal(t, beforeDuels, loadArenaLegacyDuelStates(t, ctx, pool),
		"schema 000017 must preserve casual lifecycle state")
	require.Equal(t, beforeDurableResults, loadArenaDurableResultSnapshot(
		t,
		ctx,
		pool,
		submissionID,
		resultCommit,
	), "schema 000017 must preserve append-only Arena submissions and results")

	require.NoError(t, goose.UpToContext(
		ctx,
		database,
		migrationsDirAbs(),
		arenaMigrationHeadVersion,
	))
	requireArenaMigrationVersion(t, ctx, pool, arenaMigrationHeadVersion)

	poolConfig := pool.Config().Copy()
	pool.Close()
	restartedPool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	require.NoError(t, err)
	t.Cleanup(restartedPool.Close)
	require.NoError(t, restartedPool.Ping(ctx))

	afterSnapshot := loadArenaLegacySnapshot(t, ctx, restartedPool)
	afterDuels := loadArenaLegacyDuelStates(t, ctx, restartedPool)
	afterDurableResults := loadArenaDurableResultSnapshot(
		t,
		ctx,
		restartedPool,
		submissionID,
		resultCommit,
	)
	require.Equal(t, beforeSnapshot, afterSnapshot,
		"forward Arena migrations must not lose or corrupt legacy casual rows")
	require.Equal(t, beforeDuels, afterDuels,
		"active and finished casual lifecycle timestamps must remain exact")
	require.Equal(t, beforeDurableResults, afterDurableResults,
		"append-only Arena submission and result evidence must survive migration and restart")

	tx, err := restartedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, err = tx.Exec(ctx, `SET TRANSACTION READ ONLY`)
	require.NoError(t, err)
	var maintenanceSubmissionCount int
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM arena_submission_events
		WHERE id = $1`, submissionID).Scan(&maintenanceSubmissionCount))
	require.Equal(t, 1, maintenanceSubmissionCount,
		"maintenance-mode reads must retain durable Arena evidence")
	require.NoError(t, tx.Commit(ctx))
}

func arenaLegacyFixturePath() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(
		filepath.Dir(thisFile),
		"..",
		"testdata",
		"migrations",
		"pre_arena_legacy.sql",
	)
}

func requireArenaMigrationVersion(
	t testing.TB,
	ctx context.Context,
	pool *pgxpool.Pool,
	expected int64,
) {
	t.Helper()

	var actual int64
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT MAX(version_id)
		FROM goose_db_version
		WHERE is_applied`,
	).Scan(&actual))
	require.Equal(t, expected, actual)
}

func loadArenaLegacySnapshot(
	t testing.TB,
	ctx context.Context,
	pool *pgxpool.Pool,
) arenaLegacySnapshot {
	t.Helper()

	load := func(query string) []byte {
		var payload string
		require.NoError(t, pool.QueryRow(ctx, query).Scan(&payload))
		return []byte(payload)
	}

	return arenaLegacySnapshot{
		players: load(`
			SELECT COALESCE(jsonb_agg(to_jsonb(source) ORDER BY source.id), '[]'::jsonb)::text
			FROM (
				SELECT * FROM players
				WHERE username LIKE 'migration_legacy_%'
			) AS source`),
		tasks: load(`
			SELECT COALESCE(jsonb_agg(to_jsonb(source) ORDER BY source.id), '[]'::jsonb)::text
			FROM (
				SELECT * FROM tasks
				WHERE id IN (
					'00000000-0000-4000-8000-000000000301',
					'00000000-0000-4000-8000-000000000302',
					'00000000-0000-4000-8000-000000000303',
					'00000000-0000-4000-8000-000000000304'
				)
			) AS source`),
		duels: load(`
			SELECT COALESCE(jsonb_agg(to_jsonb(source) ORDER BY source.id), '[]'::jsonb)::text
			FROM (
				SELECT * FROM duels
				WHERE id IN (
					'00000000-0000-4000-8000-000000000401',
					'00000000-0000-4000-8000-000000000402'
				)
			) AS source`),
		duelPlayerTasks: load(`
			SELECT COALESCE(
				jsonb_agg(to_jsonb(source) ORDER BY source.duel_id, source.player_id),
				'[]'::jsonb
			)::text
			FROM (
				SELECT * FROM duel_player_tasks
				WHERE duel_id IN (
					'00000000-0000-4000-8000-000000000401',
					'00000000-0000-4000-8000-000000000402'
				)
			) AS source`),
		playerTaskHistory: load(`
			SELECT COALESCE(
				jsonb_agg(to_jsonb(source) ORDER BY source.player_id, source.task_id),
				'[]'::jsonb
			)::text
			FROM (
				SELECT * FROM player_task_history
				WHERE player_id IN (
					'00000000-0000-4000-8000-000000000101',
					'00000000-0000-4000-8000-000000000103'
				)
			) AS source`),
		leaderboardOverrides: load(`
			SELECT COALESCE(
				jsonb_agg(to_jsonb(source) ORDER BY source.player_id),
				'[]'::jsonb
			)::text
			FROM (
				SELECT * FROM player_leaderboard_overrides
				WHERE player_id IN (
					'00000000-0000-4000-8000-000000000101',
					'00000000-0000-4000-8000-000000000103'
				)
			) AS source`),
	}
}

func loadArenaLegacyDuelStates(
	t testing.TB,
	ctx context.Context,
	pool *pgxpool.Pool,
) []arenaLegacyDuelState {
	t.Helper()

	rows, err := pool.Query(ctx, `
		SELECT id, status, winner_id, deadline, started_at, finished_at
		FROM duels
		WHERE id IN (
			'00000000-0000-4000-8000-000000000401',
			'00000000-0000-4000-8000-000000000402'
		)
		ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()

	states := make([]arenaLegacyDuelState, 0, 2)
	for rows.Next() {
		var state arenaLegacyDuelState
		require.NoError(t, rows.Scan(
			&state.id,
			&state.status,
			&state.winnerID,
			&state.deadline,
			&state.startedAt,
			&state.finishedAt,
		))
		states = append(states, state)
	}
	require.NoError(t, rows.Err())
	return states
}

func loadArenaDurableResultSnapshot(
	t testing.TB,
	ctx context.Context,
	pool *pgxpool.Pool,
	submissionID uuid.UUID,
	commit arenaResultAuditCommit,
) arenaDurableResultSnapshot {
	t.Helper()

	load := func(query string, identifier uuid.UUID) []byte {
		var payload string
		require.NoError(t, pool.QueryRow(ctx, query, identifier).Scan(&payload))
		return []byte(payload)
	}

	return arenaDurableResultSnapshot{
		submissions: load(`
			SELECT COALESCE(jsonb_agg(to_jsonb(source) ORDER BY source.id), '[]'::jsonb)::text
			FROM (SELECT * FROM arena_submission_events WHERE id = $1) AS source`, submissionID),
		results: load(`
			SELECT COALESCE(jsonb_agg(to_jsonb(source) ORDER BY source.id), '[]'::jsonb)::text
			FROM (SELECT * FROM arena_result_events WHERE id = $1) AS source`, commit.resultEventID),
		commits: load(`
			SELECT COALESCE(jsonb_agg(to_jsonb(source) ORDER BY source.id), '[]'::jsonb)::text
			FROM (SELECT * FROM arena_result_commits WHERE result_event_id = $1) AS source`, commit.resultEventID),
		audit: load(`
			SELECT COALESCE(jsonb_agg(to_jsonb(source) ORDER BY source.id), '[]'::jsonb)::text
			FROM (SELECT * FROM arena_audit_events WHERE id = $1) AS source`, commit.auditEventID),
		outbox: load(`
			SELECT COALESCE(jsonb_agg(to_jsonb(source) ORDER BY source.id), '[]'::jsonb)::text
			FROM (SELECT * FROM arena_outbox_events WHERE id = $1) AS source`, commit.outboxEventID),
	}
}
