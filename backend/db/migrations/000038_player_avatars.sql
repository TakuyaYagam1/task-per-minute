-- +goose Up
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

CREATE TABLE player_avatar_state (
    player_id uuid PRIMARY KEY REFERENCES players(id) ON DELETE CASCADE,
    generation bigint NOT NULL DEFAULT 0 CHECK (generation >= 0),
    updated_at timestamp with time zone NOT NULL DEFAULT now()
);

-- The object registry doubles as a durable upload-intent queue and cleanup
-- outbox. It intentionally has no player foreign key so pending deletes remain
-- available after a player's hard deletion.
CREATE TABLE player_avatar_objects (
    object_key text PRIMARY KEY,
    player_id uuid NOT NULL,
    generation bigint NOT NULL CHECK (generation >= 0),
    lifecycle_state text NOT NULL,
    cleanup_after timestamp with time zone,
    claim_token uuid,
    claim_until timestamp with time zone,
    cleanup_attempts integer NOT NULL DEFAULT 0 CHECK (cleanup_attempts >= 0),
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT player_avatar_objects_key_check CHECK (
        object_key ~ '^avatars/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}[.](jpg|png|gif)$'
    ),
    CONSTRAINT player_avatar_objects_state_check CHECK (
        (lifecycle_state = 'uploading' AND cleanup_after IS NOT NULL AND claim_token IS NULL AND claim_until IS NULL)
        OR (lifecycle_state = 'active' AND cleanup_after IS NULL AND claim_token IS NULL AND claim_until IS NULL)
        OR (lifecycle_state = 'deleting' AND cleanup_after IS NOT NULL
            AND ((claim_token IS NULL AND claim_until IS NULL) OR (claim_token IS NOT NULL AND claim_until IS NOT NULL)))
    )
);

CREATE INDEX player_avatar_objects_cleanup_idx
    ON player_avatar_objects (cleanup_after, claim_until, created_at)
    WHERE lifecycle_state <> 'active';

CREATE UNIQUE INDEX player_avatar_objects_one_active_idx
    ON player_avatar_objects (player_id)
    WHERE lifecycle_state = 'active';

CREATE TABLE player_avatars (
    player_id uuid PRIMARY KEY REFERENCES players(id) ON DELETE CASCADE,
    object_key text NOT NULL UNIQUE REFERENCES player_avatar_objects(object_key) ON DELETE RESTRICT,
    content_type text NOT NULL CHECK (content_type IN ('image/jpeg', 'image/png', 'image/gif')),
    size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 5242880),
    sha256 bytea NOT NULL CHECK (octet_length(sha256) = 32),
    updated_at timestamp with time zone NOT NULL DEFAULT now()
);

CREATE FUNCTION enqueue_player_avatar_cleanup_for_deleted_player() RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    target_player_id uuid;
BEGIN
    IF TG_OP = 'DELETE' THEN
        target_player_id := OLD.id;
    ELSE
        target_player_id := NEW.id;
    END IF;

    UPDATE player_avatar_state
    SET generation = generation + 1,
        updated_at = clock_timestamp()
    WHERE player_id = target_player_id;

    WITH removed_avatar AS (
        DELETE FROM player_avatars
        WHERE player_id = target_player_id
        RETURNING object_key
    )
    UPDATE player_avatar_objects AS object
    SET lifecycle_state = 'deleting',
        cleanup_after = clock_timestamp(),
        claim_token = NULL,
        claim_until = NULL
    FROM removed_avatar
    WHERE object.object_key = removed_avatar.object_key
      AND object.lifecycle_state = 'active';

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER player_avatar_soft_delete_cleanup
AFTER UPDATE OF deleted_at ON players
FOR EACH ROW
WHEN (OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL)
EXECUTE FUNCTION enqueue_player_avatar_cleanup_for_deleted_player();

CREATE TRIGGER player_avatar_hard_delete_cleanup
BEFORE DELETE ON players
FOR EACH ROW
EXECUTE FUNCTION enqueue_player_avatar_cleanup_for_deleted_player();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM player_avatar_objects) OR EXISTS (SELECT 1 FROM player_avatars) THEN
        RAISE EXCEPTION 'cannot roll back player avatars while avatar objects or cleanup work remain';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS player_avatar_hard_delete_cleanup ON players;
DROP TRIGGER IF EXISTS player_avatar_soft_delete_cleanup ON players;
DROP FUNCTION IF EXISTS enqueue_player_avatar_cleanup_for_deleted_player();
DROP TABLE IF EXISTS player_avatars;
DROP TABLE IF EXISTS player_avatar_objects;
DROP TABLE IF EXISTS player_avatar_state;

-- +goose StatementEnd
