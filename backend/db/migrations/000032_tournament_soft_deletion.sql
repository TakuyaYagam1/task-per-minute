-- +goose Up
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

ALTER TABLE public.tournaments
    ADD COLUMN deleted_at timestamp with time zone;

CREATE TABLE public.tournament_deletions (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    source_revision bigint NOT NULL,
    resulting_revision bigint NOT NULL,
    source_state character varying(32) NOT NULL,
    cancelled boolean NOT NULL,
    reason text NOT NULL,
    deleted_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tournament_deletions_pkey PRIMARY KEY (command_id),
    CONSTRAINT tournament_deletions_tournament_command_key UNIQUE (tournament_id, command_id),
    CONSTRAINT tournament_deletions_command_check CHECK (command_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CONSTRAINT tournament_deletions_tournament_check CHECK (tournament_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CONSTRAINT tournament_deletions_actor_check CHECK (actor_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CONSTRAINT tournament_deletions_revision_check CHECK (source_revision >= 1 AND resulting_revision >= source_revision + 1),
    CONSTRAINT tournament_deletions_state_check CHECK ((source_state)::text = ANY ((ARRAY['draft'::character varying, 'registration'::character varying, 'roster_locked'::character varying, 'swiss'::character varying, 'golden'::character varying, 'playoffs'::character varying, 'technical_pause'::character varying, 'completed'::character varying, 'cancelled'::character varying])::text[])),
    CONSTRAINT tournament_deletions_reason_check CHECK (reason = btrim(reason) AND reason <> ''::text AND char_length(reason) <= 512),
    CONSTRAINT tournament_deletions_timestamps_check CHECK (deleted_at <= created_at),
    CONSTRAINT tournament_deletions_tournament_fk FOREIGN KEY (tournament_id) REFERENCES public.tournaments(id) ON DELETE RESTRICT
);

CREATE INDEX tournament_deletions_tournament_idx
    ON public.tournament_deletions (tournament_id, deleted_at, command_id);

CREATE TRIGGER tournament_deletions_append_only
    BEFORE DELETE OR UPDATE ON public.tournament_deletions
    FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

DROP TABLE IF EXISTS public.tournament_deletions;
ALTER TABLE public.tournaments
    DROP COLUMN IF EXISTS deleted_at;

-- +goose StatementEnd
