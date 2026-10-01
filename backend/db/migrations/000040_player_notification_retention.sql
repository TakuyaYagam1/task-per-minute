-- +goose Up
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

-- Existing rows satisfy the legacy branch, so validation would only scan the
-- table while the migration holds its DDL lock. New writes are checked at once.
ALTER TABLE public.player_notifications
    DROP CONSTRAINT player_notifications_timestamps_check,
    ADD CONSTRAINT player_notifications_timestamps_check CHECK (
        expires_at = created_at + interval '30 minutes'
        OR expires_at = created_at + interval '24 hours'
    ) NOT VALID;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.player_notifications IN ACCESS EXCLUSIVE MODE;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM public.player_notifications
        WHERE expires_at = created_at + interval '24 hours'
    ) THEN
        RAISE EXCEPTION 'cannot roll back player notification retention while 24-hour notifications exist';
    END IF;
END;
$$;

ALTER TABLE public.player_notifications
    DROP CONSTRAINT player_notifications_timestamps_check,
    ADD CONSTRAINT player_notifications_timestamps_check CHECK (
        expires_at = created_at + interval '30 minutes'
    );

-- +goose StatementEnd
