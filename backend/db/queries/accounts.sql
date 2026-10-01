-- name: LockPlayerUsername :exec
SELECT pg_advisory_xact_lock(hashtextextended($1, 81604431));

-- name: GetPlayerAccountByEmail :one
SELECT id
FROM player_accounts
WHERE email_normalized = $1;

-- name: GetPlayerAccountByUsername :one
SELECT id
FROM player_accounts
WHERE username_normalized = $1;

-- name: GetPlayerAccountForDeletion :one
SELECT id,
    username_normalized,
    email_normalized
FROM player_accounts
WHERE player_id = $1
FOR UPDATE;

-- name: DeletePlayerAccountUsernameReservation :exec
DELETE FROM player_username_reservations
WHERE account_id = $1;

-- name: DeletePlayerAccountForDeletion :execrows
DELETE FROM player_accounts
WHERE id = $1
    AND player_id = $2;

-- name: GetPlayerUsernameReservation :one
SELECT normalized_username,
    legacy_count,
    account_id
FROM player_username_reservations
WHERE normalized_username = $1;

-- name: PlayerUsernameExists :one
SELECT EXISTS (
    SELECT 1 FROM players WHERE lower(username) = $1
)::boolean AS exists;

-- name: CreateLegacyPlayerUsernameReservation :execrows
INSERT INTO player_username_reservations (normalized_username, legacy_count)
VALUES ($1, 1)
ON CONFLICT (normalized_username) DO NOTHING;

-- name: DecrementLegacyPlayerUsernameReservation :exec
UPDATE player_username_reservations
SET legacy_count = legacy_count - 1
WHERE normalized_username = $1
    AND legacy_count > 0
    AND account_id IS NULL;

-- name: DeleteEmptyLegacyPlayerUsernameReservation :exec
DELETE FROM player_username_reservations
WHERE normalized_username = $1
    AND legacy_count = 0
    AND account_id IS NULL;

-- name: CreatePendingPlayerAccount :one
INSERT INTO player_accounts (
    username,
    username_normalized,
    email,
    email_normalized,
    password_hash,
    verification_token_hash,
    verification_expires_at,
    verification_sent_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (email_normalized) DO NOTHING
RETURNING id;

-- name: ReservePlayerAccountUsername :execrows
INSERT INTO player_username_reservations (normalized_username, legacy_count, account_id)
VALUES ($1, 0, $2)
ON CONFLICT (normalized_username) DO NOTHING;

-- name: FindPlayerLoginCredentials :one
SELECT account.username,
    account.username_normalized,
    account.email,
    account.email_normalized,
    account.password_hash,
    player.id AS player_id,
    account.email_verified_at
FROM player_accounts AS account
LEFT JOIN players AS player
    ON player.id = account.player_id
    AND player.deleted_at IS NULL
WHERE account.username_normalized = $1
    OR account.email_normalized = $1;

-- name: ReplacePendingPlayerVerification :one
UPDATE player_accounts
SET verification_token_hash = sqlc.arg(token_hash),
    verification_expires_at = sqlc.arg(expires_at),
    verification_sent_at = sqlc.arg(sent_at)
WHERE email_normalized = sqlc.arg(email_normalized)
    AND email_verified_at IS NULL
    AND verification_sent_at <= sqlc.arg(eligible_before)
RETURNING email;

-- name: GetPendingPlayerAccountByToken :one
SELECT id,
    username,
    username_normalized,
    email,
    email_normalized
FROM player_accounts
WHERE verification_token_hash = $1
    AND email_verified_at IS NULL
    AND verification_expires_at > $2
FOR UPDATE;

-- name: CreateVerifiedPlayer :one
INSERT INTO players (username)
VALUES ($1)
RETURNING id,
    username,
    session_token,
    created_at,
    deleted_at,
    session_expires_at;

-- name: CompletePlayerAccountVerification :execrows
UPDATE player_accounts
SET player_id = $2,
    email_verified_at = $3,
    verification_token_hash = NULL,
    verification_expires_at = NULL,
    verification_sent_at = NULL
WHERE id = $1
    AND player_id IS NULL
    AND email_verified_at IS NULL;

-- name: UpdateAccountPlayerSession :one
UPDATE players AS player
SET session_token = $2,
    session_expires_at = $3
WHERE player.id = $1
    AND player.deleted_at IS NULL
    AND EXISTS (
        SELECT 1 FROM player_accounts AS account
        WHERE account.player_id = player.id
            AND account.email_verified_at IS NOT NULL
    )
RETURNING player.id,
    player.username,
    player.session_token,
    player.created_at,
    player.deleted_at,
    player.session_expires_at;

-- name: PlayerHasAccount :one
SELECT EXISTS (
    SELECT 1 FROM player_accounts WHERE player_id = $1
)::boolean AS has_account;

-- name: LockPlayerForLogin :one
SELECT id,
    username,
    session_token,
    created_at,
    deleted_at,
    session_expires_at
FROM players
WHERE id = $1
    AND deleted_at IS NULL
FOR UPDATE;

-- name: LockPlayerAccountCredentialsForLogin :one
SELECT password_hash,
    username_normalized,
    email_normalized,
    email_verified_at
FROM player_accounts
WHERE player_id = $1
    AND email_verified_at IS NOT NULL
FOR UPDATE;

-- name: LockPlayerAccountSettingsSession :one
SELECT id,
    username,
    session_token,
    created_at,
    deleted_at,
    session_expires_at
FROM players
WHERE id = $1
    AND session_token = sqlc.arg(session_token)
    AND session_expires_at > sqlc.arg(now)
    AND deleted_at IS NULL
FOR UPDATE;

-- name: IsDeletedPlayerAccountSession :one
SELECT EXISTS (
    SELECT 1
    FROM player_session_tombstones
    WHERE session_token_hash = $1
        AND expires_at > $2
)::boolean AS is_deleted;

-- name: GetPlayerAccountSettingsForUpdate :one
SELECT id,
    username_normalized,
    email,
    email_normalized,
    password_hash,
    pending_email,
    pending_email_normalized,
    email_change_code_hash,
    email_change_expires_at,
    email_change_last_sent_at,
    email_change_send_window_started_at,
    email_change_send_count,
    email_change_attempt_window_started_at,
    email_change_attempt_count
FROM player_accounts
WHERE player_id = $1
    AND email_verified_at IS NOT NULL
FOR UPDATE;

-- name: PlayerAccountEmailExists :one
SELECT id
FROM player_accounts
WHERE email_normalized = $1;

-- name: PlayerUsernameExistsExcept :one
SELECT EXISTS (
    SELECT 1 FROM players
    WHERE lower(username) = $1
        AND id <> $2
)::boolean AS exists;

-- name: UpdatePlayerAccountUsername :execrows
UPDATE player_accounts
SET username = $2,
    username_normalized = $3
WHERE id = $1
    AND player_id IS NOT NULL;

-- name: UpdatePlayerAccountUsernameReservation :execrows
UPDATE player_username_reservations
SET normalized_username = sqlc.arg(new_normalized_username)
WHERE account_id = $1
    AND normalized_username = sqlc.arg(current_normalized_username);

-- name: UpdateAccountBackedPlayerUsername :one
UPDATE players
SET username = $2
WHERE id = $1
    AND deleted_at IS NULL
RETURNING id,
    username,
    session_token,
    created_at,
    deleted_at,
    session_expires_at;

-- name: UpdatePlayerAccountPasswordHash :execrows
UPDATE player_accounts
SET password_hash = $2
WHERE id = $1
    AND player_id IS NOT NULL
    AND email_verified_at IS NOT NULL;

-- name: UpdateAccountPlayerSessionForSettings :one
UPDATE players AS player
SET session_token = sqlc.arg(new_session_token),
    session_expires_at = sqlc.arg(new_session_expires_at)
WHERE player.id = $1
    AND player.session_token = sqlc.arg(current_session_token)
    AND player.session_expires_at > sqlc.arg(now)
    AND player.deleted_at IS NULL
    AND EXISTS (
        SELECT 1 FROM player_accounts AS account
        WHERE account.player_id = player.id
            AND account.email_verified_at IS NOT NULL
    )
RETURNING player.id,
    player.username,
    player.session_token,
    player.created_at,
    player.deleted_at,
    player.session_expires_at;

-- name: StartPlayerEmailChange :execrows
UPDATE player_accounts
SET pending_email = $2,
    pending_email_normalized = $3,
    email_change_code_hash = $4,
    email_change_expires_at = $5,
    email_change_last_sent_at = $6,
    email_change_send_window_started_at = $7,
    email_change_send_count = $8,
    email_change_attempt_window_started_at = $9,
    email_change_attempt_count = $10
WHERE id = $1
    AND player_id IS NOT NULL
    AND email_verified_at IS NOT NULL;

-- name: RecordPlayerEmailChangeAttempt :execrows
UPDATE player_accounts
SET email_change_attempt_window_started_at = $2,
    email_change_attempt_count = $3
WHERE id = $1
    AND player_id IS NOT NULL
    AND email_verified_at IS NOT NULL;

-- name: CancelPlayerEmailChange :execrows
UPDATE player_accounts
SET pending_email = NULL,
    pending_email_normalized = NULL,
    email_change_code_hash = NULL,
    email_change_expires_at = NULL
WHERE id = $1
    AND player_id IS NOT NULL
    AND email_verified_at IS NOT NULL;

-- name: CompletePlayerEmailChange :execrows
UPDATE player_accounts
SET email = pending_email,
    email_normalized = pending_email_normalized,
    pending_email = NULL,
    pending_email_normalized = NULL,
    email_change_code_hash = NULL,
    email_change_expires_at = NULL
WHERE id = $1
    AND player_id = $2
    AND pending_email_normalized = $3
    AND email_change_code_hash = sqlc.arg(expected_code_hash)
    AND email_verified_at IS NOT NULL;
