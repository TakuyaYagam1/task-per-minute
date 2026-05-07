-- name: CreateAdminPlayerAuditEvent :exec
INSERT INTO admin_player_audit_events (
    actor_subject,
    actor_jti,
    action,
    player_id,
    before_state,
    after_state,
    created_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListAdminPlayerAuditEventsByPlayer :many
SELECT id,
    actor_subject,
    actor_jti,
    action,
    player_id,
    before_state,
    after_state,
    created_at
FROM admin_player_audit_events
WHERE player_id = $1
ORDER BY created_at DESC,
    id DESC
LIMIT $2;
