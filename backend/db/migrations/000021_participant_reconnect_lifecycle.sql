-- +goose Up
-- +goose StatementBegin

-- A lease is one durable participant connection.  Multiple active leases are
-- intentional: one participant may have more than one tab connected at once.
CREATE TABLE public.participant_connection_leases (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    player_id uuid NOT NULL,
    connection_id uuid NOT NULL,
    connection_generation bigint NOT NULL,
    assignment_id uuid,
    series_id uuid,
    game_attempt_id uuid,
    state character varying(16) DEFAULT 'active'::character varying NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    connected_at timestamp with time zone NOT NULL,
    disconnected_at timestamp with time zone,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT participant_connection_leases_pkey PRIMARY KEY (id),
    CONSTRAINT participant_connection_leases_fence_key UNIQUE (connection_id, connection_generation),
    CONSTRAINT participant_connection_leases_uuid_check CHECK (
        id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND tournament_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND roster_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND participant_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND player_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND connection_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND (assignment_id IS NULL OR assignment_id <> '00000000-0000-0000-0000-000000000000'::uuid)
        AND (series_id IS NULL OR series_id <> '00000000-0000-0000-0000-000000000000'::uuid)
        AND (game_attempt_id IS NULL OR game_attempt_id <> '00000000-0000-0000-0000-000000000000'::uuid)
    ),
    CONSTRAINT participant_connection_leases_binding_check CHECK (
        (assignment_id IS NULL AND series_id IS NULL AND game_attempt_id IS NULL)
        OR (assignment_id IS NOT NULL AND series_id IS NOT NULL AND game_attempt_id IS NOT NULL)
    ),
    CONSTRAINT participant_connection_leases_generation_check CHECK (connection_generation >= 1),
    CONSTRAINT participant_connection_leases_revision_check CHECK (revision >= 1),
    CONSTRAINT participant_connection_leases_state_check CHECK (
        state IN ('active'::character varying, 'disconnected'::character varying)
    ),
    CONSTRAINT participant_connection_leases_state_timestamps_check CHECK (
        (state = 'active'::character varying AND disconnected_at IS NULL)
        OR (state = 'disconnected'::character varying AND disconnected_at IS NOT NULL)
    ),
    CONSTRAINT participant_connection_leases_timestamps_check CHECK (
        connected_at <= updated_at
        AND (disconnected_at IS NULL OR connected_at <= disconnected_at)
        AND (disconnected_at IS NULL OR updated_at >= disconnected_at)
    ),
    CONSTRAINT participant_connection_leases_tournament_fk
        FOREIGN KEY (tournament_id) REFERENCES public.tournaments(id) ON DELETE RESTRICT,
    CONSTRAINT participant_connection_leases_roster_fk
        FOREIGN KEY (roster_id, tournament_id)
        REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT,
    CONSTRAINT participant_connection_leases_participant_fk
        FOREIGN KEY (roster_id, participant_id)
        REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT,
    CONSTRAINT participant_connection_leases_player_fk
        FOREIGN KEY (player_id) REFERENCES public.players(id) ON DELETE RESTRICT,
    CONSTRAINT participant_connection_leases_assignment_fk
        FOREIGN KEY (assignment_id, game_attempt_id, roster_id)
        REFERENCES public.assignments(id, attempt_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT participant_connection_leases_series_fk
        FOREIGN KEY (series_id, tournament_id, roster_id)
        REFERENCES public.series(id, tournament_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT participant_connection_leases_attempt_fk
        FOREIGN KEY (game_attempt_id, series_id, roster_id)
        REFERENCES public.game_attempts(id, series_id, roster_id) ON DELETE RESTRICT
);

-- This partial unique fence rejects a duplicate active lease while leaving
-- distinct tabs countable by the non-unique active-participant index below.
CREATE UNIQUE INDEX participant_connection_leases_current_fence_key
    ON public.participant_connection_leases (
        tournament_id, roster_id, participant_id, connection_id, connection_generation
    )
    WHERE state = 'active'::character varying;

CREATE INDEX participant_connection_leases_active_participant_idx
    ON public.participant_connection_leases (tournament_id, participant_id)
    WHERE state = 'active'::character varying;

CREATE INDEX participant_connection_leases_participant_lookup_idx
    ON public.participant_connection_leases (
        tournament_id, roster_id, participant_id, connected_at DESC, id
    );

CREATE FUNCTION public.participant_connection_lease_guard() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NOT EXISTS (
            SELECT 1
            FROM public.participants AS participant
            WHERE participant.id = NEW.participant_id
                AND participant.roster_id = NEW.roster_id
                AND participant.player_id = NEW.player_id
        ) THEN
            RAISE EXCEPTION 'Participant connection lease identity does not match its player'
                USING ERRCODE = 'foreign_key_violation';
        END IF;
        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Participant connection leases are durable lifecycle evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.participant_id IS DISTINCT FROM OLD.participant_id
        OR NEW.player_id IS DISTINCT FROM OLD.player_id
        OR NEW.connection_id IS DISTINCT FROM OLD.connection_id
        OR NEW.connection_generation IS DISTINCT FROM OLD.connection_generation
        OR NEW.assignment_id IS DISTINCT FROM OLD.assignment_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.game_attempt_id IS DISTINCT FROM OLD.game_attempt_id
        OR NEW.connected_at IS DISTINCT FROM OLD.connected_at
        OR OLD.state <> 'active'::character varying
        OR NEW.state <> 'disconnected'::character varying
        OR NEW.disconnected_at IS NULL
        OR NEW.revision <> OLD.revision + 1
        OR NEW.updated_at <= OLD.updated_at THEN
        RAISE EXCEPTION 'Invalid participant connection lease CAS transition'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER participant_connection_leases_guard
    BEFORE INSERT OR DELETE OR UPDATE ON public.participant_connection_leases
    FOR EACH ROW EXECUTE FUNCTION public.participant_connection_lease_guard();

-- The exact connection fence is the only authority for disconnect cleanup.
-- A stale generation or a repeated cleanup updates no row and returns false.
CREATE FUNCTION public.disconnect_participant_connection_lease(
    p_tournament_id uuid,
    p_roster_id uuid,
    p_participant_id uuid,
    p_connection_id uuid,
    p_connection_generation bigint,
    p_disconnected_at timestamp with time zone
) RETURNS boolean
LANGUAGE plpgsql
AS $$
BEGIN
    IF p_tournament_id = '00000000-0000-0000-0000-000000000000'::uuid
        OR p_roster_id = '00000000-0000-0000-0000-000000000000'::uuid
        OR p_participant_id = '00000000-0000-0000-0000-000000000000'::uuid
        OR p_connection_id = '00000000-0000-0000-0000-000000000000'::uuid
        OR p_connection_generation < 1
        OR p_disconnected_at IS NULL THEN
        RAISE EXCEPTION 'Invalid participant connection lease disconnect fence'
            USING ERRCODE = 'check_violation';
    END IF;

    UPDATE public.participant_connection_leases
    SET state = 'disconnected'::character varying,
        disconnected_at = p_disconnected_at,
        updated_at = p_disconnected_at,
        revision = revision + 1
    WHERE tournament_id = p_tournament_id
        AND roster_id = p_roster_id
        AND participant_id = p_participant_id
        AND connection_id = p_connection_id
        AND connection_generation = p_connection_generation
        AND state = 'active'::character varying;

    RETURN FOUND;
END;
$$;

-- Receipts retain the complete ReconnectRecord as one versioned document.
-- interval_id remains nullable because terminal disconnect can have no new
-- reconnect interval when the participant has exhausted its limit.
CREATE TABLE public.reconnect_command_receipts (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    wave_id uuid NOT NULL,
    mutation_kind character varying(16) NOT NULL,
    participant_id uuid NOT NULL,
    interval_id uuid,
    expected_authority_revision bigint NOT NULL,
    result_authority_revision bigint NOT NULL,
    schema_version smallint DEFAULT 1 NOT NULL,
    record_document jsonb NOT NULL,
    recorded_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT reconnect_command_receipts_pkey PRIMARY KEY (command_id),
    CONSTRAINT reconnect_command_receipts_uuid_check CHECK (
        command_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND tournament_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND roster_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND wave_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND participant_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND (interval_id IS NULL OR interval_id <> '00000000-0000-0000-0000-000000000000'::uuid)
    ),
    CONSTRAINT reconnect_command_receipts_mutation_check CHECK (
        mutation_kind = btrim(mutation_kind)
        AND mutation_kind IN (
            'reconnect'::character varying,
            'timeout'::character varying,
            'disconnect'::character varying
        )
    ),
    CONSTRAINT reconnect_command_receipts_revision_check CHECK (
        expected_authority_revision >= 1
        AND result_authority_revision >= 1
        AND result_authority_revision >= expected_authority_revision
    ),
    CONSTRAINT reconnect_command_receipts_schema_version_check CHECK (schema_version = 1),
    CONSTRAINT reconnect_command_receipts_document_check CHECK (
        jsonb_typeof(record_document) = 'object'::text
        AND record_document <> '{}'::jsonb
    ),
    CONSTRAINT reconnect_command_receipts_timestamps_check CHECK (
        recorded_at <= created_at
    ),
    CONSTRAINT reconnect_command_receipts_tournament_fk
        FOREIGN KEY (tournament_id) REFERENCES public.tournaments(id) ON DELETE RESTRICT,
    CONSTRAINT reconnect_command_receipts_roster_fk
        FOREIGN KEY (roster_id, tournament_id)
        REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT,
    CONSTRAINT reconnect_command_receipts_wave_fk
        FOREIGN KEY (wave_id, tournament_id, roster_id)
        REFERENCES public.waves(id, tournament_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT reconnect_command_receipts_participant_fk
        FOREIGN KEY (roster_id, participant_id)
        REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT,
    CONSTRAINT reconnect_command_receipts_interval_fk
        FOREIGN KEY (interval_id) REFERENCES public.reconnect_intervals(id) ON DELETE RESTRICT
);

CREATE INDEX reconnect_command_receipts_participant_idx
    ON public.reconnect_command_receipts (
        tournament_id, roster_id, participant_id, created_at DESC, command_id
    );

CREATE INDEX reconnect_command_receipts_wave_idx
    ON public.reconnect_command_receipts (
        tournament_id, roster_id, wave_id, created_at DESC, command_id
    );

CREATE FUNCTION public.reconnect_command_receipt_guard() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        RETURN NEW;
    END IF;

    RAISE EXCEPTION 'Reconnect command receipts are immutable replay evidence'
        USING ERRCODE = 'check_violation';
END;
$$;

CREATE TRIGGER reconnect_command_receipts_guard
    BEFORE INSERT OR DELETE OR UPDATE ON public.reconnect_command_receipts
    FOR EACH ROW EXECUTE FUNCTION public.reconnect_command_receipt_guard();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE public.reconnect_command_receipts;
DROP FUNCTION public.reconnect_command_receipt_guard();
DROP FUNCTION public.disconnect_participant_connection_lease(
    uuid, uuid, uuid, uuid, bigint, timestamp with time zone
);
DROP TABLE public.participant_connection_leases;
DROP FUNCTION public.participant_connection_lease_guard();

-- +goose StatementEnd
