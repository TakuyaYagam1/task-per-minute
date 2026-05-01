-- name: CreatePlayer :one
INSERT INTO players (username)
VALUES ($1)
RETURNING id,
    username,
    session_token,
    status,
    created_at;
-- name: GetPlayerByID :one
SELECT id,
    username,
    session_token,
    status,
    created_at
FROM players
WHERE id = $1;
-- name: GetPlayerByUsername :one
SELECT id,
    username,
    session_token,
    status,
    created_at
FROM players
WHERE username = $1;
-- name: GetPlayerBySessionToken :one
SELECT id,
    username,
    session_token,
    status,
    created_at
FROM players
WHERE session_token = $1;
-- name: UpdatePlayerSessionToken :one
UPDATE players
SET session_token = $2
WHERE id = $1
RETURNING id,
    username,
    session_token,
    status,
    created_at;
-- name: UpdatePlayerStatus :one
UPDATE players
SET status = $2
WHERE id = $1
RETURNING id,
    username,
    session_token,
    status,
    created_at;
