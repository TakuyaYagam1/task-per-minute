package main

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const seedLockKey = "task-per-minute/test-accounts/v1"

var (
	errDatabaseUnavailable  = errors.New("test account database is unavailable")
	errDatabaseNameMismatch = errors.New("test account database name did not match the expected name")
	errIdentityConflict     = errors.New("test account identity is owned by another record")
)

type passwordHasher interface {
	Hash(password string) (string, error)
	Verify(password, encodedHash string) (bool, error)
}

func seedAccounts(ctx context.Context, pool *pgxpool.Pool, manifestPath string, value *manifest, hasher passwordHasher) (result error) {
	if pool == nil || value == nil || hasher == nil || validateManifest(*value) != nil {
		return errInvalidManifest
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return errDatabaseUnavailable
	}
	defer connection.Release()

	if _, err := connection.Exec(ctx, "SELECT pg_advisory_lock(hashtextextended($1, 0))", seedLockKey); err != nil {
		return errDatabaseUnavailable
	}
	defer func() {
		if unlockErr := releaseSeedLock(ctx, connection); unlockErr != nil && result == nil {
			result = unlockErr
		}
	}()
	if err := refreshManifest(manifestPath, value); err != nil {
		return err
	}
	if err := applySeedTransaction(ctx, connection, *value, hasher); err != nil {
		return err
	}

	updated := *value
	updated.Applied = true
	if err := writeManifest(manifestPath, updated, false); err != nil {
		return err
	}
	*value = updated
	return nil
}

func releaseSeedLock(ctx context.Context, connection *pgxpool.Conn) error {
	unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	var unlocked bool
	if err := connection.QueryRow(unlockCtx, "SELECT pg_advisory_unlock(hashtextextended($1, 0))", seedLockKey).Scan(&unlocked); err != nil || !unlocked {
		closeCtx, closeCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		_ = connection.Conn().Close(closeCtx)
		closeCancel()
		return errDatabaseUnavailable
	}
	return nil
}

func applySeedTransaction(ctx context.Context, connection *pgxpool.Conn, value manifest, hasher passwordHasher) error {
	transaction, err := connection.Begin(ctx)
	if err != nil {
		return errDatabaseUnavailable
	}
	committed := false
	defer func() {
		if !committed {
			rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			_ = transaction.Rollback(rollbackCtx)
			cancel()
		}
	}()

	if err := seedAccountsTransaction(ctx, transaction, value, hasher); err != nil {
		return err
	}
	if err := transaction.Commit(ctx); err != nil {
		return errDatabaseUnavailable
	}
	committed = true
	return nil
}

func refreshManifest(path string, loaded *manifest) error {
	current, err := readManifest(path)
	if err != nil {
		return err
	}
	if !sameManifestCredentials(*loaded, current) || (loaded.Applied && !current.Applied) {
		return errInvalidManifest
	}
	*loaded = current
	return nil
}

func sameManifestCredentials(left, right manifest) bool {
	if left.Version != right.Version || len(left.Accounts) != len(right.Accounts) {
		return false
	}
	for index := range left.Accounts {
		if left.Accounts[index] != right.Accounts[index] {
			return false
		}
	}
	return true
}

func seedAccountsTransaction(ctx context.Context, transaction pgx.Tx, value manifest, hasher passwordHasher) error {
	for _, account := range value.Accounts {
		playerID, _ := uuid.Parse(account.PlayerID)
		accountID, _ := uuid.Parse(account.AccountID)
		if err := ensurePlayer(ctx, transaction, account, playerID, value.Applied); err != nil {
			return err
		}
		if err := ensureAccount(ctx, transaction, account, playerID, accountID, value.Applied, hasher); err != nil {
			return err
		}
		if err := ensureUsernameReservation(ctx, transaction, account.Username, accountID, value.Applied); err != nil {
			return err
		}
	}
	return nil
}

func ensurePlayer(ctx context.Context, transaction pgx.Tx, account manifestAccount, playerID uuid.UUID, applied bool) error {
	if !applied {
		if _, err := transaction.Exec(ctx, `
			INSERT INTO public.players (id, username)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING`, playerID, account.Username); err != nil {
			return errDatabaseUnavailable
		}
	}
	var username string
	var deleted bool
	err := transaction.QueryRow(ctx, `
		SELECT username, deleted_at IS NOT NULL
		FROM public.players
		WHERE id = $1`, playerID).Scan(&username, &deleted)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (username != account.Username || deleted)) {
		return errIdentityConflict
	}
	if err != nil {
		return errDatabaseUnavailable
	}
	return nil
}

func ensureAccount(
	ctx context.Context,
	transaction pgx.Tx,
	account manifestAccount,
	playerID, accountID uuid.UUID,
	applied bool,
	hasher passwordHasher,
) error {
	if !applied {
		passwordHash, err := hasher.Hash(account.Password)
		if err != nil {
			return errCredentialGeneration
		}
		if _, err := transaction.Exec(ctx, `
			INSERT INTO public.player_accounts (
				id,
				player_id,
				username,
				username_normalized,
				email,
				email_normalized,
				password_hash,
				email_verified_at
			)
			VALUES ($1, $2, $3, $3, $4, $4, $5, now())
			ON CONFLICT DO NOTHING`, accountID, playerID, account.Username, account.Email, passwordHash); err != nil {
			return errDatabaseUnavailable
		}
	}

	var storedPlayerID uuid.UUID
	var storedUsername string
	var normalizedUsername string
	var storedEmail string
	var normalizedEmail string
	var passwordHash string
	var verified bool
	err := transaction.QueryRow(ctx, `
		SELECT player_id,
			username,
			username_normalized,
			email,
			email_normalized,
			password_hash,
			email_verified_at IS NOT NULL
		FROM public.player_accounts
		WHERE id = $1`, accountID).Scan(
		&storedPlayerID,
		&storedUsername,
		&normalizedUsername,
		&storedEmail,
		&normalizedEmail,
		&passwordHash,
		&verified,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return errIdentityConflict
	}
	if err != nil {
		return errDatabaseUnavailable
	}
	if storedPlayerID != playerID || storedUsername != account.Username || normalizedUsername != account.Username ||
		storedEmail != account.Email || normalizedEmail != account.Email || !verified {
		return errIdentityConflict
	}
	validPassword, err := hasher.Verify(account.Password, passwordHash)
	if err != nil || !validPassword {
		return errIdentityConflict
	}
	return nil
}

func ensureUsernameReservation(ctx context.Context, transaction pgx.Tx, username string, accountID uuid.UUID, applied bool) error {
	if !applied {
		if _, err := transaction.Exec(ctx, `
			INSERT INTO public.player_username_reservations (
				normalized_username,
				legacy_count,
				account_id
			)
			VALUES ($1, 0, $2)
			ON CONFLICT DO NOTHING`, username, accountID); err != nil {
			return errDatabaseUnavailable
		}
	}
	var reservationOwned bool
	if err := transaction.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM public.player_username_reservations
			WHERE normalized_username = $1
				AND legacy_count = 0
				AND account_id = $2
		)`, username, accountID).Scan(&reservationOwned); err != nil {
		return errDatabaseUnavailable
	}
	if !reservationOwned {
		return errIdentityConflict
	}
	return nil
}
