//go:build integration

package player_test

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
)

func TestPlayerAccountMigrationPreservesLegacyNamesAndGuardsRollback(t *testing.T) {
	ctx := context.Background()
	pool, database := testkit.CreateIsolatedDatabase(ctx, t, sharedPool, "player_account_migration")
	migrationsDir := migrationsDirAbs()
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpToContext(ctx, database, migrationsDir, 34))

	_, err := pool.Exec(ctx, `INSERT INTO players (username) VALUES ('LegacyCase'), ('legacycase')`)
	require.NoError(t, err)
	var legacyRows int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM players WHERE lower(username) = 'legacycase'`).Scan(&legacyRows))
	require.Equal(t, 2, legacyRows)

	require.NoError(t, goose.UpToContext(ctx, database, migrationsDir, 35))
	var reservationCount int64
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT legacy_count
		FROM player_username_reservations
		WHERE normalized_username = 'legacycase'`).Scan(&reservationCount))
	require.EqualValues(t, 2, reservationCount)
	require.NoError(t, goose.DownToContext(ctx, database, migrationsDir, 34), "empty account tables should roll back")
	var accountsTableExists bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT to_regclass('player_accounts') IS NOT NULL`).Scan(&accountsTableExists))
	require.False(t, accountsTableExists)
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM players WHERE lower(username) = 'legacycase'`).Scan(&legacyRows))
	require.Equal(t, 2, legacyRows, "rolling the account migration back must preserve legacy players")

	require.NoError(t, goose.UpToContext(ctx, database, migrationsDir, 35))
	accountID := uuid.New()
	tokenHash := sha256.Sum256([]byte("synthetic-migration-verification-token"))
	now := time.Now().UTC()
	_, err = pool.Exec(ctx, `
		INSERT INTO player_accounts (
			id, username, username_normalized, email, email_normalized, password_hash,
			verification_token_hash, verification_expires_at, verification_sent_at
		)
		VALUES ($1, 'MigrationAccount', 'migrationaccount', 'migration@example.test',
			'migration@example.test', 'synthetic-test-hash', $2, $3, $4)`,
		accountID, tokenHash[:], now.Add(time.Hour), now)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO player_username_reservations (normalized_username, legacy_count, account_id)
		VALUES ('migrationaccount', 0, $1)`, accountID)
	require.NoError(t, err)

	err = goose.DownToContext(ctx, database, migrationsDir, 34)
	require.ErrorContains(t, err, "cannot roll back player accounts while account data exists")
	var accountRows int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM player_accounts WHERE id = $1`, accountID).Scan(&accountRows))
	require.Equal(t, 1, accountRows, "a rejected rollback must retain account data")
	require.NoError(t, pool.QueryRow(ctx, `SELECT legacy_count FROM player_username_reservations WHERE normalized_username = 'legacycase'`).Scan(&reservationCount))
	require.EqualValues(t, 2, reservationCount, "a rejected rollback must retain legacy reservations")
	var appliedVersion int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&appliedVersion))
	require.EqualValues(t, 35, appliedVersion)
}

func TestPlayerAccountDeletionMigrationCleansSoftDeletedAccounts(t *testing.T) {
	ctx := context.Background()
	pool, database := testkit.CreateIsolatedDatabase(ctx, t, sharedPool, "player_account_deletion_migration")
	migrationsDir := migrationsDirAbs()
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpToContext(ctx, database, migrationsDir, 35))

	deletedPlayerID := uuid.New()
	deletedAccountID := uuid.New()
	activePlayerID := uuid.New()
	activeAccountID := uuid.New()
	deletedAt := time.Now().UTC()
	_, err := pool.Exec(ctx, `
		INSERT INTO players (id, username, deleted_at)
		VALUES ($1, 'deleted-migration-player', $2)`, deletedPlayerID, deletedAt)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO player_accounts (
			id, player_id, username, username_normalized, email, email_normalized,
			password_hash, email_verified_at
		)
		VALUES ($1, $2, 'deleted-migration-player', 'deleted-migration-player',
			'deleted@example.test', 'deleted@example.test', 'synthetic-hash', $3)`,
		deletedAccountID, deletedPlayerID, deletedAt)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO player_username_reservations (normalized_username, legacy_count, account_id)
		VALUES ('deleted-migration-player', 0, $1), ('empty-migration-player', 0, NULL)`, deletedAccountID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO player_leaderboard_overrides (player_id, wins, average_solve_time_ms)
		VALUES ($1, 1, 100)`, deletedPlayerID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO players (id, username)
		VALUES ($1, 'active-migration-player')`, activePlayerID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO player_accounts (
			id, player_id, username, username_normalized, email, email_normalized,
			password_hash, email_verified_at
		)
		VALUES ($1, $2, 'active-migration-player', 'active-migration-player',
			'active@example.test', 'active@example.test', 'synthetic-hash', $3)`,
		activeAccountID, activePlayerID, deletedAt)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO player_username_reservations (normalized_username, legacy_count, account_id)
		VALUES ('active-migration-player', 0, $1)`, activeAccountID)
	require.NoError(t, err)

	require.NoError(t, goose.UpToContext(ctx, database, migrationsDir, 36))

	var deletedPlayerRetained, deletedAccountRemoved, deletedReservationRemoved bool
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT deleted_at IS NOT NULL FROM players WHERE id = $1`, deletedPlayerID).Scan(&deletedPlayerRetained))
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT NOT EXISTS (SELECT 1 FROM player_accounts WHERE id = $1)
			AND NOT EXISTS (
				SELECT 1 FROM player_username_reservations
				WHERE normalized_username = 'deleted-migration-player'
			)`, deletedAccountID).Scan(&deletedAccountRemoved))
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT NOT EXISTS (
			SELECT 1 FROM player_username_reservations WHERE normalized_username = 'empty-migration-player'
		)`).Scan(&deletedReservationRemoved))
	require.True(t, deletedPlayerRetained)
	require.True(t, deletedAccountRemoved)
	require.True(t, deletedReservationRemoved)

	var deletedOverrideCount, activeAccountCount, activeReservationCount int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM player_leaderboard_overrides WHERE player_id = $1`, deletedPlayerID).Scan(&deletedOverrideCount))
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM player_accounts WHERE id = $1 AND player_id = $2`, activeAccountID, activePlayerID).Scan(&activeAccountCount))
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM player_username_reservations
		WHERE normalized_username = 'active-migration-player' AND account_id = $1`, activeAccountID).Scan(&activeReservationCount))
	require.Zero(t, deletedOverrideCount)
	require.Equal(t, 1, activeAccountCount)
	require.Equal(t, 1, activeReservationCount)
}
