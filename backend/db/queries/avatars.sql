-- name: LockPlayerAvatarSession :one
SELECT id
FROM players
WHERE id = sqlc.arg(player_id)
    AND session_token = sqlc.arg(session_token)
    AND session_expires_at > statement_timestamp()
    AND deleted_at IS NULL
FOR UPDATE;

-- name: IsDeletedPlayerAvatarSession :one
SELECT EXISTS (
    SELECT 1
    FROM player_session_tombstones
    WHERE session_token_hash = sqlc.arg(session_token_hash)
        AND expires_at > statement_timestamp()
)::boolean AS is_deleted;

-- name: EnsurePlayerAvatarState :one
INSERT INTO player_avatar_state (player_id)
VALUES (sqlc.arg(player_id))
ON CONFLICT (player_id) DO UPDATE
SET updated_at = player_avatar_state.updated_at
RETURNING generation;

-- name: IncrementPlayerAvatarGeneration :one
INSERT INTO player_avatar_state (player_id, generation, updated_at)
VALUES (sqlc.arg(player_id), 1, clock_timestamp())
ON CONFLICT (player_id) DO UPDATE
SET generation = player_avatar_state.generation + 1,
    updated_at = clock_timestamp()
RETURNING generation;

-- name: CreatePlayerAvatarUploadIntent :execrows
INSERT INTO player_avatar_objects (
    object_key,
    player_id,
    generation,
    lifecycle_state,
    cleanup_after
)
VALUES (
    sqlc.arg(object_key),
    sqlc.arg(player_id),
    sqlc.arg(generation),
    'uploading',
    sqlc.arg(cleanup_after)
);

-- name: GetPlayerAvatar :one
SELECT player_id,
    object_key,
    content_type,
    size_bytes,
    sha256,
    updated_at
FROM player_avatars
WHERE player_id = sqlc.arg(player_id)
FOR UPDATE;

-- name: GetPublicPlayerAvatar :one
SELECT avatar.player_id,
    avatar.object_key,
    avatar.content_type,
    avatar.size_bytes,
    avatar.sha256,
    avatar.updated_at
FROM player_avatars AS avatar
JOIN player_avatar_objects AS object
    ON object.object_key = avatar.object_key
    AND object.player_id = avatar.player_id
    AND object.lifecycle_state = 'active'
JOIN players AS player
    ON player.id = avatar.player_id
    AND player.deleted_at IS NULL
WHERE avatar.player_id = sqlc.arg(player_id)
    AND avatar.sha256 = sqlc.arg(version_sha256);

-- name: DeletePlayerAvatar :execrows
DELETE FROM player_avatars
WHERE player_id = sqlc.arg(player_id);

-- name: ActivatePlayerAvatarObject :execrows
UPDATE player_avatar_objects
SET lifecycle_state = 'active',
    cleanup_after = NULL,
    claim_token = NULL,
    claim_until = NULL
WHERE object_key = sqlc.arg(object_key)
    AND player_id = sqlc.arg(player_id)
    AND generation = sqlc.arg(generation)
    AND lifecycle_state = 'uploading';

-- name: QueuePlayerAvatarObjectDeletion :execrows
UPDATE player_avatar_objects
SET lifecycle_state = 'deleting',
    cleanup_after = sqlc.arg(cleanup_after),
    claim_token = NULL,
    claim_until = NULL
WHERE object_key = sqlc.arg(object_key)
    AND lifecycle_state = 'active';

-- name: InsertPlayerAvatar :exec
INSERT INTO player_avatars (
    player_id,
    object_key,
    content_type,
    size_bytes,
    sha256,
    updated_at
)
VALUES (
    sqlc.arg(player_id),
    sqlc.arg(object_key),
    sqlc.arg(content_type),
    sqlc.arg(size_bytes),
    sqlc.arg(sha256),
    sqlc.arg(updated_at)
);

-- name: ClaimPlayerAvatarObjectCleanup :many
WITH candidates AS (
    SELECT candidate.object_key
    FROM player_avatar_objects AS candidate
    WHERE candidate.lifecycle_state IN ('uploading', 'deleting')
        AND candidate.cleanup_after <= sqlc.arg(now)
        AND (candidate.claim_until IS NULL OR candidate.claim_until <= sqlc.arg(now))
    ORDER BY candidate.cleanup_after, candidate.created_at, candidate.object_key
    LIMIT sqlc.arg(batch_size)
    FOR UPDATE SKIP LOCKED
)
UPDATE player_avatar_objects AS object
SET lifecycle_state = 'deleting',
    claim_token = sqlc.arg(claim_token),
    claim_until = sqlc.arg(lease_until)
FROM candidates
WHERE object.object_key = candidates.object_key
RETURNING object.object_key,
    object.cleanup_attempts;

-- name: CompletePlayerAvatarObjectCleanup :execrows
DELETE FROM player_avatar_objects
WHERE object_key = sqlc.arg(object_key)
    AND lifecycle_state = 'deleting'
    AND claim_token = sqlc.arg(claim_token);

-- name: RetryPlayerAvatarObjectCleanup :execrows
UPDATE player_avatar_objects
SET cleanup_after = sqlc.arg(retry_at),
    cleanup_attempts = cleanup_attempts + 1,
    claim_token = NULL,
    claim_until = NULL
WHERE object_key = sqlc.arg(object_key)
    AND lifecycle_state = 'deleting'
    AND claim_token = sqlc.arg(claim_token);
