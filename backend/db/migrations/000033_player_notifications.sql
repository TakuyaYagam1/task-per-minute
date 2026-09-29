-- +goose Up
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

CREATE TABLE public.player_notifications (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    player_id uuid NOT NULL,
    notification_type character varying(64) NOT NULL,
    tournament_id uuid NOT NULL,
    tournament_name character varying(120) NOT NULL,
    created_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    CONSTRAINT player_notifications_pkey PRIMARY KEY (id),
    CONSTRAINT player_notifications_player_fk FOREIGN KEY (player_id)
        REFERENCES public.players(id) ON DELETE CASCADE,
    CONSTRAINT player_notifications_type_check CHECK (
        notification_type = 'tournament_player_removed'
    ),
    CONSTRAINT player_notifications_tournament_name_check CHECK (
        tournament_name = btrim(tournament_name)
        AND tournament_name <> ''
        AND char_length(tournament_name) <= 120
    ),
    CONSTRAINT player_notifications_timestamps_check CHECK (
        expires_at = created_at + interval '30 minutes'
    )
);

CREATE INDEX player_notifications_player_expiry_created_idx
    ON public.player_notifications (player_id, created_at DESC, id DESC);

CREATE INDEX player_notifications_expiry_idx
    ON public.player_notifications (expires_at, id);

CREATE FUNCTION public.notify_player_notification_changed() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    PERFORM pg_notify('player_notifications_changed', NEW.player_id::text);
    RETURN NEW;
END;
$$;

CREATE TRIGGER player_notifications_changed_notify
    AFTER INSERT ON public.player_notifications
    FOR EACH ROW EXECUTE FUNCTION public.notify_player_notification_changed();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

DROP TRIGGER IF EXISTS player_notifications_changed_notify ON public.player_notifications;
DROP FUNCTION IF EXISTS public.notify_player_notification_changed();
DROP INDEX IF EXISTS public.player_notifications_expiry_idx;
DROP INDEX IF EXISTS public.player_notifications_player_expiry_created_idx;
DROP TABLE IF EXISTS public.player_notifications;

-- +goose StatementEnd
