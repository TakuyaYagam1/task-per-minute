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
