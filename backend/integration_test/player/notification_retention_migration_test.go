//go:build integration

package player_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	notificationrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/notification"
)

func TestPlayerNotificationRetentionMigrationKeepsLegacyExpiryAndGuardsRollback(t *testing.T) {
	ctx := context.Background()
	pool, database := testkit.CreateIsolatedDatabase(ctx, t, sharedPool, "player_notification_retention")
	migrationsDir := migrationsDirAbs()
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpToContext(ctx, database, migrationsDir, 39))

	playerID := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO players (id, username)
		VALUES ($1, $2)`, playerID, uniq("notification_retention"))
	require.NoError(t, err)

	tournamentID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO tournaments (id, name)
		VALUES ($1, $2)`, tournamentID, uniq("notification_tournament"))
	require.NoError(t, err)

	legacyActiveCreatedAt := time.Now().UTC().Add(-5 * time.Minute).Truncate(time.Microsecond)
	legacyExpiredCreatedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	legacyActiveID := insertNotificationRetentionRow(
		t, ctx, pool, playerID, tournamentID, legacyActiveCreatedAt,
		legacyActiveCreatedAt.Add(30*time.Minute),
	)
	legacyExpiredID := insertNotificationRetentionRow(
		t, ctx, pool, playerID, tournamentID, legacyExpiredCreatedAt,
		legacyExpiredCreatedAt.Add(30*time.Minute),
	)

	require.NoError(t, goose.UpToContext(ctx, database, migrationsDir, 40))
	assertNotificationRetentionExpiry(
		t, ctx, pool, legacyActiveID, legacyActiveCreatedAt, legacyActiveCreatedAt.Add(30*time.Minute),
	)
	assertNotificationRetentionExpiry(
		t, ctx, pool, legacyExpiredID, legacyExpiredCreatedAt, legacyExpiredCreatedAt.Add(30*time.Minute),
	)

	var activeLegacyCount int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*)
		FROM player_notifications
		WHERE player_id = $1
			AND expires_at > statement_timestamp()`, playerID).Scan(&activeLegacyCount))
	require.Equal(t, 1, activeLegacyCount, "migration must leave the active legacy notification active")
	var expiredLegacyActive bool
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT expires_at > statement_timestamp()
		FROM player_notifications
		WHERE id = $1`, legacyExpiredID).Scan(&expiredLegacyActive))
	require.False(t, expiredLegacyActive, "migration must not make an expired legacy notification active")

	repository := notificationrepo.NewRepository(postgres.NewTxManager(pool))
	require.NoError(t, repository.CreatePlayerRemoved(ctx, playerID, tournamentID))
	var createdAt, expiresAt time.Time
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT created_at, expires_at
		FROM player_notifications
		WHERE player_id = $1
			AND expires_at = created_at + interval '24 hours'`, playerID).Scan(&createdAt, &expiresAt))
	require.True(t, expiresAt.Equal(createdAt.Add(24*time.Hour)), "new notifications must expire after 24 hours")

	invalidCreatedAt := time.Now().UTC().Truncate(time.Microsecond)
	_, err = pool.Exec(ctx, `
		INSERT INTO player_notifications (
			player_id, notification_type, tournament_id, tournament_name, created_at, expires_at
		)
		VALUES ($1, 'tournament_player_removed', $2, 'Retention test', $3, $4)`,
		playerID, tournamentID, invalidCreatedAt, invalidCreatedAt.Add(time.Hour))
	require.ErrorContains(t, err, "player_notifications_timestamps_check")

	err = goose.DownToContext(ctx, database, migrationsDir, 39)
	require.ErrorContains(t, err, "cannot roll back player notification retention while 24-hour notifications exist")
	var appliedVersion int64
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT max(version_id)
		FROM goose_db_version
		WHERE is_applied`).Scan(&appliedVersion))
	require.EqualValues(t, 40, appliedVersion, "a rejected rollback must keep migration 40 applied")

	_, err = pool.Exec(ctx, `
		DELETE FROM player_notifications
		WHERE player_id = $1
			AND expires_at = created_at + interval '24 hours'`, playerID)
	require.NoError(t, err)
	require.NoError(t, goose.DownToContext(ctx, database, migrationsDir, 39))

	_, err = pool.Exec(ctx, `
		INSERT INTO player_notifications (
			player_id, notification_type, tournament_id, tournament_name, created_at, expires_at
		)
		VALUES ($1, 'tournament_player_removed', $2, 'Retention test', $3, $4)`,
		playerID, tournamentID, invalidCreatedAt, invalidCreatedAt.Add(24*time.Hour))
	require.ErrorContains(t, err, "player_notifications_timestamps_check")
	assertNotificationRetentionExpiry(
		t, ctx, pool, legacyActiveID, legacyActiveCreatedAt, legacyActiveCreatedAt.Add(30*time.Minute),
	)
	assertNotificationRetentionExpiry(
		t, ctx, pool, legacyExpiredID, legacyExpiredCreatedAt, legacyExpiredCreatedAt.Add(30*time.Minute),
	)
}

func insertNotificationRetentionRow(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	playerID, tournamentID uuid.UUID,
	createdAt, expiresAt time.Time,
) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO player_notifications (
			player_id, notification_type, tournament_id, tournament_name, created_at, expires_at
		)
		VALUES ($1, 'tournament_player_removed', $2, 'Retention test', $3, $4)
		RETURNING id`, playerID, tournamentID, createdAt, expiresAt).Scan(&id)
	require.NoError(t, err)
	return id
}

func assertNotificationRetentionExpiry(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	id uuid.UUID,
	wantCreatedAt, wantExpiresAt time.Time,
) {
	t.Helper()
	var createdAt, expiresAt time.Time
	err := pool.QueryRow(ctx, `
		SELECT created_at, expires_at
		FROM player_notifications
		WHERE id = $1`, id).Scan(&createdAt, &expiresAt)
	require.NoError(t, err)
	require.True(t, createdAt.Equal(wantCreatedAt), "migration must preserve the notification creation time")
	require.True(t, expiresAt.Equal(wantExpiresAt), "migration must preserve the assigned expiry")
}
