-- +goose Up
-- +goose StatementBegin

-- Initial game domain schema.
SET LOCAL check_function_bodies = false;

-- Name: game_attempt_identity_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.game_attempt_identity_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Game attempts are immutable history'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.slot_id IS DISTINCT FROM OLD.slot_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.attempt_number IS DISTINCT FROM OLD.attempt_number
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Game attempt identity is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: game_attempt_insert_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.game_attempt_insert_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    previous_attempt_number INTEGER;
    previous_state VARCHAR(32);
BEGIN
    PERFORM 1
    FROM game_slots
    WHERE id = NEW.slot_id
    FOR UPDATE;

    SELECT attempt_number, state
    INTO previous_attempt_number, previous_state
    FROM game_attempts
    WHERE slot_id = NEW.slot_id
    ORDER BY attempt_number DESC
    LIMIT 1;

    IF previous_attempt_number IS NULL THEN
        IF NEW.attempt_number <> 1 THEN
            RAISE EXCEPTION 'first Game attempt number must be 1'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.attempt_number <> previous_attempt_number + 1 OR previous_state <> 'void' THEN
        RAISE EXCEPTION 'Game attempts must form a contiguous void-replay chain'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: game_slot_identity_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.game_slot_identity_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Game slots are stable Series history'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.slot_number IS DISTINCT FROM OLD.slot_number
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Game slot identity and position are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: ready_window_lifecycle_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ready_window_lifecycle_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    member_count INTEGER;
    ready_count INTEGER;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'ready windows are retained execution history'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.wave_id IS DISTINCT FROM OLD.wave_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.revision_id IS DISTINCT FROM OLD.revision_id
        OR NEW.opened_at IS DISTINCT FROM OLD.opened_at
        OR NEW.deadline IS DISTINCT FROM OLD.deadline
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'ready-window identity is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state IS DISTINCT FROM OLD.state
        AND NOT (
            (OLD.state = 'open' AND NEW.state IN ('consumed', 'expired', 'superseded'))
            OR (OLD.state IN ('consumed', 'expired') AND NEW.state = 'superseded')
        ) THEN
        RAISE EXCEPTION 'invalid ready-window transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'consumed' AND OLD.state <> 'consumed' THEN
        SELECT COUNT(*) INTO member_count
        FROM wave_members
        WHERE wave_id = NEW.wave_id;

        SELECT COUNT(*) INTO ready_count
        FROM wave_readiness
        WHERE ready_window_id = NEW.id
            AND ready;

        IF member_count < 2 OR ready_count <> member_count THEN
            RAISE EXCEPTION 'all Wave members must be ready before start'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: validate_wave_window(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.validate_wave_window() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    target_wave_id UUID;
    wave_tournament_id UUID;
    wave_roster_id UUID;
    wave_replaces_id UUID;
    wave_state VARCHAR(32);
    wave_started_at TIMESTAMPTZ;
    window_state VARCHAR(16);
    window_consumed_at TIMESTAMPTZ;
    member_count INTEGER;
    replaced_tournament_id UUID;
    replaced_roster_id UUID;
    replaced_state VARCHAR(32);
BEGIN
    IF TG_TABLE_NAME = 'waves' THEN
        target_wave_id := NEW.id;
    ELSE
        target_wave_id := NEW.wave_id;
    END IF;

    SELECT
        wave.tournament_id,
        wave.roster_id,
        wave.replaces_wave_id,
        wave.state,
        wave.started_at,
        ready_window.state,
        ready_window.consumed_at
    INTO
        wave_tournament_id,
        wave_roster_id,
        wave_replaces_id,
        wave_state,
        wave_started_at,
        window_state,
        window_consumed_at
    FROM waves AS wave
    LEFT JOIN ready_windows AS ready_window ON ready_window.wave_id = wave.id
    WHERE wave.id = target_wave_id;

    IF NOT FOUND THEN
        RETURN NULL;
    END IF;

    SELECT COUNT(*) INTO member_count
    FROM wave_members
    WHERE wave_id = target_wave_id;

    IF wave_state = 'planned' THEN
        IF window_state IS NOT NULL THEN
            RAISE EXCEPTION 'planned Wave cannot have a ready window'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF wave_state IN ('ready_window_open', 'ready') THEN
        IF window_state <> 'open' OR member_count < 2 THEN
            RAISE EXCEPTION 'open Wave requires members and an open ready window'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF wave_state IN ('active', 'paused', 'completed') THEN
        IF window_state <> 'consumed'
            OR window_consumed_at IS DISTINCT FROM wave_started_at
            OR member_count < 2 THEN
            RAISE EXCEPTION 'started Wave requires one shared consumed ready window'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF wave_state = 'ready_window_expired' THEN
        IF window_state <> 'expired' OR member_count < 2 THEN
            RAISE EXCEPTION 'expired Wave requires its expired ready window'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF wave_state = 'superseded' THEN
        IF window_state <> 'superseded' OR member_count < 2 THEN
            RAISE EXCEPTION 'superseded Wave requires its superseded ready window'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF wave_replaces_id IS NOT NULL THEN
        SELECT tournament_id, roster_id, state
        INTO replaced_tournament_id, replaced_roster_id, replaced_state
        FROM waves
        WHERE id = wave_replaces_id;

        IF replaced_state <> 'superseded'
            OR replaced_tournament_id IS DISTINCT FROM wave_tournament_id
            OR replaced_roster_id IS DISTINCT FROM wave_roster_id THEN
            RAISE EXCEPTION 'Wave replacement must continue one superseded lineage'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NULL;
END;
$$;

--
-- Name: wave_identity_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.wave_identity_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Waves are retained execution history'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.replaces_wave_id IS DISTINCT FROM OLD.replaces_wave_id
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Wave identity and lineage are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.revision_id IS DISTINCT FROM OLD.revision_id
        AND NOT (
            OLD.state = 'active'
            AND NEW.state = 'completed'
            AND NEW.revision = OLD.revision + 1
            AND NEW.closed_at IS NOT NULL
        ) THEN
        RAISE EXCEPTION 'Wave revision identity changes only at closure'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.state = 'active'
        AND NEW.state = 'completed'
        AND NEW.revision_id IS NOT DISTINCT FROM OLD.revision_id THEN
        RAISE EXCEPTION 'Wave closure requires a fresh revision identity'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.started_at IS NOT NULL AND NEW.started_at IS DISTINCT FROM OLD.started_at THEN
        RAISE EXCEPTION 'Wave start is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.closed_at IS NOT NULL AND NEW.closed_at IS DISTINCT FROM OLD.closed_at THEN
        RAISE EXCEPTION 'Wave closure is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: wave_readiness_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.wave_readiness_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    window_state VARCHAR(16);
    window_opened_at TIMESTAMPTZ;
    window_deadline TIMESTAMPTZ;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Wave readiness heads are retained execution state'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_OP = 'INSERT' THEN
        IF NEW.revision <> 1
            OR NEW.ready_window_id IS NOT NULL
            OR NEW.ready
            OR NEW.ready_at IS NOT NULL THEN
            RAISE EXCEPTION 'Wave readiness head must start unbound at revision 1'
                USING ERRCODE = 'check_violation';
        END IF;

        RETURN NEW;
    END IF;

    IF NEW.wave_id IS DISTINCT FROM OLD.wave_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.participant_id IS DISTINCT FROM OLD.participant_id
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Wave readiness head identity is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.revision <> OLD.revision + 1 THEN
        RAISE EXCEPTION 'Wave readiness revision must advance by one'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.ready_window_id IS NOT NULL
        AND NEW.ready_window_id IS DISTINCT FROM OLD.ready_window_id THEN
        RAISE EXCEPTION 'Wave readiness window binding is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.ready_window_id IS NULL THEN
        RETURN NEW;
    END IF;

    SELECT state, opened_at, deadline
    INTO window_state, window_opened_at, window_deadline
    FROM ready_windows
    WHERE id = NEW.ready_window_id
    FOR UPDATE;

    IF window_state <> 'open' THEN
        RAISE EXCEPTION 'Wave readiness update requires an open window'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.ready
        AND (NEW.ready_at < window_opened_at OR NEW.ready_at > window_deadline) THEN
        RAISE EXCEPTION 'Wave readiness is outside the current open window'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: readiness_event_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.readiness_event_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Readiness events are append-only evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: execution_authority_lease_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.execution_authority_lease_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    predecessor RECORD;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Execution authority leases are append-only evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.revision = 1 THEN
        RETURN NEW;
    END IF;

    SELECT
        holder_id,
        lease_id,
        epoch,
        acquired_at,
        renewed_at,
        expires_at
    INTO predecessor
    FROM execution_authority_leases
    WHERE tournament_id = NEW.tournament_id
        AND revision = NEW.previous_revision
    FOR KEY SHARE;

    IF NOT FOUND
        OR NEW.previous_revision <> NEW.revision - 1
        OR NEW.previous_lease_id IS DISTINCT FROM predecessor.lease_id
        OR NEW.previous_epoch IS DISTINCT FROM predecessor.epoch THEN
        RAISE EXCEPTION 'Execution authority predecessor does not match its lineage'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.lease_id = predecessor.lease_id THEN
        IF NEW.epoch <> predecessor.epoch
            OR NEW.holder_id <> predecessor.holder_id
            OR NEW.acquired_at <> predecessor.acquired_at
            OR NEW.renewed_at < predecessor.renewed_at
            OR NEW.renewed_at >= predecessor.expires_at
            OR NEW.created_at >= predecessor.expires_at THEN
            RAISE EXCEPTION 'Execution authority renewal breaks lease continuity'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.epoch <> predecessor.epoch + 1
        OR NEW.acquired_at <> NEW.renewed_at
        OR NEW.renewed_at < predecessor.expires_at THEN
        RAISE EXCEPTION 'Execution authority takeover breaks epoch continuity'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

-- Execution epoch bindings and replay receipts are append-only authority
-- evidence. A replay must never be rewritten to make a reused command appear
-- valid after a later takeover.
CREATE FUNCTION public.execution_epoch_evidence_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        RETURN NEW;
    END IF;

    RAISE EXCEPTION 'Execution epoch evidence is immutable'
        USING ERRCODE = 'check_violation';
END;
$$;

--
-- Name: game_attempts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.game_attempts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    slot_id uuid NOT NULL,
    series_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    attempt_number integer NOT NULL,
    state character varying(32) DEFAULT 'planned'::character varying NOT NULL,
    result_reason character varying(40),
    winner_id uuid,
    result_revision_id uuid,
    revision bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    started_at timestamp with time zone,
    finished_at timestamp with time zone,
    CONSTRAINT game_attempts_number_check CHECK ((attempt_number >= 1)),
    CONSTRAINT game_attempts_result_check CHECK (((((state)::text = ANY ((ARRAY['planned'::character varying, 'ready'::character varying, 'active'::character varying, 'paused'::character varying])::text[])) AND (result_reason IS NULL) AND (winner_id IS NULL) AND (result_revision_id IS NULL)) OR (((state)::text = 'completed'::text) AND (result_reason IS NOT NULL) AND ((result_reason)::text = ANY ((ARRAY['solved'::character varying, 'surrender'::character varying, 'operator_forfeit'::character varying])::text[])) AND (winner_id IS NOT NULL) AND (result_revision_id IS NOT NULL)) OR (((state)::text = 'void'::text) AND (result_reason IS NOT NULL) AND ((result_reason)::text = ANY ((ARRAY['no_solve'::character varying, 'task_failure'::character varying, 'common_platform_failure'::character varying, 'disconnect'::character varying, 'execution_epoch_break'::character varying])::text[])) AND (winner_id IS NULL) AND (result_revision_id IS NOT NULL)) OR (((state)::text = 'cancelled'::text) AND (result_reason IS NOT NULL) AND ((result_reason)::text = ANY ((ARRAY['no_show'::character varying, 'series_cancelled'::character varying, 'tournament_cancelled'::character varying])::text[])) AND (winner_id IS NULL) AND (result_revision_id IS NOT NULL)) OR (((state)::text = 'superseded'::text) AND (result_reason IS NOT NULL) AND ((result_reason)::text = 'derived_revision_superseded'::text) AND (winner_id IS NULL) AND (result_revision_id IS NOT NULL)))),
    CONSTRAINT game_attempts_revision_check CHECK ((revision >= 1)),
    CONSTRAINT game_attempts_state_check CHECK (((state)::text = ANY ((ARRAY['planned'::character varying, 'ready'::character varying, 'active'::character varying, 'paused'::character varying, 'completed'::character varying, 'void'::character varying, 'cancelled'::character varying, 'superseded'::character varying])::text[]))),
    CONSTRAINT game_attempts_timestamps_check CHECK (((updated_at >= created_at) AND ((started_at IS NULL) OR (started_at >= created_at)) AND ((finished_at IS NULL) OR (finished_at >= COALESCE(started_at, created_at))) AND ((((state)::text = ANY ((ARRAY['completed'::character varying, 'void'::character varying, 'cancelled'::character varying, 'superseded'::character varying])::text[])) AND (finished_at IS NOT NULL)) OR (((state)::text <> ALL ((ARRAY['completed'::character varying, 'void'::character varying, 'cancelled'::character varying, 'superseded'::character varying])::text[])) AND (finished_at IS NULL)))))
);

--
-- Name: game_slots; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.game_slots (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    series_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    slot_number smallint NOT NULL,
    category character varying(32) NOT NULL,
    first_participant_wins_before smallint DEFAULT 0 NOT NULL,
    second_participant_wins_before smallint DEFAULT 0 NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT game_slots_category_check CHECK (((category)::text = ANY ((ARRAY['web'::character varying, 'crypto'::character varying, 'forensics'::character varying, 'reverse'::character varying, 'pwn'::character varying, 'steganography'::character varying, 'ppc'::character varying, 'osint'::character varying, 'mobile'::character varying, 'hardware'::character varying, 'misc'::character varying])::text[]))),
    CONSTRAINT game_slots_position_check CHECK (((slot_number >= 1) AND (slot_number <= 3))),
    CONSTRAINT game_slots_revision_check CHECK ((revision >= 1)),
    CONSTRAINT game_slots_score_check CHECK ((((first_participant_wins_before >= 0) AND (first_participant_wins_before <= 2)) AND ((second_participant_wins_before >= 0) AND (second_participant_wins_before <= 2)) AND (NOT ((first_participant_wins_before = 2) AND (second_participant_wins_before = 2))))),
    CONSTRAINT game_slots_timestamps_check CHECK ((updated_at >= created_at))
);

--
-- Name: ready_windows; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.ready_windows (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    wave_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    revision_id uuid NOT NULL,
    state character varying(16) DEFAULT 'open'::character varying NOT NULL,
    opened_at timestamp with time zone NOT NULL,
    deadline timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT ready_windows_consumption_check CHECK (((((state)::text = 'consumed'::text) AND (consumed_at IS NOT NULL) AND ((consumed_at >= opened_at) AND (consumed_at <= deadline))) OR (((state)::text <> 'consumed'::text) AND (consumed_at IS NULL)))),
    CONSTRAINT ready_windows_interval_check CHECK (((deadline > opened_at) AND (created_at >= opened_at))),
    CONSTRAINT ready_windows_state_check CHECK (((state)::text = ANY ((ARRAY['open'::character varying, 'consumed'::character varying, 'expired'::character varying, 'superseded'::character varying])::text[])))
);

--
-- Name: series; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.series (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    first_participant_id uuid NOT NULL,
    second_participant_id uuid NOT NULL,
    format character varying(8) NOT NULL,
    state character varying(32) DEFAULT 'planned'::character varying NOT NULL,
    first_participant_wins smallint DEFAULT 0 NOT NULL,
    second_participant_wins smallint DEFAULT 0 NOT NULL,
    winner_id uuid,
    current_score_revision_id uuid,
    current_result_revision_id uuid,
    revision bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    started_at timestamp with time zone,
    finished_at timestamp with time zone,
    CONSTRAINT series_format_check CHECK (((format)::text = ANY ((ARRAY['bo1'::character varying, 'bo3'::character varying])::text[]))),
    CONSTRAINT series_participants_check CHECK (((first_participant_id <> second_participant_id) AND ((winner_id IS NULL) OR ((winner_id = first_participant_id) OR (winner_id = second_participant_id))))),
    CONSTRAINT series_result_check CHECK (((((state)::text = 'completed'::text) AND (winner_id IS NOT NULL) AND (current_score_revision_id IS NOT NULL) AND (current_result_revision_id IS NOT NULL) AND (((winner_id = first_participant_id) AND ((((format)::text = 'bo1'::text) AND (first_participant_wins = 1)) OR (((format)::text = 'bo3'::text) AND (first_participant_wins = 2)))) OR ((winner_id = second_participant_id) AND ((((format)::text = 'bo1'::text) AND (second_participant_wins = 1)) OR (((format)::text = 'bo3'::text) AND (second_participant_wins = 2)))))) OR (((state)::text = 'cancelled'::text) AND (current_score_revision_id IS NOT NULL) AND (current_result_revision_id IS NOT NULL)) OR (((state)::text <> ALL ((ARRAY['completed'::character varying, 'cancelled'::character varying])::text[])) AND (winner_id IS NULL) AND (current_result_revision_id IS NULL)))),
    CONSTRAINT series_revision_check CHECK ((revision >= 1)),
    CONSTRAINT series_score_check CHECK (((first_participant_wins >= 0) AND (second_participant_wins >= 0) AND ((((format)::text = 'bo1'::text) AND (first_participant_wins <= 1) AND (second_participant_wins <= 1) AND (NOT ((first_participant_wins = 1) AND (second_participant_wins = 1)))) OR (((format)::text = 'bo3'::text) AND (first_participant_wins <= 2) AND (second_participant_wins <= 2) AND (NOT ((first_participant_wins = 2) AND (second_participant_wins = 2))))))),
    CONSTRAINT series_state_check CHECK (((state)::text = ANY ((ARRAY['planned'::character varying, 'locked'::character varying, 'draft'::character varying, 'ready'::character varying, 'active'::character varying, 'replay_required'::character varying, 'technical_pause'::character varying, 'completed'::character varying, 'cancelled'::character varying])::text[]))),
    CONSTRAINT series_timestamps_check CHECK (((updated_at >= created_at) AND ((started_at IS NULL) OR (started_at >= created_at)) AND ((finished_at IS NULL) OR (finished_at >= COALESCE(started_at, created_at))) AND ((((state)::text = ANY ((ARRAY['completed'::character varying, 'cancelled'::character varying])::text[])) AND (finished_at IS NOT NULL)) OR (((state)::text <> ALL ((ARRAY['completed'::character varying, 'cancelled'::character varying])::text[])) AND (finished_at IS NULL)))))
);

--
-- Name: wave_members; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.wave_members (
    wave_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

--
-- Name: wave_series; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.wave_series (
    wave_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    series_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

--
-- Name: wave_readiness; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.wave_readiness (
    ready_window_id uuid,
    wave_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    ready boolean DEFAULT false NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    ready_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT wave_readiness_revision_check CHECK ((revision >= 1)),
    CONSTRAINT wave_readiness_state_check CHECK (((ready AND ready_window_id IS NOT NULL AND ready_at IS NOT NULL) OR (NOT ready AND ready_at IS NULL))),
    CONSTRAINT wave_readiness_timestamps_check CHECK ((updated_at >= created_at))
);

--
-- Name: readiness_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.readiness_events (
    command_id uuid NOT NULL,
    wave_id uuid NOT NULL,
    ready_window_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    readiness_revision bigint NOT NULL,
    event_type character varying(16) NOT NULL,
    occurred_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT readiness_events_revision_check CHECK ((readiness_revision >= 1)),
    CONSTRAINT readiness_events_type_check CHECK (((event_type)::text = ANY ((ARRAY['ready'::character varying, 'cleared'::character varying])::text[]))),
    CONSTRAINT readiness_events_timestamps_check CHECK ((occurred_at <= created_at))
);

--
-- Name: execution_authority_leases; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.execution_authority_leases (
    tournament_id uuid NOT NULL,
    command_id uuid NOT NULL,
    holder_id uuid NOT NULL,
    lease_id uuid NOT NULL,
    epoch bigint NOT NULL,
    process_kind character varying(16) NOT NULL,
    revision bigint NOT NULL,
    previous_revision bigint,
    previous_lease_id uuid,
    previous_epoch bigint,
    acquired_at timestamp with time zone NOT NULL,
    renewed_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT execution_authority_leases_epoch_check CHECK ((epoch >= 1)),
    CONSTRAINT execution_authority_leases_process_check CHECK (((process_kind)::text = 'authority'::text)),
    CONSTRAINT execution_authority_leases_revision_check CHECK ((revision >= epoch)),
    CONSTRAINT execution_authority_leases_lineage_check CHECK (((revision = 1 AND epoch = 1 AND previous_revision IS NULL AND previous_lease_id IS NULL AND previous_epoch IS NULL) OR (revision > 1 AND previous_revision = revision - 1 AND previous_lease_id IS NOT NULL AND previous_epoch >= 1 AND ((previous_lease_id = lease_id AND previous_epoch = epoch) OR (previous_lease_id <> lease_id AND previous_epoch = epoch - 1))))),
    CONSTRAINT execution_authority_leases_timestamps_check CHECK ((acquired_at <= renewed_at AND renewed_at < expires_at AND renewed_at <= created_at))
);

-- Exact execution authority that activated a Game attempt. The revision is
-- retained as a durable fence in addition to the public lease stamp.
CREATE TABLE public.execution_game_epochs (
    game_attempt_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    wave_id uuid NOT NULL,
    series_id uuid NOT NULL,
    slot_id uuid NOT NULL,
    authority_holder_id uuid NOT NULL,
    authority_lease_id uuid NOT NULL,
    authority_epoch bigint NOT NULL,
    authority_revision bigint NOT NULL,
    bound_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT execution_game_epochs_epoch_check CHECK (authority_epoch >= 1),
    CONSTRAINT execution_game_epochs_revision_check CHECK (authority_revision >= authority_epoch),
    CONSTRAINT execution_game_epochs_timestamps_check CHECK (bound_at <= created_at)
);

-- A resumed Game keeps its activation epoch immutable and appends the exact
-- successor authority that fenced the paused-to-active transition.
CREATE TABLE public.execution_game_epoch_rebinds (
    game_attempt_id uuid NOT NULL,
    rebind_sequence bigint NOT NULL,
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    wave_id uuid NOT NULL,
    series_id uuid NOT NULL,
    slot_id uuid NOT NULL,
    previous_authority_holder_id uuid NOT NULL,
    previous_authority_lease_id uuid NOT NULL,
    previous_authority_epoch bigint NOT NULL,
    previous_authority_revision bigint NOT NULL,
    authority_holder_id uuid NOT NULL,
    authority_lease_id uuid NOT NULL,
    authority_epoch bigint NOT NULL,
    authority_revision bigint NOT NULL,
    rebound_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT execution_game_epoch_rebinds_sequence_check CHECK (rebind_sequence >= 1),
    CONSTRAINT execution_game_epoch_rebinds_previous_epoch_check CHECK (previous_authority_epoch >= 1),
    CONSTRAINT execution_game_epoch_rebinds_current_epoch_check CHECK (authority_epoch >= 1),
    CONSTRAINT execution_game_epoch_rebinds_previous_revision_check CHECK (
        previous_authority_revision >= previous_authority_epoch
    ),
    CONSTRAINT execution_game_epoch_rebinds_revision_check CHECK (
        authority_revision >= authority_epoch
        AND authority_revision >= previous_authority_revision
    ),
    CONSTRAINT execution_game_epoch_rebinds_same_revision_check CHECK (
        authority_revision <> previous_authority_revision
        OR (
            authority_holder_id = previous_authority_holder_id
            AND authority_lease_id = previous_authority_lease_id
            AND authority_epoch = previous_authority_epoch
        )
    ),
    CONSTRAINT execution_game_epoch_rebinds_timestamps_check CHECK (rebound_at <= created_at)
);

-- One immutable technical replay receipt belongs to the broken attempt. The
-- normalized identity columns reject command reuse while record_document keeps
-- the complete reconciled terminal record needed for exact retry recovery.
CREATE TABLE public.execution_epoch_replays (
    command_id uuid NOT NULL,
    game_attempt_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    wave_id uuid NOT NULL,
    series_id uuid NOT NULL,
    slot_id uuid NOT NULL,
    assignment_id uuid NOT NULL,
    assignment_attempt_id uuid NOT NULL,
    current_holder_id uuid NOT NULL,
    current_lease_id uuid NOT NULL,
    current_epoch bigint NOT NULL,
    expected_lease_revision bigint NOT NULL,
    broken_lease_id uuid NOT NULL,
    broken_epoch bigint NOT NULL,
    expected_attempt_revision bigint NOT NULL,
    command_digest bytea NOT NULL,
    record_document jsonb NOT NULL,
    replayed_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT execution_epoch_replays_current_epoch_check CHECK (current_epoch >= 1),
    CONSTRAINT execution_epoch_replays_broken_epoch_check CHECK (broken_epoch >= 1),
    CONSTRAINT execution_epoch_replays_revision_check CHECK (
        expected_lease_revision >= current_epoch AND expected_attempt_revision >= 1
    ),
    CONSTRAINT execution_epoch_replays_distinct_stamp_check CHECK (
        current_lease_id <> broken_lease_id OR current_epoch <> broken_epoch
    ),
    CONSTRAINT execution_epoch_replays_digest_check CHECK (octet_length(command_digest) = 32),
    CONSTRAINT execution_epoch_replays_document_check CHECK (
        jsonb_typeof(record_document) = 'object' AND record_document <> '{}'::jsonb
    ),
    CONSTRAINT execution_epoch_replays_timestamps_check CHECK (replayed_at <= created_at)
);

CREATE FUNCTION public.execution_epoch_rebind_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    previous_holder_id UUID;
    previous_lease_id UUID;
    previous_epoch BIGINT;
    previous_revision BIGINT;
    previous_sequence BIGINT;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Execution epoch rebind evidence is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT rebind.authority_holder_id,
        rebind.authority_lease_id,
        rebind.authority_epoch,
        rebind.authority_revision,
        rebind.rebind_sequence
    INTO previous_holder_id,
        previous_lease_id,
        previous_epoch,
        previous_revision,
        previous_sequence
    FROM execution_game_epoch_rebinds AS rebind
    WHERE rebind.game_attempt_id = NEW.game_attempt_id
    ORDER BY rebind.rebind_sequence DESC
    LIMIT 1;

    IF NOT FOUND THEN
        SELECT epoch.authority_holder_id,
            epoch.authority_lease_id,
            epoch.authority_epoch,
            epoch.authority_revision,
            0
        INTO previous_holder_id,
            previous_lease_id,
            previous_epoch,
            previous_revision,
            previous_sequence
        FROM execution_game_epochs AS epoch
        WHERE epoch.game_attempt_id = NEW.game_attempt_id
            AND epoch.tournament_id = NEW.tournament_id
            AND epoch.roster_id = NEW.roster_id
            AND epoch.wave_id = NEW.wave_id
            AND epoch.series_id = NEW.series_id
            AND epoch.slot_id = NEW.slot_id;
    END IF;

    IF previous_holder_id IS NULL
        OR NEW.rebind_sequence <> previous_sequence + 1
        OR NEW.previous_authority_holder_id <> previous_holder_id
        OR NEW.previous_authority_lease_id <> previous_lease_id
        OR NEW.previous_authority_epoch <> previous_epoch
        OR NEW.previous_authority_revision <> previous_revision THEN
        RAISE EXCEPTION 'Execution epoch rebind does not continue immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM execution_authority_leases AS previous_authority
        WHERE previous_authority.tournament_id = NEW.tournament_id
            AND previous_authority.revision = NEW.previous_authority_revision
            AND previous_authority.holder_id = NEW.previous_authority_holder_id
            AND previous_authority.lease_id = NEW.previous_authority_lease_id
            AND previous_authority.epoch = NEW.previous_authority_epoch
    ) OR NOT EXISTS (
        SELECT 1
        FROM execution_authority_leases AS current_authority
        WHERE current_authority.tournament_id = NEW.tournament_id
            AND current_authority.revision = NEW.authority_revision
            AND current_authority.holder_id = NEW.authority_holder_id
            AND current_authority.lease_id = NEW.authority_lease_id
            AND current_authority.epoch = NEW.authority_epoch
    ) THEN
        RAISE EXCEPTION 'Execution epoch rebind authority evidence is invalid'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: waves; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.waves (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    revision_id uuid NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    state character varying(32) DEFAULT 'planned'::character varying NOT NULL,
    replaces_wave_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    started_at timestamp with time zone,
    paused_at timestamp with time zone,
    closed_at timestamp with time zone,
    CONSTRAINT waves_replacement_check CHECK ((replaces_wave_id IS DISTINCT FROM id)),
    CONSTRAINT waves_revision_check CHECK ((revision >= 1)),
    CONSTRAINT waves_state_check CHECK (((state)::text = ANY ((ARRAY['planned'::character varying, 'ready_window_open'::character varying, 'ready'::character varying, 'active'::character varying, 'paused'::character varying, 'completed'::character varying, 'ready_window_expired'::character varying, 'superseded'::character varying])::text[]))),
    CONSTRAINT waves_state_evidence_check CHECK (((((state)::text = ANY ((ARRAY['planned'::character varying, 'ready_window_open'::character varying, 'ready'::character varying, 'ready_window_expired'::character varying])::text[])) AND (started_at IS NULL) AND (paused_at IS NULL) AND (closed_at IS NULL)) OR (((state)::text = 'active'::text) AND (started_at IS NOT NULL) AND (paused_at IS NULL) AND (closed_at IS NULL)) OR (((state)::text = 'paused'::text) AND (started_at IS NOT NULL) AND (paused_at IS NOT NULL) AND (closed_at IS NULL)) OR (((state)::text = 'completed'::text) AND (started_at IS NOT NULL) AND (paused_at IS NULL) AND (closed_at IS NOT NULL)) OR (((state)::text = 'superseded'::text) AND (paused_at IS NULL) AND (closed_at IS NOT NULL)))),
    CONSTRAINT waves_timestamps_check CHECK (((updated_at >= created_at) AND ((started_at IS NULL) OR (started_at >= created_at)) AND ((paused_at IS NULL) OR (paused_at >= started_at)) AND ((closed_at IS NULL) OR (closed_at >= COALESCE(started_at, created_at)))))
);

-- Stable Swiss round to execution Wave lineage, including an optional bye.
CREATE TABLE public.swiss_wave_links (
    wave_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    round_id uuid NOT NULL,
    bye_participant_id uuid,
    bye_revision_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT swiss_wave_links_bye_check CHECK (
        (bye_participant_id IS NULL) = (bye_revision_id IS NULL)
    )
);

ALTER TABLE ONLY public.swiss_wave_links
    ADD CONSTRAINT swiss_wave_links_pkey PRIMARY KEY (wave_id);

ALTER TABLE ONLY public.swiss_wave_links
    ADD CONSTRAINT swiss_wave_links_round_key UNIQUE (round_id);

ALTER TABLE ONLY public.swiss_wave_links
    ADD CONSTRAINT swiss_wave_links_round_fk
    FOREIGN KEY (round_id, roster_id)
    REFERENCES public.swiss_rounds(id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.swiss_wave_links
    ADD CONSTRAINT swiss_wave_links_bye_fk
    FOREIGN KEY (roster_id, bye_participant_id)
    REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

-- Idempotency and exact-source evidence for operator Wave controls.
CREATE TABLE public.wave_control_commands (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    wave_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    action character varying(24) NOT NULL,
    source_projection_revision_id uuid NOT NULL,
    source_projection_revision bigint NOT NULL,
    source_tournament_revision bigint NOT NULL,
    source_roster_revision bigint NOT NULL,
    source_wave_revision bigint NOT NULL,
    resulting_wave_revision bigint NOT NULL,
    source_revisions jsonb NOT NULL,
    source_graph jsonb NOT NULL,
    request_digest bytea NOT NULL,
    reason text,
    result_document jsonb NOT NULL,
    executed_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT wave_control_commands_action_check CHECK (
        action IN (
            'open_ready_window',
            'start',
            'pause',
            'resume',
            'complete',
            'cancel'
        )
    ),
    CONSTRAINT wave_control_commands_digest_check CHECK (octet_length(request_digest) = 32),
    CONSTRAINT wave_control_commands_evidence_check CHECK (
        jsonb_typeof(source_revisions) = 'object'
        AND jsonb_typeof(source_graph) = 'object'
        AND source_graph <> '{}'::jsonb
        AND jsonb_typeof(result_document) = 'object'
        AND result_document <> '{}'::jsonb
    ),
    CONSTRAINT wave_control_commands_reason_check CHECK (
        reason IS NULL OR (reason = btrim(reason) AND reason <> '')
    ),
    CONSTRAINT wave_control_commands_revision_check CHECK (
        source_projection_revision >= 1
        AND source_tournament_revision >= 1
        AND source_roster_revision >= 1
        AND source_wave_revision >= 1
        AND resulting_wave_revision = source_wave_revision + 1
    ),
    CONSTRAINT wave_control_commands_timestamps_check CHECK (executed_at <= created_at)
);

ALTER TABLE ONLY public.wave_control_commands
    ADD CONSTRAINT wave_control_commands_pkey PRIMARY KEY (command_id);

ALTER TABLE ONLY public.wave_control_commands
    ADD CONSTRAINT wave_control_commands_command_tournament_key
    UNIQUE (command_id, tournament_id);

-- The row may be written before command receipt inside the same Wave
-- transaction. Deferred validation requires that receipt to be the matching
-- durable resume command before the transaction can commit.
CREATE FUNCTION public.execution_epoch_rebind_command_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM wave_control_commands AS command
        WHERE command.command_id = NEW.command_id
            AND command.tournament_id = NEW.tournament_id
            AND command.action = 'resume'
    ) THEN
        RAISE EXCEPTION 'Execution epoch rebind requires its durable resume command'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;

--
-- Name: game_attempts game_attempts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_attempts
    ADD CONSTRAINT game_attempts_pkey PRIMARY KEY (id);

--
-- Name: game_attempts game_attempts_slot_number_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_attempts
    ADD CONSTRAINT game_attempts_slot_number_key UNIQUE (slot_id, attempt_number);

--
-- Name: game_slots game_slots_id_series_roster_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_slots
    ADD CONSTRAINT game_slots_id_series_roster_key UNIQUE (id, series_id, roster_id);

--
-- Name: game_slots game_slots_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_slots
    ADD CONSTRAINT game_slots_pkey PRIMARY KEY (id);

--
-- Name: game_slots game_slots_series_position_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_slots
    ADD CONSTRAINT game_slots_series_position_key UNIQUE (series_id, slot_number);

--
-- Name: ready_windows ready_windows_id_wave_roster_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.ready_windows
    ADD CONSTRAINT ready_windows_id_wave_roster_key UNIQUE (id, wave_id, roster_id);

--
-- Name: ready_windows ready_windows_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.ready_windows
    ADD CONSTRAINT ready_windows_pkey PRIMARY KEY (id);

--
-- Name: ready_windows ready_windows_revision_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.ready_windows
    ADD CONSTRAINT ready_windows_revision_id_key UNIQUE (revision_id);

--
-- Name: ready_windows ready_windows_wave_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.ready_windows
    ADD CONSTRAINT ready_windows_wave_id_key UNIQUE (wave_id);

--
-- Name: series series_id_roster_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.series
    ADD CONSTRAINT series_id_roster_key UNIQUE (id, roster_id);

--
-- Name: series series_id_tournament_roster_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.series
    ADD CONSTRAINT series_id_tournament_roster_key UNIQUE (id, tournament_id, roster_id);

--
-- Name: series series_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.series
    ADD CONSTRAINT series_pkey PRIMARY KEY (id);

--
-- Name: wave_members wave_members_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.wave_members
    ADD CONSTRAINT wave_members_pkey PRIMARY KEY (wave_id, participant_id);

--
-- Name: wave_members wave_members_wave_roster_participant_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.wave_members
    ADD CONSTRAINT wave_members_wave_roster_participant_key UNIQUE (wave_id, roster_id, participant_id);

--
-- Name: wave_series wave_series_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.wave_series
    ADD CONSTRAINT wave_series_pkey PRIMARY KEY (wave_id, series_id);

--
-- Name: wave_readiness wave_readiness_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.wave_readiness
    ADD CONSTRAINT wave_readiness_pkey PRIMARY KEY (wave_id, participant_id);

--
-- Name: readiness_events readiness_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.readiness_events
    ADD CONSTRAINT readiness_events_pkey PRIMARY KEY (command_id);

--
-- Name: readiness_events readiness_events_revision_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.readiness_events
    ADD CONSTRAINT readiness_events_revision_key UNIQUE (wave_id, participant_id, readiness_revision);

--
-- Name: execution_authority_leases execution_authority_leases_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.execution_authority_leases
    ADD CONSTRAINT execution_authority_leases_pkey PRIMARY KEY (tournament_id, command_id);

--
-- Name: execution_authority_leases execution_authority_leases_revision_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.execution_authority_leases
    ADD CONSTRAINT execution_authority_leases_revision_key UNIQUE (tournament_id, revision);

ALTER TABLE ONLY public.execution_game_epochs
    ADD CONSTRAINT execution_game_epochs_pkey PRIMARY KEY (game_attempt_id);

ALTER TABLE ONLY public.execution_game_epoch_rebinds
    ADD CONSTRAINT execution_game_epoch_rebinds_pkey PRIMARY KEY (game_attempt_id, rebind_sequence);

ALTER TABLE ONLY public.execution_game_epoch_rebinds
    ADD CONSTRAINT execution_game_epoch_rebinds_command_key UNIQUE (game_attempt_id, command_id);

ALTER TABLE ONLY public.execution_epoch_replays
    ADD CONSTRAINT execution_epoch_replays_pkey PRIMARY KEY (command_id);

ALTER TABLE ONLY public.execution_epoch_replays
    ADD CONSTRAINT execution_epoch_replays_game_attempt_key UNIQUE (game_attempt_id);

--
-- Name: waves waves_id_roster_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.waves
    ADD CONSTRAINT waves_id_roster_key UNIQUE (id, roster_id);

--
-- Name: waves waves_id_tournament_roster_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.waves
    ADD CONSTRAINT waves_id_tournament_roster_key UNIQUE (id, tournament_id, roster_id);

ALTER TABLE ONLY public.swiss_wave_links
    ADD CONSTRAINT swiss_wave_links_wave_fk
    FOREIGN KEY (wave_id, tournament_id, roster_id)
    REFERENCES public.waves(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.wave_control_commands
    ADD CONSTRAINT wave_control_commands_wave_fk
    FOREIGN KEY (wave_id, tournament_id, roster_id)
    REFERENCES public.waves(id, tournament_id, roster_id) ON DELETE RESTRICT;

--
-- Name: waves waves_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.waves
    ADD CONSTRAINT waves_pkey PRIMARY KEY (id);

--
-- Name: waves waves_replaces_wave_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.waves
    ADD CONSTRAINT waves_replaces_wave_id_key UNIQUE (replaces_wave_id);

--
-- Name: waves waves_revision_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.waves
    ADD CONSTRAINT waves_revision_id_key UNIQUE (revision_id);

--
-- Name: game_attempts_assignment_identity_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX game_attempts_assignment_identity_idx ON public.game_attempts USING btree (id, series_id, roster_id);

--
-- Name: game_attempts_one_non_terminal_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX game_attempts_one_non_terminal_idx ON public.game_attempts USING btree (slot_id) WHERE ((state)::text = ANY ((ARRAY['planned'::character varying, 'ready'::character varying, 'active'::character varying, 'paused'::character varying])::text[]));

--
-- Name: game_attempts_slot_created_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX game_attempts_slot_created_idx ON public.game_attempts USING btree (slot_id, attempt_number, created_at);

--
-- Name: series_draft_identity_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX series_draft_identity_idx ON public.series USING btree (id, roster_id, first_participant_id, second_participant_id, format);

--
-- Name: wave_members_participant_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX wave_members_participant_idx ON public.wave_members USING btree (roster_id, participant_id, wave_id);

--
-- Name: wave_series_series_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX wave_series_series_idx ON public.wave_series USING btree (series_id, created_at);

--
-- Name: wave_readiness_window_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX wave_readiness_window_idx ON public.wave_readiness USING btree (ready_window_id, ready, participant_id);

--
-- Name: readiness_events_window_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX readiness_events_window_idx ON public.readiness_events USING btree (ready_window_id, readiness_revision, participant_id);

--
-- Name: execution_authority_leases_expiry_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX execution_authority_leases_expiry_idx ON public.execution_authority_leases USING btree (expires_at, tournament_id);

CREATE INDEX execution_game_epochs_tournament_wave_idx
    ON public.execution_game_epochs USING btree (tournament_id, wave_id, game_attempt_id);

CREATE INDEX execution_game_epoch_rebinds_game_sequence_idx
    ON public.execution_game_epoch_rebinds USING btree (game_attempt_id, rebind_sequence DESC);

--
-- Name: waves_tournament_state_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX waves_tournament_state_idx ON public.waves USING btree (tournament_id, state, created_at);

-- Name: game_attempts game_attempt_identity_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER game_attempt_identity_guard BEFORE DELETE OR UPDATE ON public.game_attempts FOR EACH ROW EXECUTE FUNCTION public.game_attempt_identity_guard();

--
-- Name: game_attempts game_attempt_insert_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER game_attempt_insert_guard BEFORE INSERT ON public.game_attempts FOR EACH ROW EXECUTE FUNCTION public.game_attempt_insert_guard();

--
-- Name: game_slots game_slot_identity_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER game_slot_identity_guard BEFORE DELETE OR UPDATE ON public.game_slots FOR EACH ROW EXECUTE FUNCTION public.game_slot_identity_guard();

--
-- Name: ready_windows ready_window_lifecycle_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER ready_window_lifecycle_guard BEFORE DELETE OR UPDATE ON public.ready_windows FOR EACH ROW EXECUTE FUNCTION public.ready_window_lifecycle_guard();

--
-- Name: ready_windows ready_windows_wave_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER ready_windows_wave_consistency AFTER INSERT OR UPDATE ON public.ready_windows DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_wave_window();

--
-- Name: waves wave_identity_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER wave_identity_guard BEFORE DELETE OR UPDATE ON public.waves FOR EACH ROW EXECUTE FUNCTION public.wave_identity_guard();

--
-- Name: wave_readiness wave_readiness_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER wave_readiness_guard BEFORE INSERT OR DELETE OR UPDATE ON public.wave_readiness FOR EACH ROW EXECUTE FUNCTION public.wave_readiness_guard();

--
-- Name: readiness_events readiness_event_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER readiness_event_guard BEFORE INSERT OR DELETE OR UPDATE ON public.readiness_events FOR EACH ROW EXECUTE FUNCTION public.readiness_event_guard();

--
-- Name: execution_authority_leases execution_authority_lease_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER execution_authority_lease_guard BEFORE INSERT OR DELETE OR UPDATE ON public.execution_authority_leases FOR EACH ROW EXECUTE FUNCTION public.execution_authority_lease_guard();

CREATE TRIGGER execution_game_epochs_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.execution_game_epochs
FOR EACH ROW EXECUTE FUNCTION public.execution_epoch_evidence_guard();

CREATE TRIGGER execution_game_epoch_rebinds_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.execution_game_epoch_rebinds
FOR EACH ROW EXECUTE FUNCTION public.execution_epoch_rebind_guard();

CREATE CONSTRAINT TRIGGER execution_game_epoch_rebind_command_guard
AFTER INSERT ON public.execution_game_epoch_rebinds
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.execution_epoch_rebind_command_guard();

CREATE TRIGGER execution_epoch_replays_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.execution_epoch_replays
FOR EACH ROW EXECUTE FUNCTION public.execution_epoch_evidence_guard();

--
-- Name: waves waves_window_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER waves_window_consistency AFTER INSERT OR UPDATE ON public.waves DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_wave_window();

--
-- Name: game_attempts game_attempts_slot_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_attempts
    ADD CONSTRAINT game_attempts_slot_fk FOREIGN KEY (slot_id, series_id, roster_id) REFERENCES public.game_slots(id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: game_attempts game_attempts_winner_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_attempts
    ADD CONSTRAINT game_attempts_winner_fk FOREIGN KEY (roster_id, winner_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

--
-- Name: game_slots game_slots_series_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_slots
    ADD CONSTRAINT game_slots_series_fk FOREIGN KEY (series_id, roster_id) REFERENCES public.series(id, roster_id) ON DELETE RESTRICT;

--
-- Name: ready_windows ready_windows_wave_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.ready_windows
    ADD CONSTRAINT ready_windows_wave_fk FOREIGN KEY (wave_id, roster_id) REFERENCES public.waves(id, roster_id) ON DELETE RESTRICT;

--
-- Name: series series_first_participant_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.series
    ADD CONSTRAINT series_first_participant_fk FOREIGN KEY (roster_id, first_participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

--
-- Name: series series_roster_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.series
    ADD CONSTRAINT series_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

--
-- Name: series series_second_participant_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.series
    ADD CONSTRAINT series_second_participant_fk FOREIGN KEY (roster_id, second_participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

--
-- Name: series series_winner_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.series
    ADD CONSTRAINT series_winner_fk FOREIGN KEY (roster_id, winner_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

--
-- Name: wave_members wave_members_participant_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.wave_members
    ADD CONSTRAINT wave_members_participant_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

--
-- Name: wave_members wave_members_wave_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.wave_members
    ADD CONSTRAINT wave_members_wave_fk FOREIGN KEY (wave_id, roster_id) REFERENCES public.waves(id, roster_id) ON DELETE RESTRICT;

--
-- Name: wave_series wave_series_series_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.wave_series
    ADD CONSTRAINT wave_series_series_fk FOREIGN KEY (series_id, tournament_id, roster_id) REFERENCES public.series(id, tournament_id, roster_id) ON DELETE RESTRICT;

--
-- Name: wave_series wave_series_wave_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.wave_series
    ADD CONSTRAINT wave_series_wave_fk FOREIGN KEY (wave_id, tournament_id, roster_id) REFERENCES public.waves(id, tournament_id, roster_id) ON DELETE RESTRICT;

--
-- Name: wave_readiness wave_readiness_member_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.wave_readiness
    ADD CONSTRAINT wave_readiness_member_fk FOREIGN KEY (wave_id, roster_id, participant_id) REFERENCES public.wave_members(wave_id, roster_id, participant_id) ON DELETE RESTRICT;

--
-- Name: wave_readiness wave_readiness_window_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.wave_readiness
    ADD CONSTRAINT wave_readiness_window_fk FOREIGN KEY (ready_window_id, wave_id, roster_id) REFERENCES public.ready_windows(id, wave_id, roster_id) ON DELETE RESTRICT;

--
-- Name: readiness_events readiness_events_member_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.readiness_events
    ADD CONSTRAINT readiness_events_member_fk FOREIGN KEY (wave_id, roster_id, participant_id) REFERENCES public.wave_members(wave_id, roster_id, participant_id) ON DELETE RESTRICT;

--
-- Name: readiness_events readiness_events_window_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.readiness_events
    ADD CONSTRAINT readiness_events_window_fk FOREIGN KEY (ready_window_id, wave_id, roster_id) REFERENCES public.ready_windows(id, wave_id, roster_id) ON DELETE RESTRICT;

--
-- Name: execution_authority_leases execution_authority_leases_predecessor_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.execution_authority_leases
    ADD CONSTRAINT execution_authority_leases_predecessor_fk FOREIGN KEY (tournament_id, previous_revision) REFERENCES public.execution_authority_leases(tournament_id, revision) ON DELETE RESTRICT;

--
-- Name: execution_authority_leases execution_authority_leases_tournament_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.execution_authority_leases
    ADD CONSTRAINT execution_authority_leases_tournament_fk FOREIGN KEY (tournament_id) REFERENCES public.tournaments(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.execution_game_epochs
    ADD CONSTRAINT execution_game_epochs_game_fk
    FOREIGN KEY (game_attempt_id, series_id, roster_id)
    REFERENCES public.game_attempts(id, series_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.execution_game_epochs
    ADD CONSTRAINT execution_game_epochs_wave_fk
    FOREIGN KEY (wave_id, tournament_id, roster_id)
    REFERENCES public.waves(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.execution_game_epochs
    ADD CONSTRAINT execution_game_epochs_authority_fk
    FOREIGN KEY (tournament_id, authority_revision)
    REFERENCES public.execution_authority_leases(tournament_id, revision) ON DELETE RESTRICT;

ALTER TABLE ONLY public.execution_game_epoch_rebinds
    ADD CONSTRAINT execution_game_epoch_rebinds_epoch_fk
    FOREIGN KEY (game_attempt_id) REFERENCES public.execution_game_epochs(game_attempt_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.execution_game_epoch_rebinds
    ADD CONSTRAINT execution_game_epoch_rebinds_game_fk
    FOREIGN KEY (game_attempt_id, series_id, roster_id)
    REFERENCES public.game_attempts(id, series_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.execution_game_epoch_rebinds
    ADD CONSTRAINT execution_game_epoch_rebinds_wave_fk
    FOREIGN KEY (wave_id, tournament_id, roster_id)
    REFERENCES public.waves(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.execution_game_epoch_rebinds
    ADD CONSTRAINT execution_game_epoch_rebinds_previous_authority_fk
    FOREIGN KEY (tournament_id, previous_authority_revision)
    REFERENCES public.execution_authority_leases(tournament_id, revision) ON DELETE RESTRICT;

ALTER TABLE ONLY public.execution_game_epoch_rebinds
    ADD CONSTRAINT execution_game_epoch_rebinds_authority_fk
    FOREIGN KEY (tournament_id, authority_revision)
    REFERENCES public.execution_authority_leases(tournament_id, revision) ON DELETE RESTRICT;

-- A rebind is part of the durable resume command. It is deferred because the
-- Wave transaction writes the fenced rebind before it records command receipt.
ALTER TABLE ONLY public.execution_game_epoch_rebinds
    ADD CONSTRAINT execution_game_epoch_rebinds_command_fk
    FOREIGN KEY (command_id, tournament_id)
    REFERENCES public.wave_control_commands(command_id, tournament_id)
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.execution_epoch_replays
    ADD CONSTRAINT execution_epoch_replays_game_fk
    FOREIGN KEY (game_attempt_id, series_id, roster_id)
    REFERENCES public.game_attempts(id, series_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.execution_epoch_replays
    ADD CONSTRAINT execution_epoch_replays_epoch_fk
    FOREIGN KEY (game_attempt_id) REFERENCES public.execution_game_epochs(game_attempt_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.execution_epoch_replays
    ADD CONSTRAINT execution_epoch_replays_current_authority_fk
    FOREIGN KEY (tournament_id, expected_lease_revision)
    REFERENCES public.execution_authority_leases(tournament_id, revision) ON DELETE RESTRICT;

--
-- Name: waves waves_replaces_wave_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.waves
    ADD CONSTRAINT waves_replaces_wave_id_fkey FOREIGN KEY (replaces_wave_id) REFERENCES public.waves(id) ON DELETE RESTRICT;

--
-- Name: waves waves_roster_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.waves
    ADD CONSTRAINT waves_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

-- Durable evidence for routing a terminal Game out of its current Wave slot.
CREATE TABLE public.wave_member_routes (
    id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    wave_id uuid NOT NULL,
    series_id uuid NOT NULL,
    slot_id uuid NOT NULL,
    game_attempt_id uuid NOT NULL,
    category character varying(32) NOT NULL,
    routed_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT wave_member_routes_category_check CHECK (((category)::text = ANY ((ARRAY['web'::character varying, 'crypto'::character varying, 'forensics'::character varying, 'reverse'::character varying, 'pwn'::character varying, 'steganography'::character varying, 'ppc'::character varying, 'osint'::character varying, 'mobile'::character varying, 'hardware'::character varying, 'misc'::character varying])::text[]))),
    CONSTRAINT wave_member_routes_timestamps_check CHECK ((routed_at <= created_at))
);

ALTER TABLE ONLY public.wave_member_routes
    ADD CONSTRAINT wave_member_routes_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.wave_member_routes
    ADD CONSTRAINT wave_member_routes_game_key UNIQUE (game_attempt_id);

ALTER TABLE ONLY public.wave_member_routes
    ADD CONSTRAINT wave_member_routes_wave_series_key UNIQUE (wave_id, series_id, game_attempt_id);

ALTER TABLE ONLY public.wave_member_routes
    ADD CONSTRAINT wave_member_routes_wave_fk FOREIGN KEY (wave_id, tournament_id, roster_id) REFERENCES public.waves(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.wave_member_routes
    ADD CONSTRAINT wave_member_routes_series_fk FOREIGN KEY (series_id, tournament_id, roster_id) REFERENCES public.series(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.wave_member_routes
    ADD CONSTRAINT wave_member_routes_wave_series_fk FOREIGN KEY (wave_id, series_id) REFERENCES public.wave_series(wave_id, series_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.wave_member_routes
    ADD CONSTRAINT wave_member_routes_slot_fk FOREIGN KEY (slot_id, series_id, roster_id) REFERENCES public.game_slots(id, series_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.wave_member_routes
    ADD CONSTRAINT wave_member_routes_game_fk FOREIGN KEY (game_attempt_id, series_id, roster_id) REFERENCES public.game_attempts(id, series_id, roster_id) ON DELETE RESTRICT;

CREATE FUNCTION public.wave_member_route_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    slot_category VARCHAR(32);
    attempt_slot_id UUID;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Wave member routes are append-only evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT category
    INTO slot_category
    FROM game_slots
    WHERE id = NEW.slot_id
        AND series_id = NEW.series_id
        AND roster_id = NEW.roster_id;

    SELECT slot_id
    INTO attempt_slot_id
    FROM game_attempts
    WHERE id = NEW.game_attempt_id
        AND series_id = NEW.series_id
        AND roster_id = NEW.roster_id;

    IF slot_category IS DISTINCT FROM NEW.category
        OR attempt_slot_id IS DISTINCT FROM NEW.slot_id THEN
        RAISE EXCEPTION 'Wave member route does not match its Game slot'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER wave_member_routes_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.wave_member_routes
FOR EACH ROW EXECUTE FUNCTION public.wave_member_route_guard();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS
    public.wave_member_routes,
    public.wave_control_commands,
    public.swiss_wave_links,
    public.execution_epoch_replays,
    public.execution_game_epoch_rebinds,
    public.execution_game_epochs,
    public.execution_authority_leases,
    public.readiness_events,
    public.wave_readiness,
    public.ready_windows,
    public.wave_series,
    public.wave_members,
    public.waves,
    public.game_attempts,
    public.game_slots,
    public.series;

DROP FUNCTION IF EXISTS
    public.wave_member_route_guard(),
    public.execution_epoch_rebind_command_guard(),
    public.execution_epoch_rebind_guard(),
    public.execution_epoch_evidence_guard(),
    public.execution_authority_lease_guard(),
    public.readiness_event_guard(),
    public.wave_readiness_guard(),
    public.wave_identity_guard(),
    public.validate_wave_window(),
    public.ready_window_lifecycle_guard(),
    public.game_slot_identity_guard(),
    public.game_attempt_insert_guard(),
    public.game_attempt_identity_guard();

-- +goose StatementEnd
