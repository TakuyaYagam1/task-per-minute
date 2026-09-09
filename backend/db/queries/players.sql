-- name: CreatePlayer :one
INSERT INTO players (username)
VALUES ($1)
RETURNING id,
    username,
    session_token,
    created_at,
    deleted_at,
    session_expires_at;
-- name: ClaimPlayerSessionByUsername :one
INSERT INTO players AS target (username, session_token, session_expires_at)
VALUES ($1, $2, $3) ON CONFLICT (username) DO
UPDATE
SET session_token = EXCLUDED.session_token,
    session_expires_at = EXCLUDED.session_expires_at
WHERE target.deleted_at IS NULL
    AND (
        (
            target.session_token IS NULL
            AND target.session_expires_at IS NULL
        )
        OR target.session_expires_at <= CURRENT_TIMESTAMP
    )
RETURNING id,
    username,
    session_token,
    created_at,
    deleted_at,
    session_expires_at;
-- name: GetPlayerByID :one
SELECT id,
    username,
    session_token,
    created_at,
    deleted_at,
    session_expires_at
FROM players
WHERE id = $1;
-- name: GetPlayerByUsername :one
SELECT id,
    username,
    session_token,
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
    created_at,
    deleted_at,
    session_expires_at;
-- name: UpdatePlayerUsername :one
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

-- name: SoftDeletePlayer :one
UPDATE players
SET username = $2,
    session_token = NULL,
    session_expires_at = NULL,
    deleted_at = $3
WHERE id = $1
    AND NOT EXISTS (
        SELECT 1
        FROM participant_reservations AS reservation
        WHERE reservation.player_id = players.id
    )
    AND deleted_at IS NULL
RETURNING id,
    username,
    session_token,
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
    SELECT participant.player_id,
        COUNT(*)::INT AS wins,
        FLOOR(
            AVG(
                EXTRACT(
                    EPOCH
                    FROM submission.received_at - attempt.started_at
                ) * 1000
            )
        )::BIGINT AS average_solve_time_ms
    FROM official_result_heads AS head
        JOIN official_result_revisions AS revision
            ON revision.id = head.current_revision_id
            AND revision.entity_kind = 'game_attempt'
        JOIN result_events AS result
            ON result.id = revision.result_event_id
            AND result.result_state = 'completed'
            AND result.result_reason = 'solved'
        JOIN submission_events AS submission
            ON submission.id = result.submission_event_id
            AND submission.participant_id = revision.winner_id
            AND submission.status = 'accepted'
        JOIN game_attempts AS attempt
            ON attempt.id = revision.game_attempt_id
            AND attempt.started_at IS NOT NULL
        JOIN participants AS participant
            ON participant.roster_id = revision.roster_id
            AND participant.id = revision.winner_id
    WHERE head.entity_kind = 'game_attempt'
        AND revision.result_state = 'completed'
        AND revision.result_reason = 'solved'
        AND submission.received_at >= attempt.started_at
    GROUP BY participant.player_id
)
SELECT p.id,
    p.username,
    p.session_token,
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
    SELECT participant.player_id,
        COUNT(*)::INT AS wins,
        FLOOR(
            AVG(
                EXTRACT(
                    EPOCH
                    FROM submission.received_at - attempt.started_at
                ) * 1000
            )
        )::BIGINT AS average_solve_time_ms
    FROM official_result_heads AS head
        JOIN official_result_revisions AS revision
            ON revision.id = head.current_revision_id
            AND revision.entity_kind = 'game_attempt'
        JOIN result_events AS result
            ON result.id = revision.result_event_id
            AND result.result_state = 'completed'
            AND result.result_reason = 'solved'
        JOIN submission_events AS submission
            ON submission.id = result.submission_event_id
            AND submission.participant_id = revision.winner_id
            AND submission.status = 'accepted'
        JOIN game_attempts AS attempt
            ON attempt.id = revision.game_attempt_id
            AND attempt.started_at IS NOT NULL
        JOIN participants AS participant
            ON participant.roster_id = revision.roster_id
            AND participant.id = revision.winner_id
    WHERE head.entity_kind = 'game_attempt'
        AND revision.result_state = 'completed'
        AND revision.result_reason = 'solved'
        AND submission.received_at >= attempt.started_at
    GROUP BY participant.player_id
)
SELECT p.id,
    p.username,
    p.session_token,
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
    SELECT participant.player_id,
        COUNT(*)::INT AS wins,
        FLOOR(
            AVG(
                EXTRACT(
                    EPOCH
                    FROM submission.received_at - attempt.started_at
                ) * 1000
            )
        )::BIGINT AS average_solve_time_ms
    FROM official_result_heads AS head
        JOIN official_result_revisions AS revision
            ON revision.id = head.current_revision_id
            AND revision.entity_kind = 'game_attempt'
        JOIN result_events AS result
            ON result.id = revision.result_event_id
            AND result.result_state = 'completed'
            AND result.result_reason = 'solved'
        JOIN submission_events AS submission
            ON submission.id = result.submission_event_id
            AND submission.participant_id = revision.winner_id
            AND submission.status = 'accepted'
        JOIN game_attempts AS attempt
            ON attempt.id = revision.game_attempt_id
            AND attempt.started_at IS NOT NULL
        JOIN participants AS participant
            ON participant.roster_id = revision.roster_id
            AND participant.id = revision.winner_id
    WHERE head.entity_kind = 'game_attempt'
        AND revision.result_state = 'completed'
        AND revision.result_reason = 'solved'
        AND submission.received_at >= attempt.started_at
    GROUP BY participant.player_id
)
SELECT p.id,
    p.username,
    p.session_token,
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
