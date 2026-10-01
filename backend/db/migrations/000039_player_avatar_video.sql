-- +goose Up
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

ALTER TABLE player_avatar_objects
    DROP CONSTRAINT player_avatar_objects_key_check,
    ADD CONSTRAINT player_avatar_objects_key_check CHECK (
        object_key ~ '^avatars/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}[.](jpg|png|gif|mp4)$'
    );

ALTER TABLE player_avatars
    DROP CONSTRAINT player_avatars_content_type_check,
    ADD CONSTRAINT player_avatars_content_type_check CHECK (
        content_type IN ('image/jpeg', 'image/png', 'image/gif', 'video/mp4')
    );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM player_avatar_objects
        WHERE object_key ~ '[.]mp4$'
    ) OR EXISTS (
        SELECT 1
        FROM player_avatars
        WHERE content_type = 'video/mp4'
    ) THEN
        RAISE EXCEPTION 'cannot remove MP4 avatar constraints while MP4 objects or cleanup work remain';
    END IF;
END;
$$;

ALTER TABLE player_avatar_objects
    DROP CONSTRAINT player_avatar_objects_key_check,
    ADD CONSTRAINT player_avatar_objects_key_check CHECK (
        object_key ~ '^avatars/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}[.](jpg|png|gif)$'
    );

ALTER TABLE player_avatars
    DROP CONSTRAINT player_avatars_content_type_check,
    ADD CONSTRAINT player_avatars_content_type_check CHECK (
        content_type IN ('image/jpeg', 'image/png', 'image/gif')
    );

-- +goose StatementEnd
