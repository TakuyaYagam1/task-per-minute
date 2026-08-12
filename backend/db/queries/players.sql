-- name: CreatePlayer :one
INSERT INTO players (username)
VALUES ($1)
RETURNING id,
    username,
    session_token,
    status,
    created_at,
    deleted_at,
    session_expires_at;
-- name: UpsertPlayerSessionByUsername :one
INSERT INTO players (username, session_token, session_expires_at)
VALUES ($1, $2, $3) ON CONFLICT (username) DO
UPDATE
SET session_token = EXCLUDED.session_token,
    session_expires_at = EXCLUDED.session_expires_at
WHERE players.status = 'idle'
    AND players.deleted_at IS NULL
RETURNING id,
    username,
    session_token,
    status,
    created_at,
    deleted_at,
    session_expires_at;
-- name: GetPlayerByID :one
SELECT id,
    username,
    session_token,
    status,
    created_at,
    deleted_at,
    session_expires_at
FROM players
WHERE id = $1;
-- name: GetPlayerByUsername :one
SELECT id,
    username,
    session_token,
    status,
    created_at,
    deleted_at,
    session_expires_at
FROM players
WHERE username = $1
    AND deleted_at IS NULL;
-- name: GetPlayerBySessionToken :one
SELECT id,
    username,
    session_token,
    status,
    created_at,
    deleted_at,
    session_expires_at
FROM players
WHERE session_token = $1
    AND deleted_at IS NULL;
-- name: UpdatePlayerSessionToken :one
UPDATE players
SET session_token = $2,
    session_expires_at = $3
WHERE id = $1
    AND deleted_at IS NULL
RETURNING id,
    username,
    session_token,
    status,
    created_at,
    deleted_at,
    session_expires_at;
-- name: UpdatePlayerStatus :one
UPDATE players
SET status = $2
WHERE id = $1
    AND deleted_at IS NULL
RETURNING id,
    username,
    session_token,
    status,
    created_at,
    deleted_at,
    session_expires_at;
-- name: UpdatePlayerStatusIfCurrent :one
UPDATE players
SET status = $3
WHERE id = $1
    AND status = $2
    AND deleted_at IS NULL
RETURNING id,
    username,
    session_token,
    status,
    created_at,
    deleted_at,
    session_expires_at;

-- name: ResetQueuedPlayers :execrows
UPDATE players
SET status = 'idle'
WHERE status = 'queued'
    AND deleted_at IS NULL;

-- name: UpdatePlayerUsername :one
UPDATE players
SET username = $2
WHERE id = $1
    AND deleted_at IS NULL
RETURNING id,
    username,
    session_token,
    status,
    created_at,
    deleted_at,
    session_expires_at;

-- name: SoftDeleteIdlePlayer :one
UPDATE players
SET username = $2,
    session_token = NULL,
    session_expires_at = NULL,
    status = 'idle',
    deleted_at = $3
WHERE id = $1
    AND status = 'idle'
    AND deleted_at IS NULL
RETURNING id,
    username,
    session_token,
    status,
    created_at,
    deleted_at,
    session_expires_at;

-- name: UpsertPlayerLeaderboardOverride :one
INSERT INTO player_leaderboard_overrides (
    player_id,
    wins,
    average_solve_time_ms,
    updated_at
)
VALUES ($1, $2, $3, $4)
ON CONFLICT (player_id) DO UPDATE
SET wins = EXCLUDED.wins,
    average_solve_time_ms = EXCLUDED.average_solve_time_ms,
    updated_at = EXCLUDED.updated_at
RETURNING player_id,
    wins,
    average_solve_time_ms,
    updated_at;

-- name: GetAdminPlayer :one
WITH base_stats AS (
    SELECT d.winner_id AS player_id,
        COUNT(*)::INT AS wins,
        FLOOR(
            AVG(
                (
                    EXTRACT(
                        EPOCH
                        FROM dpt.solved_at - d.started_at
                    ) * 1000
                )::BIGINT
            )
        )::BIGINT AS average_solve_time_ms
    FROM duels d
        JOIN duel_player_tasks dpt ON dpt.duel_id = d.id
            AND dpt.player_id = d.winner_id
            AND dpt.solved = TRUE
            AND dpt.solved_at IS NOT NULL
    WHERE d.status = 'finished'
        AND d.winner_id IS NOT NULL
    GROUP BY d.winner_id
)
SELECT p.id,
    p.username,
    p.session_token,
    p.status,
    p.created_at,
    p.deleted_at,
    p.session_expires_at,
    COALESCE(o.wins, b.wins, 0)::INT AS wins,
    COALESCE(o.average_solve_time_ms, b.average_solve_time_ms, 0)::BIGINT AS average_solve_time_ms,
    (o.player_id IS NOT NULL)::BOOLEAN AS stats_overridden
FROM players p
    LEFT JOIN base_stats b ON b.player_id = p.id
    LEFT JOIN player_leaderboard_overrides o ON o.player_id = p.id
WHERE p.id = $1
    AND p.deleted_at IS NULL;

-- name: GetAdminPlayerIncludingDeleted :one
WITH base_stats AS (
    SELECT d.winner_id AS player_id,
        COUNT(*)::INT AS wins,
        FLOOR(
            AVG(
                (
                    EXTRACT(
                        EPOCH
                        FROM dpt.solved_at - d.started_at
                    ) * 1000
                )::BIGINT
            )
        )::BIGINT AS average_solve_time_ms
    FROM duels d
        JOIN duel_player_tasks dpt ON dpt.duel_id = d.id
            AND dpt.player_id = d.winner_id
            AND dpt.solved = TRUE
            AND dpt.solved_at IS NOT NULL
    WHERE d.status = 'finished'
        AND d.winner_id IS NOT NULL
    GROUP BY d.winner_id
)
SELECT p.id,
    p.username,
    p.session_token,
    p.status,
    p.created_at,
    p.deleted_at,
    p.session_expires_at,
    COALESCE(o.wins, b.wins, 0)::INT AS wins,
    COALESCE(o.average_solve_time_ms, b.average_solve_time_ms, 0)::BIGINT AS average_solve_time_ms,
    (o.player_id IS NOT NULL)::BOOLEAN AS stats_overridden
FROM players p
    LEFT JOIN base_stats b ON b.player_id = p.id
    LEFT JOIN player_leaderboard_overrides o ON o.player_id = p.id
WHERE p.id = $1;

-- name: ListAdminPlayers :many
WITH base_stats AS (
    SELECT d.winner_id AS player_id,
        COUNT(*)::INT AS wins,
        FLOOR(
            AVG(
                (
                    EXTRACT(
                        EPOCH
                        FROM dpt.solved_at - d.started_at
                    ) * 1000
                )::BIGINT
            )
        )::BIGINT AS average_solve_time_ms
    FROM duels d
        JOIN duel_player_tasks dpt ON dpt.duel_id = d.id
            AND dpt.player_id = d.winner_id
            AND dpt.solved = TRUE
            AND dpt.solved_at IS NOT NULL
    WHERE d.status = 'finished'
        AND d.winner_id IS NOT NULL
    GROUP BY d.winner_id
)
SELECT p.id,
    p.username,
    p.session_token,
    p.status,
    p.created_at,
    p.deleted_at,
    p.session_expires_at,
    COALESCE(o.wins, b.wins, 0)::INT AS wins,
    COALESCE(o.average_solve_time_ms, b.average_solve_time_ms, 0)::BIGINT AS average_solve_time_ms,
    (o.player_id IS NOT NULL)::BOOLEAN AS stats_overridden
FROM players p
    LEFT JOIN base_stats b ON b.player_id = p.id
    LEFT JOIN player_leaderboard_overrides o ON o.player_id = p.id
WHERE ($1::BOOLEAN OR p.deleted_at IS NULL)
ORDER BY p.created_at DESC,
    p.username ASC;
