-- name: CreatePlayerRemovedNotification :execrows
WITH notification_time AS MATERIALIZED (
    SELECT clock_timestamp() AS created_at
), target_tournament AS MATERIALIZED (
    SELECT tournaments.id AS tournament_id, tournaments.name
    FROM tournaments
    WHERE tournaments.id = sqlc.arg(tournament_id)
), active_player AS MATERIALIZED (
    SELECT players.id AS player_id
    FROM players
    WHERE players.id = sqlc.arg(player_id)
        AND players.deleted_at IS NULL
)
INSERT INTO player_notifications (
    player_id,
    notification_type,
    tournament_id,
    tournament_name,
    created_at,
    expires_at
)
SELECT active_player.player_id,
    'tournament_player_removed',
    target_tournament.tournament_id,
    target_tournament.name,
    notification_time.created_at,
    notification_time.created_at + interval '24 hours'
FROM active_player, target_tournament, notification_time;

-- name: ListActivePlayerNotifications :many
SELECT id,
    notification_type,
    tournament_id,
    tournament_name,
    created_at,
    expires_at
FROM player_notifications
WHERE player_id = sqlc.arg(player_id)
    AND expires_at > statement_timestamp()
ORDER BY created_at DESC, id DESC
LIMIT 100;

-- name: DeleteExpiredPlayerNotifications :execrows
WITH expired AS MATERIALIZED (
    SELECT id
    FROM player_notifications
    WHERE expires_at <= statement_timestamp()
    ORDER BY expires_at, id
    LIMIT sqlc.arg(batch_size)
    FOR UPDATE SKIP LOCKED
)
DELETE FROM player_notifications AS notification
USING expired
WHERE notification.id = expired.id;
