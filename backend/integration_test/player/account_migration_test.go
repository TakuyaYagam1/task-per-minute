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
