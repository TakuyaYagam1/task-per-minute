//go:build integration

package main

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	auth "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const integrationDatabaseName = "seed_tests"

func TestIntegrationSeedAccounts(t *testing.T) {
	dsn, exists := os.LookupEnv("TEST_ACCOUNTS_TEST_DSN")
	if !exists || strings.TrimSpace(dsn) == "" {
		t.Fatal("integration test DSN was not provided")
	}

	adminPool := integrationAdminPool(t, dsn)
	defer adminPool.Close()

	t.Run("partial existing account and idempotent rerun", func(t *testing.T) {
		pool, name, cloneDSN := integrationDatabase(t, adminPool, dsn)
		manifestPath := filepath.Join(t.TempDir(), "accounts.json")
		value, err := loadOrCreateManifest(manifestPath)
		if err != nil {
			t.Fatal("manifest could not be created")
		}
		playerID := parseFixtureUUID(t, value.Accounts[0].PlayerID)
		sessionToken, err := uuid.NewRandom()
		if err != nil {
			t.Fatal("session fixture could not be created")
		}
		sessionExpiry := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Microsecond)
		if _, err := pool.Exec(context.Background(), `
			INSERT INTO public.players (id, username, session_token, session_expires_at)
			VALUES ($1, $2, $3, $4)`, playerID, value.Accounts[0].Username, sessionToken, sessionExpiry); err != nil {
			t.Fatal("partial player fixture could not be created")
		}
		if _, err := pool.Exec(context.Background(), `
			INSERT INTO public.player_leaderboard_overrides (player_id, wins, average_solve_time_ms)
			VALUES ($1, 3, 2500)`, playerID); err != nil {
			t.Fatal("leaderboard fixture could not be created")
		}

		wrongTargetPath := filepath.Join(t.TempDir(), "blocked.json")
		if _, err := run(context.Background(), mapLookup(seedEnvironment(cloneDSN, "another_database", wrongTargetPath))); !errors.Is(err, errDatabaseNameMismatch) {
			t.Fatal("database name guard did not reject a different target")
		}
		if _, err := os.Lstat(wrongTargetPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("manifest was written before the database guard passed")
		}

		environment := seedEnvironment(cloneDSN, name, manifestPath)
		if _, err := run(context.Background(), mapLookup(environment)); err != nil {
			t.Fatal("seed failed for the isolated database")
		}
		loaded, err := readManifest(manifestPath)
		if err != nil || !loaded.Applied {
			t.Fatal("successful seed state was not persisted")
		}
		accountID := parseFixtureUUID(t, loaded.Accounts[0].AccountID)
		var originalPasswordHash string
		var originalVerifiedAt time.Time
		if err := pool.QueryRow(context.Background(), `
			SELECT password_hash, email_verified_at
			FROM public.player_accounts
			WHERE id = $1`, accountID).Scan(&originalPasswordHash, &originalVerifiedAt); err != nil {
			t.Fatal("seeded credentials could not be read")
		}
		var originalSession uuid.UUID
		var originalExpiry time.Time
		if err := pool.QueryRow(context.Background(), `
			SELECT session_token, session_expires_at
			FROM public.players
			WHERE id = $1`, playerID).Scan(&originalSession, &originalExpiry); err != nil {
			t.Fatal("existing player session could not be read")
		}
		var originalWins int
		var originalSolveTime int64
		if err := pool.QueryRow(context.Background(), `
			SELECT wins, average_solve_time_ms
			FROM public.player_leaderboard_overrides
			WHERE player_id = $1`, playerID).Scan(&originalWins, &originalSolveTime); err != nil {
			t.Fatal("existing player statistics could not be read")
		}

		if _, err := run(context.Background(), mapLookup(environment)); err != nil {
			t.Fatal("idempotent seed rerun failed")
		}
		var playerCount int
		if err := pool.QueryRow(context.Background(), `
			SELECT count(*) FROM public.players WHERE username LIKE 'demo%'`).Scan(&playerCount); err != nil || playerCount != accountCount {
			t.Fatal("seed did not create exactly sixteen players")
		}
		var verifiedAccountCount int
		if err := pool.QueryRow(context.Background(), `
			SELECT count(*)
			FROM public.player_accounts
			WHERE username LIKE 'demo%'
				AND email_verified_at IS NOT NULL`).Scan(&verifiedAccountCount); err != nil || verifiedAccountCount != accountCount {
			t.Fatal("seed did not create sixteen verified accounts")
		}
		valid, err := auth.NewPasswordHasher().Verify(loaded.Accounts[0].Password, originalPasswordHash)
		if err != nil || !valid {
			t.Fatal("seeded password was not accepted by the application hasher")
		}

		var passwordHashAfterRerun string
		var verifiedAtAfterRerun time.Time
		if err := pool.QueryRow(context.Background(), `
			SELECT password_hash, email_verified_at
			FROM public.player_accounts
			WHERE id = $1`, accountID).Scan(&passwordHashAfterRerun, &verifiedAtAfterRerun); err != nil ||
			passwordHashAfterRerun != originalPasswordHash || !verifiedAtAfterRerun.Equal(originalVerifiedAt) {
			t.Fatal("existing account credentials or verification state changed during rerun")
		}
		var preservedSession uuid.UUID
		var preservedExpiry time.Time
		if err := pool.QueryRow(context.Background(), `
			SELECT session_token, session_expires_at
			FROM public.players
			WHERE id = $1`, playerID).Scan(&preservedSession, &preservedExpiry); err != nil ||
			preservedSession != originalSession || !preservedExpiry.Equal(originalExpiry) {
			t.Fatal("existing player session changed during rerun")
		}
		var preservedWins int
		var preservedSolveTime int64
		if err := pool.QueryRow(context.Background(), `
			SELECT wins, average_solve_time_ms
			FROM public.player_leaderboard_overrides
			WHERE player_id = $1`, playerID).Scan(&preservedWins, &preservedSolveTime); err != nil ||
			preservedWins != originalWins || preservedSolveTime != originalSolveTime {
			t.Fatal("existing player statistics changed during rerun")
		}

		deleteFixtureAccount(t, pool, accountID, playerID)
		if _, err := run(context.Background(), mapLookup(environment)); !errors.Is(err, errIdentityConflict) {
			t.Fatal("rerun recreated a deleted account")
		}
		var accountStillMissing bool
		if err := pool.QueryRow(context.Background(), `
			SELECT NOT EXISTS (SELECT 1 FROM public.player_accounts WHERE id = $1)`, accountID).Scan(&accountStillMissing); err != nil || !accountStillMissing {
			t.Fatal("deleted account was restored")
		}
	})

	t.Run("committed rows recover before applied marker", func(t *testing.T) {
		pool, name, cloneDSN := integrationDatabase(t, adminPool, dsn)
		manifestPath := filepath.Join(t.TempDir(), "accounts.json")
		value, err := loadOrCreateManifest(manifestPath)
		if err != nil {
			t.Fatal("manifest could not be created")
		}
		transaction, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatal("recovery transaction could not start")
		}
		if err := seedAccountsTransaction(context.Background(), transaction, value, auth.NewPasswordHasher()); err != nil {
			_ = transaction.Rollback(context.Background())
			t.Fatal("seed fixture transaction failed")
		}
		if err := transaction.Commit(context.Background()); err != nil {
			t.Fatal("seed fixture transaction could not commit")
		}

		playerID := parseFixtureUUID(t, value.Accounts[0].PlayerID)
		accountID := parseFixtureUUID(t, value.Accounts[0].AccountID)
		sessionToken, err := uuid.NewRandom()
		if err != nil {
			t.Fatal("session fixture could not be created")
		}
		sessionExpiry := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Microsecond)
		if _, err := pool.Exec(context.Background(), `
			UPDATE public.players SET session_token = $1, session_expires_at = $2 WHERE id = $3`,
			sessionToken, sessionExpiry, playerID); err != nil {
			t.Fatal("session fixture could not be saved")
		}
		var originalHash string
		var originalVerifiedAt time.Time
		if err := pool.QueryRow(context.Background(), `
			SELECT password_hash, email_verified_at FROM public.player_accounts WHERE id = $1`, accountID).
			Scan(&originalHash, &originalVerifiedAt); err != nil {
			t.Fatal("credential fixture could not be read")
		}
		loaded, err := readManifest(manifestPath)
		if err != nil || loaded.Applied {
			t.Fatal("manifest was unexpectedly marked applied")
		}

		environment := seedEnvironment(cloneDSN, name, manifestPath)
		if _, err := run(context.Background(), mapLookup(environment)); err != nil {
			t.Fatal("retry after a committed transaction failed")
		}
		if _, err := run(context.Background(), mapLookup(environment)); err != nil {
			t.Fatal("rerun after recovery failed")
		}
		var recoveredHash string
		var recoveredVerifiedAt time.Time
		if err := pool.QueryRow(context.Background(), `
			SELECT password_hash, email_verified_at FROM public.player_accounts WHERE id = $1`, accountID).
			Scan(&recoveredHash, &recoveredVerifiedAt); err != nil || recoveredHash != originalHash || !recoveredVerifiedAt.Equal(originalVerifiedAt) {
			t.Fatal("recovery changed existing account credentials or verification state")
		}
		var recoveredSession uuid.UUID
		var recoveredExpiry time.Time
		if err := pool.QueryRow(context.Background(), `
			SELECT session_token, session_expires_at FROM public.players WHERE id = $1`, playerID).
			Scan(&recoveredSession, &recoveredExpiry); err != nil || recoveredSession != sessionToken || !recoveredExpiry.Equal(sessionExpiry) {
			t.Fatal("recovery changed an active session")
		}
	})

	t.Run("conflicts roll back all inserts", func(t *testing.T) {
		for _, collision := range []struct {
			name         string
			playerName   string
			accountName  string
			accountEmail string
			reservedName string
			legacy       bool
		}{
			{name: "legacy reservation", reservedName: "demo16", legacy: true},
			{name: "username", playerName: "demo16", accountName: "demo16", accountEmail: "other@example.invalid", reservedName: "demo16"},
			{name: "email", playerName: "existing-email-owner", accountName: "existing-email-owner", accountEmail: "demo16@example.invalid", reservedName: "existing-email-owner"},
		} {
			t.Run(collision.name, func(t *testing.T) {
				pool, name, cloneDSN := integrationDatabase(t, adminPool, dsn)
				if collision.legacy {
					if _, err := pool.Exec(context.Background(), `
						INSERT INTO public.player_username_reservations (normalized_username, legacy_count)
						VALUES ($1, 1)`, collision.reservedName); err != nil {
						t.Fatal("legacy reservation fixture could not be created")
					}
				} else {
					insertCompetingAccount(t, pool, collision.playerName, collision.accountName, collision.accountEmail, collision.reservedName)
				}
				var playersBefore int
				var accountsBefore int
				if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM public.players`).Scan(&playersBefore); err != nil {
					t.Fatal("player count could not be read")
				}
				if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM public.player_accounts`).Scan(&accountsBefore); err != nil {
					t.Fatal("account count could not be read")
				}

				manifestPath := filepath.Join(t.TempDir(), "accounts.json")
				if _, err := loadOrCreateManifest(manifestPath); err != nil {
					t.Fatal("manifest could not be created")
				}
				environment := seedEnvironment(cloneDSN, name, manifestPath)
				if _, err := run(context.Background(), mapLookup(environment)); !errors.Is(err, errIdentityConflict) {
					t.Fatal("account uniqueness conflict was not rejected")
				}
				var playersAfter int
				var accountsAfter int
				if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM public.players`).Scan(&playersAfter); err != nil || playersAfter != playersBefore {
					t.Fatal("player inserts were not rolled back after a conflict")
				}
				if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM public.player_accounts`).Scan(&accountsAfter); err != nil || accountsAfter != accountsBefore {
					t.Fatal("account inserts were not rolled back after a conflict")
				}
				loaded, err := readManifest(manifestPath)
				if err != nil || loaded.Applied {
					t.Fatal("failed transaction was marked applied")
				}

				if collision.legacy {
					if _, err := pool.Exec(context.Background(), `
						DELETE FROM public.player_username_reservations WHERE normalized_username = $1`, collision.reservedName); err != nil {
						t.Fatal("conflicting fixture could not be removed")
					}
					if _, err := run(context.Background(), mapLookup(environment)); err != nil {
						t.Fatal("seed retry failed after the conflict was cleared")
					}
				}
			})
		}
	})
}

func integrationAdminPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil || config.ConnConfig.Database != integrationDatabaseName {
		t.Fatal("integration DSN did not target the designated template database")
	}
	config.ConnConfig.Database = "postgres"
	config.MinConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal("isolated PostgreSQL test service could not be reached")
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatal("isolated PostgreSQL test service could not be reached")
	}
	var ownsTemplate bool
	if err := pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM pg_database
			WHERE datname = $1 AND pg_get_userbyid(datdba) = current_user
		)`, integrationDatabaseName).Scan(&ownsTemplate); err != nil || !ownsTemplate {
		pool.Close()
		t.Fatal("integration role did not own the designated template database")
	}
	return pool
}

func integrationDatabase(t *testing.T, adminPool *pgxpool.Pool, dsn string) (*pgxpool.Pool, string, string) {
	t.Helper()
	name := "seed_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := (pgx.Identifier{name}).Sanitize()
	if _, err := adminPool.Exec(context.Background(), "CREATE DATABASE "+identifier+" TEMPLATE "+(pgx.Identifier{integrationDatabaseName}).Sanitize()); err != nil {
		t.Fatal("test-owned database clone could not be created")
	}

	var pool *pgxpool.Pool
	t.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := adminPool.Exec(ctx, "DROP DATABASE "+identifier); err != nil {
			t.Error("test-owned database clone could not be removed")
		}
	})

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("integration database connection could not be configured")
	}
	config.ConnConfig.Database = name
	config.MinConns = 0
	pool, err = pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal("test-owned database clone could not be reached")
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		pool = nil
		t.Fatal("test-owned database clone could not be reached")
	}
	cloneURL, err := url.Parse(dsn)
	if err != nil || (cloneURL.Scheme != "postgres" && cloneURL.Scheme != "postgresql") {
		t.Fatal("integration DSN was not a PostgreSQL URL")
	}
	cloneURL.Path = "/" + name
	return pool, name, cloneURL.String()
}

func seedEnvironment(dsn, expectedDB, manifestPath string) map[string]string {
	return map[string]string{
		enabledEnvName:      "true",
		dsnEnvName:          dsn,
		expectedDBEnvName:   expectedDB,
		manifestPathEnvName: manifestPath,
	}
}

func insertCompetingAccount(t *testing.T, pool *pgxpool.Pool, playerName, accountName, email, reservedName string) {
	t.Helper()
	playerID, err := uuid.NewRandom()
	if err != nil {
		t.Fatal("collision fixture player ID could not be created")
	}
	accountID, err := uuid.NewRandom()
	if err != nil {
		t.Fatal("collision fixture account ID could not be created")
	}
	passwordHash, err := auth.NewPasswordHasher().Hash("Other-Tpm-7")
	if err != nil {
		t.Fatal("collision fixture password hash could not be created")
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO public.players (id, username) VALUES ($1, $2)`, playerID, playerName); err != nil {
		t.Fatal("collision fixture player could not be created")
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO public.player_accounts (
			id, player_id, username, username_normalized, email, email_normalized, password_hash, email_verified_at
		)
		VALUES ($1, $2, $3, $3, $4, $4, $5, now())`, accountID, playerID, accountName, email, passwordHash); err != nil {
		t.Fatal("collision fixture account could not be created")
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO public.player_username_reservations (normalized_username, legacy_count, account_id)
		VALUES ($1, 0, $2)`, reservedName, accountID); err != nil {
		t.Fatal("collision fixture username reservation could not be created")
	}
}

func deleteFixtureAccount(t *testing.T, pool *pgxpool.Pool, accountID, playerID uuid.UUID) {
	t.Helper()
	transaction, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal("deleted account fixture transaction could not start")
	}
	if _, err := transaction.Exec(context.Background(), `DELETE FROM public.player_username_reservations WHERE account_id = $1`, accountID); err != nil {
		_ = transaction.Rollback(context.Background())
		t.Fatal("deleted account fixture could not be created")
	}
	if _, err := transaction.Exec(context.Background(), `DELETE FROM public.player_accounts WHERE id = $1`, accountID); err != nil {
		_ = transaction.Rollback(context.Background())
		t.Fatal("deleted account fixture could not be created")
	}
	if _, err := transaction.Exec(context.Background(), `
		UPDATE public.players
		SET deleted_at = now(), session_token = NULL, session_expires_at = NULL
		WHERE id = $1`, playerID); err != nil {
		_ = transaction.Rollback(context.Background())
		t.Fatal("deleted account fixture could not be created")
	}
	if err := transaction.Commit(context.Background()); err != nil {
		t.Fatal("deleted account fixture could not be created")
	}
}

func parseFixtureUUID(t *testing.T, value string) uuid.UUID {
	t.Helper()
	parsed, err := uuid.Parse(value)
	if err != nil {
		t.Fatal("manifest UUID was invalid")
	}
	return parsed
}
