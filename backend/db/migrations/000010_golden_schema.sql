-- +goose Up
-- +goose StatementBegin

-- Initial golden domain schema.
SET LOCAL check_function_bodies = false;

--
-- Name: golden_attempt_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.golden_attempt_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    previous_number INTEGER;
    ready_count INTEGER;
    unresolved_direct_count INTEGER;
    participant_count INTEGER;
    open_disconnect_count INTEGER;
    uncommitted_count INTEGER;
    recovery_state VARCHAR(24);
    recovery_recorded_at TIMESTAMPTZ;
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.previous_attempt_id IS NOT NULL THEN
            SELECT attempt_number INTO previous_number
            FROM golden_attempts
            WHERE id = NEW.previous_attempt_id
            FOR KEY SHARE;

            IF previous_number <> NEW.attempt_number - 1 THEN
                RAISE EXCEPTION 'Golden attempt lineage must be consecutive'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;

        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Golden attempts are retained stage history'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.attempt_number IS DISTINCT FROM OLD.attempt_number
        OR NEW.previous_attempt_id IS DISTINCT FROM OLD.previous_attempt_id
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Golden attempt identity and lineage are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF (OLD.disclosed_at IS NOT NULL AND NEW.disclosed_at IS DISTINCT FROM OLD.disclosed_at)
        OR (OLD.ready_at IS NOT NULL AND NEW.ready_at IS DISTINCT FROM OLD.ready_at)
        OR (OLD.started_at IS NOT NULL AND NEW.started_at IS DISTINCT FROM OLD.started_at)
        OR (OLD.completed_at IS NOT NULL AND NEW.completed_at IS DISTINCT FROM OLD.completed_at)
        OR (OLD.cancelled_at IS NOT NULL AND NEW.cancelled_at IS DISTINCT FROM OLD.cancelled_at)
        OR (OLD.superseded_at IS NOT NULL AND NEW.superseded_at IS DISTINCT FROM OLD.superseded_at)
        OR (
            OLD.cancellation_reason IS NOT NULL
            AND NEW.cancellation_reason IS DISTINCT FROM OLD.cancellation_reason
        )
        OR (
            OLD.supersession_reason IS NOT NULL
            AND NEW.supersession_reason IS DISTINCT FROM OLD.supersession_reason
        ) THEN
        RAISE EXCEPTION 'Golden attempt lifecycle evidence is immutable once recorded'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state IS DISTINCT FROM OLD.state
        AND NOT (
            (OLD.state = 'prepared' AND NEW.state IN ('ready', 'cancelled', 'superseded'))
            OR (OLD.state = 'ready' AND NEW.state IN ('active', 'cancelled', 'superseded'))
            OR (OLD.state = 'active' AND NEW.state IN ('technical_pause', 'completed', 'cancelled'))
            OR (OLD.state = 'technical_pause' AND NEW.state IN ('active', 'completed', 'cancelled'))
        ) THEN
        RAISE EXCEPTION 'invalid Golden attempt lifecycle transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.state = 'active' AND NEW.state = 'technical_pause' THEN
        SELECT state, recorded_at
        INTO recovery_state, recovery_recorded_at
        FROM golden_recovery_revisions
        WHERE attempt_id = NEW.id
        ORDER BY revision_number DESC
        LIMIT 1;

        IF recovery_state IS DISTINCT FROM 'technical_pause'
            OR recovery_recorded_at IS NULL
            OR recovery_recorded_at > NEW.paused_at THEN
            RAISE EXCEPTION 'Golden technical pause requires current recovery evidence'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF OLD.state = 'technical_pause' AND NEW.state = 'active' THEN
        SELECT state, recorded_at
        INTO recovery_state, recovery_recorded_at
        FROM golden_recovery_revisions
        WHERE attempt_id = NEW.id
        ORDER BY revision_number DESC
        LIMIT 1;

        IF recovery_state IS DISTINCT FROM 'resumed'
            OR recovery_recorded_at IS NULL
            OR recovery_recorded_at < OLD.paused_at THEN
            RAISE EXCEPTION 'Golden resume requires current recovery evidence'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF NEW.state = 'ready' AND OLD.state <> 'ready' THEN
        SELECT
            COUNT(*) FILTER (WHERE ready_at IS NOT NULL),
            COUNT(*) FILTER (
                WHERE selection_kind = 'direct'
                    AND ready_at IS NULL
                    AND no_show_at IS NULL
            )
        INTO ready_count, unresolved_direct_count
        FROM golden_memberships
        WHERE attempt_id = NEW.id;

        IF ready_count < 2 OR unresolved_direct_count <> 0 THEN
            RAISE EXCEPTION 'Golden readiness requires two ready members and resolved direct selections'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF NEW.state = 'active' AND OLD.state <> 'active' THEN
        SELECT COUNT(*) INTO participant_count
        FROM golden_memberships
        WHERE attempt_id = NEW.id
            AND participation_established_at IS NOT NULL;

        SELECT COUNT(*) INTO open_disconnect_count
        FROM golden_ready_disconnects
        WHERE attempt_id = NEW.id AND state = 'open';

        IF participant_count < 2 OR open_disconnect_count <> 0 THEN
            RAISE EXCEPTION 'Golden start requires established participants without open disconnects'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF NEW.state = 'completed' AND OLD.state <> 'completed' THEN
        SELECT
            COUNT(*) FILTER (WHERE participation_established_at IS NOT NULL),
            COUNT(*) FILTER (
                WHERE participation_established_at IS NOT NULL
                    AND NOT EXISTS (
                        SELECT 1
                        FROM golden_position_commits AS position_commit
                        WHERE position_commit.membership_id = membership.id
                    )
            )
        INTO participant_count, uncommitted_count
        FROM golden_memberships AS membership
        WHERE attempt_id = NEW.id;

        IF participant_count < 2 OR uncommitted_count <> 0 THEN
            RAISE EXCEPTION 'Golden completion requires every participant position commit'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: golden_disconnect_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.golden_disconnect_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    membership_ready_at TIMESTAMPTZ;
    membership_no_show_at TIMESTAMPTZ;
    attempt_state VARCHAR(24);
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Golden ready disconnect evidence is retained'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_OP = 'UPDATE' THEN
        IF NEW.id IS DISTINCT FROM OLD.id
            OR NEW.membership_id IS DISTINCT FROM OLD.membership_id
            OR NEW.attempt_id IS DISTINCT FROM OLD.attempt_id
            OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
            OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
            OR NEW.participant_id IS DISTINCT FROM OLD.participant_id
            OR NEW.sequence_number IS DISTINCT FROM OLD.sequence_number
            OR NEW.disconnected_at IS DISTINCT FROM OLD.disconnected_at
            OR NEW.created_at IS DISTINCT FROM OLD.created_at
            OR OLD.state <> 'open'
            OR NEW.state NOT IN ('reconnected', 'expired') THEN
            RAISE EXCEPTION 'Golden ready disconnect identity and terminal evidence are immutable'
                USING ERRCODE = 'check_violation';
        END IF;

        RETURN NEW;
    END IF;

    SELECT ready_at, no_show_at
    INTO membership_ready_at, membership_no_show_at
    FROM golden_memberships
    WHERE id = NEW.membership_id
    FOR NO KEY UPDATE;

    SELECT state INTO attempt_state
    FROM golden_attempts
    WHERE id = NEW.attempt_id
    FOR NO KEY UPDATE;

    IF membership_ready_at IS NULL
        OR membership_no_show_at IS NOT NULL
        OR NEW.disconnected_at < membership_ready_at
        OR attempt_state NOT IN ('ready', 'active', 'technical_pause') THEN
        RAISE EXCEPTION 'Golden disconnect requires retained ready membership'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: golden_membership_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.golden_membership_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    attempt_state VARCHAR(24);
    promotion_count INTEGER;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Golden membership evidence is retained'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT state INTO attempt_state
    FROM golden_attempts
    WHERE id = NEW.attempt_id
    FOR NO KEY UPDATE;

    IF TG_OP = 'INSERT' THEN
        IF attempt_state <> 'prepared' THEN
            RAISE EXCEPTION 'Golden membership selection is closed after preparation'
                USING ERRCODE = 'check_violation';
        END IF;

        RETURN NEW;
    END IF;

    IF attempt_state NOT IN ('prepared', 'ready') THEN
        RAISE EXCEPTION 'Golden membership evidence is closed after start'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.attempt_id IS DISTINCT FROM OLD.attempt_id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.participant_id IS DISTINCT FROM OLD.participant_id
        OR NEW.selection_kind IS DISTINCT FROM OLD.selection_kind
        OR NEW.reserve_position IS DISTINCT FROM OLD.reserve_position
        OR NEW.selected_at IS DISTINCT FROM OLD.selected_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR NEW.excluded_at IS DISTINCT FROM OLD.excluded_at
        OR NEW.exclusion_reason IS DISTINCT FROM OLD.exclusion_reason THEN
        RAISE EXCEPTION 'Golden selection and exclusion evidence are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF (OLD.ready_at IS NOT NULL AND NEW.ready_at IS DISTINCT FROM OLD.ready_at)
        OR (OLD.no_show_at IS NOT NULL AND NEW.no_show_at IS DISTINCT FROM OLD.no_show_at)
        OR (
            OLD.participation_established_at IS NOT NULL
            AND NEW.participation_established_at IS DISTINCT FROM OLD.participation_established_at
        ) THEN
        RAISE EXCEPTION 'Golden participation evidence is irreversible'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.selection_kind = 'reserve'
        AND OLD.participation_established_at IS NULL
        AND NEW.participation_established_at IS NOT NULL THEN
        SELECT COUNT(*) INTO promotion_count
        FROM golden_reserve_promotions
        WHERE reserve_membership_id = NEW.id;

        IF promotion_count <> 1 THEN
            RAISE EXCEPTION 'Golden reserve participation requires promotion evidence'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: golden_position_commit_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.golden_position_commit_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    submission_status VARCHAR(16);
    submission_position SMALLINT;
    participation_at TIMESTAMPTZ;
    attempt_state VARCHAR(24);
    current_attempt_number INTEGER;
    previous_attempt_number INTEGER;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden position commits are immutable terminal evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT status, provisional_position
    INTO submission_status, submission_position
    FROM golden_provisional_submissions
    WHERE id = NEW.provisional_submission_id
    FOR KEY SHARE;

    SELECT participation_established_at INTO participation_at
    FROM golden_memberships
    WHERE id = NEW.membership_id
    FOR NO KEY UPDATE;

    SELECT state, attempt_number
    INTO attempt_state, current_attempt_number
    FROM golden_attempts
    WHERE id = NEW.attempt_id
    FOR NO KEY UPDATE;

    IF submission_status <> 'accepted'
        OR submission_position <> NEW.position
        OR participation_at IS NULL
        OR attempt_state NOT IN ('active', 'technical_pause') THEN
        RAISE EXCEPTION 'Golden position commit must seal an accepted active submission'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.previous_position_commit_id IS NOT NULL THEN
        SELECT attempt.attempt_number INTO previous_attempt_number
        FROM golden_position_commits AS position_commit
        INNER JOIN golden_attempts AS attempt
            ON attempt.id = position_commit.attempt_id
        WHERE position_commit.id = NEW.previous_position_commit_id
        FOR KEY SHARE OF position_commit, attempt;

        IF previous_attempt_number >= current_attempt_number THEN
            RAISE EXCEPTION 'Golden prior position must belong to an earlier attempt'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: golden_recovery_revision_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.golden_recovery_revision_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    previous_number BIGINT;
    previous_state VARCHAR(24);
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden recovery revisions are immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.previous_revision_id IS NULL THEN
        IF NEW.state NOT IN ('stable', 'technical_pause') THEN
            RAISE EXCEPTION 'initial Golden recovery state must be stable or paused'
                USING ERRCODE = 'check_violation';
        END IF;

        RETURN NEW;
    END IF;

    SELECT revision_number, state
    INTO previous_number, previous_state
    FROM golden_recovery_revisions
    WHERE id = NEW.previous_revision_id
    FOR KEY SHARE;

    IF previous_number <> NEW.revision_number - 1
        OR NOT (
            (previous_state = 'stable' AND NEW.state = 'technical_pause')
            OR (previous_state = 'technical_pause' AND NEW.state IN ('recovering', 'resumed'))
            OR (previous_state = 'recovering' AND NEW.state = 'resumed')
            OR (previous_state = 'resumed' AND NEW.state = 'technical_pause')
        ) THEN
        RAISE EXCEPTION 'invalid Golden recovery revision transition'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: golden_reserve_promotion_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.golden_reserve_promotion_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    reserve_kind VARCHAR(16);
    replaced_kind VARCHAR(16);
    replaced_no_show_at TIMESTAMPTZ;
    attempt_state VARCHAR(24);
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden reserve promotions are immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT selection_kind INTO reserve_kind
    FROM golden_memberships
    WHERE id = NEW.reserve_membership_id
    FOR NO KEY UPDATE;

    SELECT selection_kind, no_show_at
    INTO replaced_kind, replaced_no_show_at
    FROM golden_memberships
    WHERE id = NEW.replaced_membership_id
    FOR NO KEY UPDATE;

    SELECT state INTO attempt_state
    FROM golden_attempts
    WHERE id = NEW.attempt_id
    FOR NO KEY UPDATE;

    IF reserve_kind <> 'reserve'
        OR replaced_kind <> 'direct'
        OR replaced_no_show_at IS NULL
        OR attempt_state NOT IN ('prepared', 'ready') THEN
        RAISE EXCEPTION 'Golden reserve promotion requires a direct no-show replacement'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: golden_submission_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.golden_submission_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    participation_at TIMESTAMPTZ;
    attempt_state VARCHAR(24);
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden provisional submissions are immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT participation_established_at INTO participation_at
    FROM golden_memberships
    WHERE id = NEW.membership_id
    FOR NO KEY UPDATE;

    SELECT state INTO attempt_state
    FROM golden_attempts
    WHERE id = NEW.attempt_id
    FOR NO KEY UPDATE;

    IF participation_at IS NULL
        OR attempt_state NOT IN ('active', 'technical_pause') THEN
        RAISE EXCEPTION 'Golden provisional submission requires an active participant'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: golden_attempts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.golden_attempts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    attempt_number integer NOT NULL,
    previous_attempt_id uuid,
    state character varying(24) DEFAULT 'prepared'::character varying NOT NULL,
    disclosed_at timestamp with time zone,
    ready_at timestamp with time zone,
    started_at timestamp with time zone,
    paused_at timestamp with time zone,
    completed_at timestamp with time zone,
    cancelled_at timestamp with time zone,
    cancellation_reason text,
    superseded_at timestamp with time zone,
    supersession_reason text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT golden_attempts_number_check CHECK (((attempt_number >= 1) AND (previous_attempt_id IS DISTINCT FROM id) AND (((attempt_number = 1) AND (previous_attempt_id IS NULL)) OR ((attempt_number > 1) AND (previous_attempt_id IS NOT NULL))))),
    CONSTRAINT golden_attempts_state_check CHECK (((state)::text = ANY ((ARRAY['prepared'::character varying, 'ready'::character varying, 'active'::character varying, 'technical_pause'::character varying, 'completed'::character varying, 'cancelled'::character varying, 'superseded'::character varying])::text[]))),
    CONSTRAINT golden_attempts_state_evidence_check CHECK (((((state)::text = 'prepared'::text) AND (disclosed_at IS NULL) AND (ready_at IS NULL) AND (started_at IS NULL) AND (paused_at IS NULL) AND (completed_at IS NULL) AND (cancelled_at IS NULL) AND (cancellation_reason IS NULL) AND (superseded_at IS NULL) AND (supersession_reason IS NULL)) OR (((state)::text = 'ready'::text) AND (disclosed_at IS NOT NULL) AND (ready_at IS NOT NULL) AND (started_at IS NULL) AND (paused_at IS NULL) AND (completed_at IS NULL) AND (cancelled_at IS NULL) AND (cancellation_reason IS NULL) AND (superseded_at IS NULL) AND (supersession_reason IS NULL)) OR (((state)::text = 'active'::text) AND (disclosed_at IS NOT NULL) AND (ready_at IS NOT NULL) AND (started_at IS NOT NULL) AND (paused_at IS NULL) AND (completed_at IS NULL) AND (cancelled_at IS NULL) AND (cancellation_reason IS NULL) AND (superseded_at IS NULL) AND (supersession_reason IS NULL)) OR (((state)::text = 'technical_pause'::text) AND (disclosed_at IS NOT NULL) AND (ready_at IS NOT NULL) AND (started_at IS NOT NULL) AND (paused_at IS NOT NULL) AND (completed_at IS NULL) AND (cancelled_at IS NULL) AND (cancellation_reason IS NULL) AND (superseded_at IS NULL) AND (supersession_reason IS NULL)) OR (((state)::text = 'completed'::text) AND (disclosed_at IS NOT NULL) AND (ready_at IS NOT NULL) AND (started_at IS NOT NULL) AND (paused_at IS NULL) AND (completed_at IS NOT NULL) AND (cancelled_at IS NULL) AND (cancellation_reason IS NULL) AND (superseded_at IS NULL) AND (supersession_reason IS NULL)) OR (((state)::text = 'cancelled'::text) AND (paused_at IS NULL) AND (completed_at IS NULL) AND (cancelled_at IS NOT NULL) AND (cancellation_reason = btrim(cancellation_reason)) AND (cancellation_reason <> ''::text) AND (superseded_at IS NULL) AND (supersession_reason IS NULL)) OR (((state)::text = 'superseded'::text) AND (started_at IS NULL) AND (paused_at IS NULL) AND (completed_at IS NULL) AND (cancelled_at IS NULL) AND (cancellation_reason IS NULL) AND (superseded_at IS NOT NULL) AND (supersession_reason = btrim(supersession_reason)) AND (supersession_reason <> ''::text) AND ((ready_at IS NULL) OR (disclosed_at IS NOT NULL))))),
    CONSTRAINT golden_attempts_timestamps_check CHECK ((((disclosed_at IS NULL) OR (disclosed_at >= created_at)) AND ((ready_at IS NULL) OR (ready_at >= disclosed_at)) AND ((started_at IS NULL) OR (started_at >= ready_at)) AND ((paused_at IS NULL) OR (paused_at >= started_at)) AND ((completed_at IS NULL) OR (completed_at >= started_at)) AND ((cancelled_at IS NULL) OR (cancelled_at >= created_at)) AND ((superseded_at IS NULL) OR (superseded_at >= created_at))))
);

--
-- Name: golden_memberships; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.golden_memberships (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    attempt_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    selection_kind character varying(16) NOT NULL,
    reserve_position smallint,
    selected_at timestamp with time zone NOT NULL,
    ready_at timestamp with time zone,
    no_show_at timestamp with time zone,
    participation_established_at timestamp with time zone,
    excluded_at timestamp with time zone,
    exclusion_reason text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT golden_memberships_readiness_check CHECK (((NOT ((ready_at IS NOT NULL) AND (no_show_at IS NOT NULL))) AND ((participation_established_at IS NULL) OR ((ready_at IS NOT NULL) AND (no_show_at IS NULL) AND ((selection_kind)::text <> 'excluded'::text))))),
    CONSTRAINT golden_memberships_selection_check CHECK (((((selection_kind)::text = 'direct'::text) AND (reserve_position IS NULL) AND (excluded_at IS NULL) AND (exclusion_reason IS NULL)) OR (((selection_kind)::text = 'reserve'::text) AND ((reserve_position >= 1) AND (reserve_position <= 16)) AND (excluded_at IS NULL) AND (exclusion_reason IS NULL)) OR (((selection_kind)::text = 'excluded'::text) AND (reserve_position IS NULL) AND (ready_at IS NULL) AND (no_show_at IS NULL) AND (participation_established_at IS NULL) AND (excluded_at IS NOT NULL) AND (exclusion_reason = btrim(exclusion_reason)) AND (exclusion_reason <> ''::text)))),
    CONSTRAINT golden_memberships_timestamps_check CHECK (((selected_at <= created_at) AND ((ready_at IS NULL) OR (ready_at >= selected_at)) AND ((no_show_at IS NULL) OR (no_show_at >= selected_at)) AND ((participation_established_at IS NULL) OR (participation_established_at >= ready_at)) AND ((excluded_at IS NULL) OR (excluded_at >= selected_at))))
);

--
-- Name: golden_position_commits; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.golden_position_commits (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    attempt_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    membership_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    provisional_submission_id uuid CONSTRAINT golden_position_commit_provisional_submission_id_not_null NOT NULL,
    previous_position_commit_id uuid,
    "position" smallint NOT NULL,
    committed_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT golden_position_commits_position_check CHECK (((("position" >= 1) AND ("position" <= 16)) AND (previous_position_commit_id IS DISTINCT FROM id))),
    CONSTRAINT golden_position_commits_timestamps_check CHECK ((committed_at <= created_at))
);

--
-- Name: golden_provisional_submissions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.golden_provisional_submissions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    attempt_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    membership_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    server_sequence bigint NOT NULL,
    idempotency_key uuid NOT NULL,
    provisional_position smallint CONSTRAINT golden_provisional_submissi_provisional_position_not_null NOT NULL,
    elapsed_milliseconds bigint CONSTRAINT golden_provisional_submissi_elapsed_milliseconds_not_null NOT NULL,
    status character varying(16) NOT NULL,
    rejection_reason text,
    payload_digest bytea NOT NULL,
    submitted_at timestamp with time zone NOT NULL,
    received_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT golden_provisional_submissions_digest_check CHECK (((octet_length(payload_digest) = 32) AND (payload_digest <> decode(repeat('00'::text, 32), 'hex'::text)))),
    CONSTRAINT golden_provisional_submissions_sequence_check CHECK (((server_sequence >= 1) AND ((provisional_position >= 1) AND (provisional_position <= 16)) AND (elapsed_milliseconds >= 0))),
    CONSTRAINT golden_provisional_submissions_status_check CHECK (((((status)::text = 'accepted'::text) AND (rejection_reason IS NULL)) OR (((status)::text = 'rejected'::text) AND (rejection_reason = btrim(rejection_reason)) AND (rejection_reason <> ''::text)))),
    CONSTRAINT golden_provisional_submissions_timestamps_check CHECK (((submitted_at <= received_at) AND (received_at <= created_at)))
);

--
-- Name: golden_ready_disconnects; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.golden_ready_disconnects (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    membership_id uuid NOT NULL,
    attempt_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    sequence_number integer NOT NULL,
    state character varying(16) DEFAULT 'open'::character varying NOT NULL,
    disconnected_at timestamp with time zone NOT NULL,
    reconnected_at timestamp with time zone,
    expired_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT golden_ready_disconnects_number_check CHECK ((sequence_number >= 1)),
    CONSTRAINT golden_ready_disconnects_state_check CHECK (((((state)::text = 'open'::text) AND (reconnected_at IS NULL) AND (expired_at IS NULL)) OR (((state)::text = 'reconnected'::text) AND (reconnected_at IS NOT NULL) AND (expired_at IS NULL)) OR (((state)::text = 'expired'::text) AND (reconnected_at IS NULL) AND (expired_at IS NOT NULL)))),
    CONSTRAINT golden_ready_disconnects_timestamps_check CHECK (((disconnected_at <= created_at) AND ((reconnected_at IS NULL) OR (reconnected_at >= disconnected_at)) AND ((expired_at IS NULL) OR (expired_at >= disconnected_at))))
);

--
-- Name: golden_recovery_revisions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.golden_recovery_revisions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    attempt_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    revision_number bigint NOT NULL,
    previous_revision_id uuid,
    state character varying(24) NOT NULL,
    recovery_evidence jsonb NOT NULL,
    recorded_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT golden_recovery_revisions_evidence_check CHECK (((jsonb_typeof(recovery_evidence) = 'object'::text) AND (recovery_evidence <> '{}'::jsonb))),
    CONSTRAINT golden_recovery_revisions_number_check CHECK (((revision_number >= 1) AND (((revision_number = 1) AND (previous_revision_id IS NULL)) OR ((revision_number > 1) AND (previous_revision_id IS NOT NULL))))),
    CONSTRAINT golden_recovery_revisions_state_check CHECK (((state)::text = ANY ((ARRAY['stable'::character varying, 'technical_pause'::character varying, 'recovering'::character varying, 'resumed'::character varying])::text[]))),
    CONSTRAINT golden_recovery_revisions_timestamps_check CHECK ((recorded_at <= created_at))
);

--
-- Name: golden_reserve_promotions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.golden_reserve_promotions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    attempt_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    reserve_membership_id uuid NOT NULL,
    reserve_participant_id uuid NOT NULL,
    replaced_membership_id uuid NOT NULL,
    replaced_participant_id uuid CONSTRAINT golden_reserve_promotion_replaced_participant_id_not_null NOT NULL,
    reason text NOT NULL,
    promoted_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT golden_reserve_promotions_participant_check CHECK ((reserve_participant_id <> replaced_participant_id)),
    CONSTRAINT golden_reserve_promotions_reason_check CHECK (((reason = btrim(reason)) AND (reason <> ''::text))),
    CONSTRAINT golden_reserve_promotions_timestamps_check CHECK ((promoted_at <= created_at))
);

--
-- Name: golden_attempts golden_attempts_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_attempts
    ADD CONSTRAINT golden_attempts_identity_key UNIQUE (id, tournament_id, roster_id);

--
-- Name: golden_attempts golden_attempts_number_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_attempts
    ADD CONSTRAINT golden_attempts_number_key UNIQUE (tournament_id, attempt_number);

--
-- Name: golden_attempts golden_attempts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_attempts
    ADD CONSTRAINT golden_attempts_pkey PRIMARY KEY (id);

--
-- Name: golden_attempts golden_attempts_previous_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_attempts
    ADD CONSTRAINT golden_attempts_previous_key UNIQUE (previous_attempt_id);

--
-- Name: golden_memberships golden_memberships_attempt_participant_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_memberships
    ADD CONSTRAINT golden_memberships_attempt_participant_key UNIQUE (attempt_id, participant_id);

--
-- Name: golden_memberships golden_memberships_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_memberships
    ADD CONSTRAINT golden_memberships_identity_key UNIQUE (id, attempt_id, participant_id, tournament_id, roster_id);

--
-- Name: golden_memberships golden_memberships_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_memberships
    ADD CONSTRAINT golden_memberships_pkey PRIMARY KEY (id);

--
-- Name: golden_position_commits golden_position_commits_attempt_position_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_position_commits
    ADD CONSTRAINT golden_position_commits_attempt_position_key UNIQUE (attempt_id, "position");

--
-- Name: golden_position_commits golden_position_commits_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_position_commits
    ADD CONSTRAINT golden_position_commits_identity_key UNIQUE (id, attempt_id, participant_id, tournament_id, roster_id);

--
-- Name: golden_position_commits golden_position_commits_member_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_position_commits
    ADD CONSTRAINT golden_position_commits_member_key UNIQUE (membership_id);

--
-- Name: golden_position_commits golden_position_commits_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_position_commits
    ADD CONSTRAINT golden_position_commits_pkey PRIMARY KEY (id);

--
-- Name: golden_position_commits golden_position_commits_previous_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_position_commits
    ADD CONSTRAINT golden_position_commits_previous_identity_key UNIQUE (id, participant_id, tournament_id, roster_id);

--
-- Name: golden_position_commits golden_position_commits_provisional_submission_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_position_commits
    ADD CONSTRAINT golden_position_commits_provisional_submission_id_key UNIQUE (provisional_submission_id);

--
-- Name: golden_position_commits golden_position_commits_scope_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_position_commits
    ADD CONSTRAINT golden_position_commits_scope_key UNIQUE (id, tournament_id, roster_id);

--
-- Name: golden_provisional_submissions golden_provisional_submissions_idempotency_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_provisional_submissions
    ADD CONSTRAINT golden_provisional_submissions_idempotency_key_key UNIQUE (idempotency_key);

--
-- Name: golden_provisional_submissions golden_provisional_submissions_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_provisional_submissions
    ADD CONSTRAINT golden_provisional_submissions_identity_key UNIQUE (id, attempt_id, membership_id, participant_id, tournament_id, roster_id);

--
-- Name: golden_provisional_submissions golden_provisional_submissions_member_sequence_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_provisional_submissions
    ADD CONSTRAINT golden_provisional_submissions_member_sequence_key UNIQUE (membership_id, server_sequence);

--
-- Name: golden_provisional_submissions golden_provisional_submissions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_provisional_submissions
    ADD CONSTRAINT golden_provisional_submissions_pkey PRIMARY KEY (id);

--
-- Name: golden_provisional_submissions golden_provisional_submissions_sequence_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_provisional_submissions
    ADD CONSTRAINT golden_provisional_submissions_sequence_key UNIQUE (attempt_id, server_sequence);

--
-- Name: golden_ready_disconnects golden_ready_disconnects_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_ready_disconnects
    ADD CONSTRAINT golden_ready_disconnects_identity_key UNIQUE (id, membership_id, attempt_id, participant_id);

--
-- Name: golden_ready_disconnects golden_ready_disconnects_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_ready_disconnects
    ADD CONSTRAINT golden_ready_disconnects_pkey PRIMARY KEY (id);

--
-- Name: golden_ready_disconnects golden_ready_disconnects_sequence_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_ready_disconnects
    ADD CONSTRAINT golden_ready_disconnects_sequence_key UNIQUE (membership_id, sequence_number);

--
-- Name: golden_recovery_revisions golden_recovery_revisions_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_recovery_revisions
    ADD CONSTRAINT golden_recovery_revisions_identity_key UNIQUE (id, attempt_id, tournament_id, roster_id);

--
-- Name: golden_recovery_revisions golden_recovery_revisions_number_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_recovery_revisions
    ADD CONSTRAINT golden_recovery_revisions_number_key UNIQUE (attempt_id, revision_number);

--
-- Name: golden_recovery_revisions golden_recovery_revisions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_recovery_revisions
    ADD CONSTRAINT golden_recovery_revisions_pkey PRIMARY KEY (id);

--
-- Name: golden_recovery_revisions golden_recovery_revisions_previous_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_recovery_revisions
    ADD CONSTRAINT golden_recovery_revisions_previous_key UNIQUE (previous_revision_id);

--
-- Name: golden_reserve_promotions golden_reserve_promotions_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_reserve_promotions
    ADD CONSTRAINT golden_reserve_promotions_identity_key UNIQUE (id, attempt_id, reserve_membership_id);

--
-- Name: golden_reserve_promotions golden_reserve_promotions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_reserve_promotions
    ADD CONSTRAINT golden_reserve_promotions_pkey PRIMARY KEY (id);

--
-- Name: golden_reserve_promotions golden_reserve_promotions_replaced_membership_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_reserve_promotions
    ADD CONSTRAINT golden_reserve_promotions_replaced_membership_id_key UNIQUE (replaced_membership_id);

--
-- Name: golden_reserve_promotions golden_reserve_promotions_reserve_membership_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_reserve_promotions
    ADD CONSTRAINT golden_reserve_promotions_reserve_membership_id_key UNIQUE (reserve_membership_id);

--
-- Name: golden_attempts_roster_state_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX golden_attempts_roster_state_idx ON public.golden_attempts USING btree (roster_id, state, attempt_number);

--
-- Name: golden_memberships_participant_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX golden_memberships_participant_idx ON public.golden_memberships USING btree (roster_id, participant_id, attempt_id);

--
-- Name: golden_memberships_reserve_position_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX golden_memberships_reserve_position_idx ON public.golden_memberships USING btree (attempt_id, reserve_position) WHERE ((selection_kind)::text = 'reserve'::text);

--
-- Name: golden_ready_disconnects_one_open_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX golden_ready_disconnects_one_open_idx ON public.golden_ready_disconnects USING btree (membership_id) WHERE ((state)::text = 'open'::text);

--
-- Name: golden_attempts golden_attempt_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER golden_attempt_guard BEFORE INSERT OR DELETE OR UPDATE ON public.golden_attempts FOR EACH ROW EXECUTE FUNCTION public.golden_attempt_guard();

--
-- Name: golden_ready_disconnects golden_disconnect_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER golden_disconnect_guard BEFORE INSERT OR DELETE OR UPDATE ON public.golden_ready_disconnects FOR EACH ROW EXECUTE FUNCTION public.golden_disconnect_guard();

--
-- Name: golden_memberships golden_membership_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER golden_membership_guard BEFORE INSERT OR DELETE OR UPDATE ON public.golden_memberships FOR EACH ROW EXECUTE FUNCTION public.golden_membership_guard();

--
-- Name: golden_position_commits golden_position_commit_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER golden_position_commit_guard BEFORE INSERT OR DELETE OR UPDATE ON public.golden_position_commits FOR EACH ROW EXECUTE FUNCTION public.golden_position_commit_guard();

--
-- Name: golden_recovery_revisions golden_recovery_revision_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER golden_recovery_revision_guard BEFORE INSERT OR DELETE OR UPDATE ON public.golden_recovery_revisions FOR EACH ROW EXECUTE FUNCTION public.golden_recovery_revision_guard();

--
-- Name: golden_reserve_promotions golden_reserve_promotion_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER golden_reserve_promotion_guard BEFORE INSERT OR DELETE OR UPDATE ON public.golden_reserve_promotions FOR EACH ROW EXECUTE FUNCTION public.golden_reserve_promotion_guard();

--
-- Name: golden_provisional_submissions golden_submission_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER golden_submission_guard BEFORE INSERT OR DELETE OR UPDATE ON public.golden_provisional_submissions FOR EACH ROW EXECUTE FUNCTION public.golden_submission_guard();

--
-- Name: golden_attempts golden_attempts_previous_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_attempts
    ADD CONSTRAINT golden_attempts_previous_fk FOREIGN KEY (previous_attempt_id, tournament_id, roster_id) REFERENCES public.golden_attempts(id, tournament_id, roster_id) ON DELETE RESTRICT;

--
-- Name: golden_attempts golden_attempts_roster_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_attempts
    ADD CONSTRAINT golden_attempts_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

--
-- Name: golden_memberships golden_memberships_attempt_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_memberships
    ADD CONSTRAINT golden_memberships_attempt_fk FOREIGN KEY (attempt_id, tournament_id, roster_id) REFERENCES public.golden_attempts(id, tournament_id, roster_id) ON DELETE RESTRICT;

--
-- Name: golden_memberships golden_memberships_participant_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_memberships
    ADD CONSTRAINT golden_memberships_participant_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

--
-- Name: golden_position_commits golden_position_commits_membership_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_position_commits
    ADD CONSTRAINT golden_position_commits_membership_fk FOREIGN KEY (membership_id, attempt_id, participant_id, tournament_id, roster_id) REFERENCES public.golden_memberships(id, attempt_id, participant_id, tournament_id, roster_id) ON DELETE RESTRICT;

--
-- Name: golden_position_commits golden_position_commits_previous_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_position_commits
    ADD CONSTRAINT golden_position_commits_previous_fk FOREIGN KEY (previous_position_commit_id, participant_id, tournament_id, roster_id) REFERENCES public.golden_position_commits(id, participant_id, tournament_id, roster_id) ON DELETE RESTRICT;

--
-- Name: golden_position_commits golden_position_commits_submission_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_position_commits
    ADD CONSTRAINT golden_position_commits_submission_fk FOREIGN KEY (provisional_submission_id, attempt_id, membership_id, participant_id, tournament_id, roster_id) REFERENCES public.golden_provisional_submissions(id, attempt_id, membership_id, participant_id, tournament_id, roster_id) ON DELETE RESTRICT;

--
-- Name: golden_provisional_submissions golden_provisional_submissions_membership_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_provisional_submissions
    ADD CONSTRAINT golden_provisional_submissions_membership_fk FOREIGN KEY (membership_id, attempt_id, participant_id, tournament_id, roster_id) REFERENCES public.golden_memberships(id, attempt_id, participant_id, tournament_id, roster_id) ON DELETE RESTRICT;

--
-- Name: golden_ready_disconnects golden_ready_disconnects_membership_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_ready_disconnects
    ADD CONSTRAINT golden_ready_disconnects_membership_fk FOREIGN KEY (membership_id, attempt_id, participant_id, tournament_id, roster_id) REFERENCES public.golden_memberships(id, attempt_id, participant_id, tournament_id, roster_id) ON DELETE RESTRICT;

--
-- Name: golden_recovery_revisions golden_recovery_revisions_attempt_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_recovery_revisions
    ADD CONSTRAINT golden_recovery_revisions_attempt_fk FOREIGN KEY (attempt_id, tournament_id, roster_id) REFERENCES public.golden_attempts(id, tournament_id, roster_id) ON DELETE RESTRICT;

--
-- Name: golden_recovery_revisions golden_recovery_revisions_previous_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_recovery_revisions
    ADD CONSTRAINT golden_recovery_revisions_previous_fk FOREIGN KEY (previous_revision_id, attempt_id, tournament_id, roster_id) REFERENCES public.golden_recovery_revisions(id, attempt_id, tournament_id, roster_id) ON DELETE RESTRICT;

--
-- Name: golden_reserve_promotions golden_reserve_promotions_replaced_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_reserve_promotions
    ADD CONSTRAINT golden_reserve_promotions_replaced_fk FOREIGN KEY (replaced_membership_id, attempt_id, replaced_participant_id, tournament_id, roster_id) REFERENCES public.golden_memberships(id, attempt_id, participant_id, tournament_id, roster_id) ON DELETE RESTRICT;

--
-- Name: golden_reserve_promotions golden_reserve_promotions_reserve_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.golden_reserve_promotions
    ADD CONSTRAINT golden_reserve_promotions_reserve_fk FOREIGN KEY (reserve_membership_id, attempt_id, reserve_participant_id, tournament_id, roster_id) REFERENCES public.golden_memberships(id, attempt_id, participant_id, tournament_id, roster_id) ON DELETE RESTRICT;

--
-- Golden groups are materialized from the exact Swiss tie evidence retained
-- by the StartGolden progression. Attempts are bound separately so retry
-- lineage remains append-only and every later settlement has a source group.
--

CREATE FUNCTION public.golden_group_revision_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    source_group_revision_id UUID;
    source_projection_revision_id UUID;
    source_projection_revision BIGINT;
    source_position_from SMALLINT;
    source_position_to SMALLINT;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden group revisions are append-only evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT
        tie_group.group_revision_id,
        tie_group.source_projection_revision_id,
        tie_group.source_projection_revision,
        tie_group.position_from,
        tie_group.position_to
    INTO
        source_group_revision_id,
        source_projection_revision_id,
        source_projection_revision,
        source_position_from,
        source_position_to
    FROM tournament_stage_tie_groups AS tie_group
    WHERE tie_group.command_id = NEW.stage_progression_command_id
        AND tie_group.tournament_id = NEW.tournament_id
        AND tie_group.roster_id = NEW.roster_id
        AND tie_group.group_id = NEW.group_id
    FOR KEY SHARE;

    IF NOT FOUND
        OR source_group_revision_id IS DISTINCT FROM NEW.revision_id
        OR source_projection_revision_id IS DISTINCT FROM NEW.source_projection_revision_id
        OR source_projection_revision IS DISTINCT FROM NEW.source_projection_revision
        OR source_position_from IS DISTINCT FROM NEW.position_from
        OR source_position_to IS DISTINCT FROM NEW.position_to THEN
        RAISE EXCEPTION 'Golden group revision must exactly bind its Swiss tie evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.validate_golden_group_revision_progression_open() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    parent_action TEXT;
    parent_transaction xid;
BEGIN
    SELECT lifecycle.action, lifecycle.xmin
    INTO parent_action, parent_transaction
    FROM tournament_lifecycle_commands AS lifecycle
    WHERE lifecycle.command_id = NEW.stage_progression_command_id
        AND lifecycle.tournament_id = NEW.tournament_id
        AND lifecycle.roster_id = NEW.roster_id;

    IF parent_action = 'correction_start_golden'
        AND parent_transaction IS DISTINCT FROM pg_current_xact_id()::text::xid THEN
        RAISE EXCEPTION 'committed correction_start_golden rejects a late Golden group revision'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.golden_attempt_stage_group_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden attempt stage bindings are append-only evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.validate_golden_stage_progression_groups() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    unbound_group_count INTEGER;
BEGIN
    IF NEW.action NOT IN ('start_golden', 'correction_start_golden', 'correction_refresh_golden') THEN
        RETURN NEW;
    END IF;

    SELECT COUNT(*)
    INTO unbound_group_count
    FROM tournament_stage_tie_groups AS tie_group
    WHERE tie_group.command_id = NEW.command_id
        AND tie_group.tournament_id = NEW.tournament_id
        AND tie_group.roster_id = NEW.roster_id
        AND NOT EXISTS (
            SELECT 1
            FROM golden_group_revisions AS golden_group
            WHERE golden_group.stage_progression_command_id = tie_group.command_id
                AND golden_group.tournament_id = tie_group.tournament_id
                AND golden_group.roster_id = tie_group.roster_id
                AND golden_group.group_id = tie_group.group_id
                AND golden_group.revision_id = tie_group.group_revision_id
        );

    IF unbound_group_count <> 0 THEN
        RAISE EXCEPTION 'Golden progression requires a materialized revision for every exact tie group'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TABLE public.golden_group_revisions (
    revision_id uuid NOT NULL,
    group_id uuid NOT NULL,
    stage_progression_command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    source_projection_revision_id uuid NOT NULL,
    source_projection_revision bigint NOT NULL,
    position_from smallint NOT NULL,
    position_to smallint NOT NULL,
    definition jsonb NOT NULL,
    definition_digest bytea NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT golden_group_revisions_definition_check CHECK (
        jsonb_typeof(definition) = 'object'
        AND definition <> '{}'::jsonb
        AND octet_length(definition_digest) = 32
        AND definition_digest <> decode(repeat('00', 32), 'hex')
    ),
    CONSTRAINT golden_group_revisions_position_check CHECK (
        position_from >= 1
        AND position_to >= position_from
        AND position_to <= 16
    ),
    CONSTRAINT golden_group_revisions_source_check CHECK (
        source_projection_revision >= 1
    )
);

CREATE TABLE public.golden_attempt_stage_groups (
    attempt_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    group_revision_id uuid NOT NULL,
    bound_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY public.golden_group_revisions
    ADD CONSTRAINT golden_group_revisions_pkey PRIMARY KEY (revision_id);

ALTER TABLE ONLY public.golden_group_revisions
    ADD CONSTRAINT golden_group_revisions_identity_key UNIQUE (revision_id, tournament_id, roster_id);

ALTER TABLE ONLY public.golden_group_revisions
    ADD CONSTRAINT golden_group_revisions_source_group_key UNIQUE (stage_progression_command_id, tournament_id, roster_id, group_id);

ALTER TABLE ONLY public.golden_group_revisions
    ADD CONSTRAINT golden_group_revisions_tie_group_fk FOREIGN KEY (stage_progression_command_id, tournament_id, roster_id, group_id) REFERENCES public.tournament_stage_tie_groups(command_id, tournament_id, roster_id, group_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_group_revisions
    ADD CONSTRAINT golden_group_revisions_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_attempt_stage_groups
    ADD CONSTRAINT golden_attempt_stage_groups_pkey PRIMARY KEY (attempt_id, tournament_id, roster_id);

ALTER TABLE ONLY public.golden_attempt_stage_groups
    ADD CONSTRAINT golden_attempt_stage_groups_identity_key UNIQUE (attempt_id, tournament_id, roster_id, group_revision_id);

ALTER TABLE ONLY public.golden_attempt_stage_groups
    ADD CONSTRAINT golden_attempt_stage_groups_attempt_fk FOREIGN KEY (attempt_id, tournament_id, roster_id) REFERENCES public.golden_attempts(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_attempt_stage_groups
    ADD CONSTRAINT golden_attempt_stage_groups_group_fk FOREIGN KEY (group_revision_id, tournament_id, roster_id) REFERENCES public.golden_group_revisions(revision_id, tournament_id, roster_id) ON DELETE RESTRICT;

CREATE INDEX golden_attempt_stage_groups_group_idx
    ON public.golden_attempt_stage_groups USING btree (tournament_id, roster_id, group_revision_id, attempt_id);

CREATE TRIGGER golden_group_revisions_append_only
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_group_revisions
    FOR EACH ROW EXECUTE FUNCTION public.golden_group_revision_guard();

CREATE CONSTRAINT TRIGGER golden_group_revisions_correction_closure
    AFTER INSERT ON public.golden_group_revisions
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION public.validate_golden_group_revision_progression_open();

CREATE TRIGGER golden_attempt_stage_groups_append_only
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_attempt_stage_groups
    FOR EACH ROW EXECUTE FUNCTION public.golden_attempt_stage_group_guard();

CREATE CONSTRAINT TRIGGER golden_stage_progression_group_coverage
    AFTER INSERT ON public.tournament_stage_progressions
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION public.validate_golden_stage_progression_groups();

-- Exact Golden plans and fallback state must remain reconstructable after the
-- mutable attempt heads advance. These relations deliberately retain only
-- immutable identities, revisions, and digests. Task content stays in the
-- existing immutable task snapshot authority.

CREATE FUNCTION public.golden_exact_plan_snapshot_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden exact plan snapshots are append-only evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.golden_exact_plan_snapshot_child_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    parent_plan_id UUID;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden exact plan authority children are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    parent_plan_id := NEW.plan_id;
    IF EXISTS (
        SELECT 1
        FROM golden_exact_plan_snapshot_seals AS seal
        WHERE seal.plan_id = parent_plan_id
    ) THEN
        RAISE EXCEPTION 'Golden exact plan authority is sealed'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.golden_exact_plan_snapshot_seal_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    plan_proof_hash TEXT;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden exact plan seals are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT proof_hash
    INTO plan_proof_hash
    FROM golden_exact_plan_snapshots
    WHERE plan_id = NEW.plan_id
        AND tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id
    FOR KEY SHARE;

    IF plan_proof_hash IS NULL OR plan_proof_hash <> NEW.proof_hash THEN
        RAISE EXCEPTION 'Golden exact plan seal must bind its exact plan proof'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.golden_state_revision_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    predecessor_revision BIGINT;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden state revisions are append-only evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.revision_number = 1 AND NEW.previous_revision_id IS NOT NULL THEN
        RAISE EXCEPTION 'first Golden state revision cannot have a predecessor'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.revision_number > 1 AND NEW.previous_revision_id IS NULL THEN
        RAISE EXCEPTION 'later Golden state revision requires a predecessor'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.previous_revision_id IS NOT NULL THEN
        SELECT revision_number
        INTO predecessor_revision
        FROM golden_state_revisions
        WHERE revision_id = NEW.previous_revision_id
            AND tournament_id = NEW.tournament_id
            AND roster_id = NEW.roster_id
            AND group_id = NEW.group_id
            AND group_revision_id = NEW.group_revision_id
        FOR KEY SHARE;

        IF NOT FOUND OR predecessor_revision + 1 <> NEW.revision_number THEN
            RAISE EXCEPTION 'Golden state predecessor is not contiguous'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.golden_state_revision_child_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    parent_revision_id UUID;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden state authority children are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    parent_revision_id := NEW.state_revision_id;
    IF EXISTS (
        SELECT 1
        FROM golden_state_revision_seals AS seal
        WHERE seal.state_revision_id = parent_revision_id
    ) THEN
        RAISE EXCEPTION 'Golden state authority is sealed'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.golden_state_revision_seal_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    state_payload_digest BYTEA;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden state seals are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT payload_digest
    INTO state_payload_digest
    FROM golden_state_revisions
    WHERE revision_id = NEW.state_revision_id
        AND tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id
    FOR KEY SHARE;

    IF state_payload_digest IS NULL OR state_payload_digest IS DISTINCT FROM NEW.payload_digest THEN
        RAISE EXCEPTION 'Golden state seal must bind its exact state digest'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.golden_position_ledger_revision_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    predecessor_revision BIGINT;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden position ledger revisions are append-only evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.revision_number = 1 AND NEW.previous_revision_id IS NOT NULL THEN
        RAISE EXCEPTION 'first Golden position ledger revision cannot have a predecessor'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.revision_number > 1 AND NEW.previous_revision_id IS NULL THEN
        RAISE EXCEPTION 'later Golden position ledger revision requires a predecessor'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.previous_revision_id IS NOT NULL THEN
        SELECT revision_number
        INTO predecessor_revision
        FROM golden_position_ledger_revisions
        WHERE revision_id = NEW.previous_revision_id
            AND tournament_id = NEW.tournament_id
            AND roster_id = NEW.roster_id
            AND group_revision_id = NEW.group_revision_id
        FOR KEY SHARE;

        IF NOT FOUND OR predecessor_revision + 1 <> NEW.revision_number THEN
            RAISE EXCEPTION 'Golden position ledger predecessor is not contiguous'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.golden_position_ledger_revision_child_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    parent_revision_id UUID;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden position ledger children are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    parent_revision_id := NEW.ledger_revision_id;
    IF EXISTS (
        SELECT 1
        FROM golden_position_ledger_revision_seals AS seal
        WHERE seal.ledger_revision_id = parent_revision_id
    ) THEN
        RAISE EXCEPTION 'Golden position ledger is sealed'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;

$$;

CREATE FUNCTION public.golden_attempt_submission_revision_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden attempt submission revisions are append-only evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.golden_position_ledger_revision_seal_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    ledger_payload_digest BYTEA;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden position ledger seals are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT payload_digest
    INTO ledger_payload_digest
    FROM golden_position_ledger_revisions
    WHERE revision_id = NEW.ledger_revision_id
        AND tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id
    FOR KEY SHARE;

    IF ledger_payload_digest IS NULL OR ledger_payload_digest IS DISTINCT FROM NEW.payload_digest THEN
        RAISE EXCEPTION 'Golden position ledger seal must bind its exact ledger digest'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.validate_golden_exact_plan_snapshot() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    incomplete_group_count INTEGER;
    candidate_count INTEGER;
    expected_group_count INTEGER;
    observed_group_count INTEGER;
    invalid_group_count INTEGER;
    missing_edge_reservation_count INTEGER;
    missing_member_reservation_count INTEGER;
BEGIN
    SELECT COUNT(*)
    INTO incomplete_group_count
    FROM golden_exact_plan_snapshot_groups AS plan_group
    WHERE plan_group.plan_id = NEW.plan_id
        AND (
            (SELECT COUNT(*)
             FROM golden_exact_plan_snapshot_members AS member
             WHERE member.plan_id = plan_group.plan_id
                 AND member.group_revision_id = plan_group.group_revision_id) < 2
            OR
            (SELECT COUNT(*)
             FROM golden_exact_plan_snapshot_edges AS edge
             WHERE edge.plan_id = plan_group.plan_id
                 AND edge.group_revision_id = plan_group.group_revision_id) <> 3
        );

    SELECT COUNT(*)
    INTO candidate_count
    FROM golden_exact_plan_snapshot_candidates AS candidate
    WHERE candidate.plan_id = NEW.plan_id;

    SELECT COUNT(*)
    INTO expected_group_count
    FROM golden_group_revisions AS group_revision
    WHERE group_revision.tournament_id = NEW.tournament_id
        AND group_revision.roster_id = NEW.roster_id
        AND group_revision.source_projection_revision_id = NEW.source_projection_revision_id
        AND group_revision.source_projection_revision = NEW.source_projection_revision;

    SELECT COUNT(*)
    INTO observed_group_count
    FROM golden_exact_plan_snapshot_groups AS plan_group
    WHERE plan_group.plan_id = NEW.plan_id
        AND plan_group.tournament_id = NEW.tournament_id
        AND plan_group.roster_id = NEW.roster_id;

    SELECT COUNT(*)
    INTO invalid_group_count
    FROM golden_exact_plan_snapshot_groups AS plan_group
    INNER JOIN golden_group_revisions AS group_revision
        ON group_revision.revision_id = plan_group.group_revision_id
        AND group_revision.tournament_id = plan_group.tournament_id
        AND group_revision.roster_id = plan_group.roster_id
    INNER JOIN tournament_stage_tie_groups AS tie_group
        ON tie_group.command_id = group_revision.stage_progression_command_id
        AND tie_group.tournament_id = group_revision.tournament_id
        AND tie_group.roster_id = group_revision.roster_id
        AND tie_group.group_id = group_revision.group_id
        AND tie_group.group_revision_id = group_revision.revision_id
    WHERE plan_group.plan_id = NEW.plan_id
        AND (
            plan_group.source_projection_revision_id <> NEW.source_projection_revision_id
            OR plan_group.source_projection_revision <> NEW.source_projection_revision
            OR group_revision.source_projection_revision_id <> NEW.source_projection_revision_id
            OR group_revision.source_projection_revision <> NEW.source_projection_revision
            OR plan_group.position_from <> tie_group.position_from
            OR plan_group.position_to <> tie_group.position_to
            OR plan_group.definition_digest IS DISTINCT FROM group_revision.definition_digest
            OR (
                SELECT COUNT(*)
                FROM golden_exact_plan_snapshot_members AS member
                WHERE member.plan_id = plan_group.plan_id
                    AND member.group_revision_id = plan_group.group_revision_id
            ) <> plan_group.position_to - plan_group.position_from + 1
            OR EXISTS (
                SELECT 1
                FROM generate_series(
                    plan_group.position_from::integer,
                    plan_group.position_to::integer
                ) AS expected(position)
                WHERE NOT EXISTS (
                    SELECT 1
                    FROM golden_exact_plan_snapshot_members AS member
                    WHERE member.plan_id = plan_group.plan_id
                        AND member.group_revision_id = plan_group.group_revision_id
                        AND member.position = expected.position
                )
            )
            OR EXISTS (
                SELECT 1
                FROM golden_exact_plan_snapshot_members AS member
                FULL OUTER JOIN tournament_stage_tie_group_members AS source_member
                    ON source_member.command_id = tie_group.command_id
                    AND source_member.tournament_id = tie_group.tournament_id
                    AND source_member.group_id = tie_group.group_id
                    AND source_member.participant_id = member.participant_id
                    AND source_member.standing_position = member.position
                WHERE member.plan_id = plan_group.plan_id
                    AND member.group_revision_id = plan_group.group_revision_id
                    AND source_member.participant_id IS NULL
            )
            OR EXISTS (
                SELECT 1
                FROM tournament_stage_tie_group_members AS source_member
                WHERE source_member.command_id = tie_group.command_id
                    AND source_member.tournament_id = tie_group.tournament_id
                    AND source_member.group_id = tie_group.group_id
                    AND NOT EXISTS (
                        SELECT 1
                        FROM golden_exact_plan_snapshot_members AS member
                        WHERE member.plan_id = plan_group.plan_id
                            AND member.group_revision_id = plan_group.group_revision_id
                            AND member.participant_id = source_member.participant_id
                            AND member.position = source_member.standing_position
                    )
            )
            OR (
                SELECT COUNT(*)
                FROM golden_exact_plan_snapshot_edges AS edge
                WHERE edge.plan_id = plan_group.plan_id
                    AND edge.group_revision_id = plan_group.group_revision_id
            ) <> 3
            OR EXISTS (
                SELECT 1
                FROM generate_series(1, 3) AS expected(position)
                WHERE NOT EXISTS (
                    SELECT 1
                    FROM golden_exact_plan_snapshot_edges AS edge
                    WHERE edge.plan_id = plan_group.plan_id
                        AND edge.group_revision_id = plan_group.group_revision_id
                        AND edge.position = expected.position
                )
            )
        );

    SELECT COUNT(*)
    INTO missing_edge_reservation_count
    FROM golden_exact_plan_snapshot_edges AS edge
    WHERE edge.plan_id = NEW.plan_id
        AND NOT EXISTS (
            SELECT 1
            FROM golden_exact_plan_snapshot_reservations AS reservation
            WHERE reservation.plan_id = edge.plan_id
                AND reservation.tournament_id = edge.tournament_id
                AND reservation.roster_id = edge.roster_id
                AND reservation.reservation_id = edge.reservation_id
                AND reservation.task_id = edge.task_id
                AND reservation.task_version = edge.task_version
        );

    SELECT COUNT(*)
    INTO missing_member_reservation_count
    FROM golden_exact_plan_snapshot_members AS member
    WHERE member.plan_id = NEW.plan_id
        AND NOT EXISTS (
            SELECT 1
            FROM golden_exact_plan_snapshot_participant_reservations AS reservation
            WHERE reservation.plan_id = member.plan_id
                AND reservation.tournament_id = member.tournament_id
                AND reservation.roster_id = member.roster_id
                AND reservation.participant_id = member.participant_id
        );

    IF NOT EXISTS (
        SELECT 1
        FROM golden_exact_plan_snapshot_seals AS seal
        WHERE seal.plan_id = NEW.plan_id
            AND seal.tournament_id = NEW.tournament_id
            AND seal.roster_id = NEW.roster_id
    ) OR incomplete_group_count <> 0
        OR expected_group_count < 1
        OR observed_group_count <> expected_group_count
        OR invalid_group_count <> 0
        OR missing_edge_reservation_count <> 0
        OR missing_member_reservation_count <> 0
        OR candidate_count < 3 THEN
        RAISE EXCEPTION 'Golden exact plan snapshot is missing canonical authority children'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.validate_golden_state_revision() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    transition_kind VARCHAR(24);
    transition_command_id UUID;
    transition_previous_revision_id UUID;
    exact_group_count INTEGER;
    member_count INTEGER;
    child_count INTEGER;
BEGIN
    SELECT COUNT(*)
    INTO exact_group_count
    FROM golden_exact_plan_snapshot_groups AS plan_group
    WHERE plan_group.plan_id = NEW.plan_id
        AND plan_group.group_id = NEW.group_id
        AND plan_group.group_revision_id = NEW.group_revision_id
        AND plan_group.tournament_id = NEW.tournament_id
        AND plan_group.roster_id = NEW.roster_id;

    SELECT COUNT(*)
    INTO member_count
    FROM golden_state_members AS member
    WHERE member.state_revision_id = NEW.revision_id
        AND member.tournament_id = NEW.tournament_id
        AND member.roster_id = NEW.roster_id;

    SELECT transition.transition_kind,
        transition.command_id,
        transition.previous_state_revision_id
    INTO transition_kind, transition_command_id, transition_previous_revision_id
    FROM golden_state_transitions AS transition
    WHERE transition.state_revision_id = NEW.revision_id
        AND transition.tournament_id = NEW.tournament_id
        AND transition.roster_id = NEW.roster_id;

    IF exact_group_count <> 1 OR member_count < 2 OR NOT FOUND
        OR transition_previous_revision_id IS DISTINCT FROM NEW.previous_revision_id
        OR NOT EXISTS (
            SELECT 1
            FROM golden_state_revision_seals AS seal
            WHERE seal.state_revision_id = NEW.revision_id
                AND seal.tournament_id = NEW.tournament_id
                AND seal.roster_id = NEW.roster_id
        ) THEN
        RAISE EXCEPTION 'Golden state revision is missing exact plan, membership, or transition authority'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.revision_number = 1 THEN
        IF transition_kind <> 'initial' OR transition_command_id IS NOT NULL THEN
            RAISE EXCEPTION 'first Golden state revision requires the initial transition'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF transition_kind = 'ready' THEN
        SELECT COUNT(*) INTO child_count
        FROM golden_state_ready_events AS event
        WHERE event.state_revision_id = NEW.revision_id
            AND event.command_id = transition_command_id;
        IF child_count <> 1 THEN
            RAISE EXCEPTION 'Golden ready transition lacks its exact event'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF transition_kind = 'no_show' THEN
        SELECT COUNT(*) INTO child_count
        FROM golden_state_no_show_resolutions AS resolution
        WHERE resolution.state_revision_id = NEW.revision_id
            AND resolution.command_id = transition_command_id;
        IF child_count <> 1 THEN
            RAISE EXCEPTION 'Golden no-show transition lacks its exact resolution'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF transition_kind = 'allocation' THEN
        SELECT COUNT(*) INTO child_count
        FROM golden_state_allocations AS allocation
        WHERE allocation.state_revision_id = NEW.revision_id
            AND allocation.command_id = transition_command_id;
        IF child_count <> 1 THEN
            RAISE EXCEPTION 'Golden allocation transition lacks its exact allocation'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSE
        RAISE EXCEPTION 'Golden state transition kind is invalid for its revision'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.validate_golden_position_ledger_revision() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    incomplete_attempt_count INTEGER;
BEGIN
    SELECT COUNT(*)
    INTO incomplete_attempt_count
    FROM golden_position_ledger_attempts AS attempt
    WHERE attempt.ledger_revision_id = NEW.revision_id
        AND (
            SELECT COUNT(*)
            FROM golden_position_ledger_commit_bindings AS binding
            WHERE binding.ledger_revision_id = attempt.ledger_revision_id
                AND binding.attempt_id = attempt.attempt_id
        ) <> attempt.order_count;

    IF NOT EXISTS (
        SELECT 1
        FROM golden_position_ledger_attempts AS attempt
        WHERE attempt.ledger_revision_id = NEW.revision_id
    ) OR incomplete_attempt_count <> 0
        OR NOT EXISTS (
            SELECT 1
            FROM golden_position_ledger_revision_seals AS seal
            WHERE seal.ledger_revision_id = NEW.revision_id
                AND seal.tournament_id = NEW.tournament_id
                AND seal.roster_id = NEW.roster_id
        ) THEN
        RAISE EXCEPTION 'Golden position ledger lacks complete immutable attempt evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.golden_attempt_authority_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    exact_binding_count INTEGER;
    exact_group_binding_count INTEGER;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden attempt authorities are append-only evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*)
    INTO exact_group_binding_count
    FROM golden_attempt_stage_groups AS attempt_group
    WHERE attempt_group.attempt_id = NEW.attempt_id
        AND attempt_group.tournament_id = NEW.tournament_id
        AND attempt_group.roster_id = NEW.roster_id
        AND attempt_group.group_revision_id = NEW.group_revision_id;

    IF exact_group_binding_count <> 1 THEN
        RAISE EXCEPTION 'Golden attempt authority must bind its exact Golden group revision'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*)
    INTO exact_binding_count
    FROM waves AS wave
    INNER JOIN wave_series AS wave_series
        ON wave_series.wave_id = wave.id
        AND wave_series.tournament_id = wave.tournament_id
        AND wave_series.roster_id = wave.roster_id
    INNER JOIN game_attempts AS game_attempt
        ON game_attempt.series_id = wave_series.series_id
        AND game_attempt.roster_id = wave_series.roster_id
    INNER JOIN assignments AS assignment
        ON assignment.id = NEW.assignment_id
        AND assignment.attempt_id = game_attempt.id
        AND assignment.series_id = game_attempt.series_id
        AND assignment.roster_id = game_attempt.roster_id
        AND assignment.snapshot_id = NEW.snapshot_id
        AND assignment.task_id = NEW.task_id
        AND assignment.task_version = NEW.task_version
    WHERE wave.id = NEW.wave_id
        AND wave.tournament_id = NEW.tournament_id
        AND wave.roster_id = NEW.roster_id;

    IF exact_binding_count <> 1 THEN
        RAISE EXCEPTION 'Golden attempt authority must bind one exact scoped Wave assignment snapshot'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TABLE public.golden_exact_plan_snapshots (
    plan_id uuid NOT NULL,
    plan_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    plan_set_id uuid NOT NULL,
    source_projection_revision_id uuid NOT NULL,
    source_projection_revision bigint NOT NULL,
    source_projection_previous_revision_id uuid,
    source_standings_artifact_id uuid NOT NULL,
    source_standings_payload_digest bytea NOT NULL,
    group_set_revision_id uuid NOT NULL,
    group_set_revision bigint NOT NULL,
    pool_revision_id uuid NOT NULL,
    pool_revision bigint NOT NULL,
    history_revision_id uuid NOT NULL,
    history_revision bigint NOT NULL,
    task_health_revision_id uuid NOT NULL,
    task_health_revision bigint NOT NULL,
    artifact_revision_id uuid NOT NULL,
    artifact_revision bigint NOT NULL,
    reservation_revision_id uuid NOT NULL,
    reservation_revision bigint NOT NULL,
    membership_revision_id uuid NOT NULL,
    membership_revision bigint NOT NULL,
    source_payload_digest bytea NOT NULL,
    group_digest bytea NOT NULL,
    pool_digest bytea NOT NULL,
    history_digest bytea NOT NULL,
    task_health_digest bytea NOT NULL,
    artifact_digest bytea NOT NULL,
    reservation_digest bytea NOT NULL,
    membership_digest bytea NOT NULL,
    proof_hash text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_exact_plan_snapshots_ids_check CHECK (
        plan_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND plan_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND tournament_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND roster_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND plan_set_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND source_projection_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND source_standings_artifact_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND group_set_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND pool_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND history_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND task_health_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND artifact_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND reservation_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND membership_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT golden_exact_plan_snapshots_revision_check CHECK (
        source_projection_revision >= 1
        AND group_set_revision >= 1
        AND pool_revision >= 1
        AND history_revision >= 1
        AND task_health_revision >= 1
        AND artifact_revision >= 1
        AND reservation_revision >= 1
        AND membership_revision >= 1
    ),
    CONSTRAINT golden_exact_plan_snapshots_digest_check CHECK (
        octet_length(source_standings_payload_digest) = 32
        AND octet_length(source_payload_digest) = 32
        AND octet_length(group_digest) = 32
        AND octet_length(pool_digest) = 32
        AND octet_length(history_digest) = 32
        AND octet_length(task_health_digest) = 32
        AND octet_length(artifact_digest) = 32
        AND octet_length(reservation_digest) = 32
        AND octet_length(membership_digest) = 32
        AND source_standings_payload_digest <> decode(repeat('00', 32), 'hex')
        AND source_payload_digest <> decode(repeat('00', 32), 'hex')
        AND group_digest <> decode(repeat('00', 32), 'hex')
        AND pool_digest <> decode(repeat('00', 32), 'hex')
        AND history_digest <> decode(repeat('00', 32), 'hex')
        AND task_health_digest <> decode(repeat('00', 32), 'hex')
        AND artifact_digest <> decode(repeat('00', 32), 'hex')
        AND reservation_digest <> decode(repeat('00', 32), 'hex')
        AND membership_digest <> decode(repeat('00', 32), 'hex')
    ),
    CONSTRAINT golden_exact_plan_snapshots_proof_check CHECK (
        proof_hash = btrim(proof_hash) AND proof_hash <> ''
    )
);

CREATE TABLE public.golden_exact_plan_snapshot_groups (
    plan_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    group_id uuid NOT NULL,
    group_revision_id uuid NOT NULL,
    source_projection_revision_id uuid NOT NULL,
    source_projection_revision bigint NOT NULL,
    position_from smallint NOT NULL,
    position_to smallint NOT NULL,
    group_ordinal smallint NOT NULL,
    definition_digest bytea NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_exact_plan_snapshot_groups_position_check CHECK (
        group_ordinal >= 1
        AND position_from >= 1
        AND position_to >= position_from
        AND position_to <= 16
        AND source_projection_revision >= 1
    ),
    CONSTRAINT golden_exact_plan_snapshot_groups_ids_check CHECK (
        group_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND group_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND source_projection_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND octet_length(definition_digest) = 32
        AND definition_digest <> decode(repeat('00', 32), 'hex')
    )
);

CREATE TABLE public.golden_exact_plan_snapshot_members (
    plan_id uuid NOT NULL,
    group_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    position smallint NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_exact_plan_snapshot_members_position_check CHECK (position >= 1 AND position <= 16)
);

CREATE TABLE public.golden_exact_plan_snapshot_edges (
    plan_id uuid NOT NULL,
    group_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    edge_id uuid NOT NULL,
    reservation_id uuid NOT NULL,
    snapshot_id uuid NOT NULL,
    task_id uuid NOT NULL,
    task_version integer NOT NULL,
    position smallint NOT NULL,
    content_digest bytea NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_exact_plan_snapshot_edges_check CHECK (
        position >= 1 AND position <= 3
        AND task_version >= 1
        AND edge_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND reservation_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND snapshot_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND task_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND octet_length(content_digest) = 32
        AND content_digest <> decode(repeat('00', 32), 'hex')
    )
);

CREATE TABLE public.golden_exact_plan_snapshot_candidates (
    plan_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    pool_revision_id uuid NOT NULL,
    task_id uuid NOT NULL,
    task_version integer NOT NULL,
    exists_in_source boolean NOT NULL,
    enabled boolean NOT NULL,
    healthy boolean NOT NULL,
    mutation_locked boolean NOT NULL,
    publicly_exposed boolean NOT NULL,
    artifact_digest bytea NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_exact_plan_snapshot_candidates_check CHECK (
        task_version >= 1
        AND pool_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND task_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND octet_length(artifact_digest) = 32
        AND artifact_digest <> decode(repeat('00', 32), 'hex')
    )
);

CREATE TABLE public.golden_exact_plan_snapshot_history (
    plan_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    task_id uuid NOT NULL,
    task_version integer NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_exact_plan_snapshot_history_check CHECK (task_version >= 1)
);

CREATE TABLE public.golden_exact_plan_snapshot_participant_reservations (
    plan_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    player_id uuid NOT NULL,
    reservation_id uuid NOT NULL,
    revision bigint NOT NULL,
    acquired_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_exact_plan_snapshot_participant_reservations_check CHECK (
        revision >= 1 AND updated_at >= acquired_at
    )
);

CREATE TABLE public.golden_exact_plan_snapshot_reservations (
    plan_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    task_id uuid NOT NULL,
    task_version integer NOT NULL,
    reservation_id uuid NOT NULL,
    owner_plan_id uuid NOT NULL,
    owner_plan_revision_id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_exact_plan_snapshot_reservations_check CHECK (task_version >= 1)
);

CREATE TABLE public.golden_exact_plan_snapshot_seals (
    plan_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    proof_hash text NOT NULL,
    sealed_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_exact_plan_snapshot_seals_check CHECK (
        proof_hash = btrim(proof_hash) AND proof_hash <> ''
    )
);

CREATE TABLE public.golden_state_revisions (
    revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    group_id uuid NOT NULL,
    group_revision_id uuid NOT NULL,
    revision_number bigint NOT NULL,
    previous_revision_id uuid,
    plan_id uuid NOT NULL,
    membership_revision_id uuid NOT NULL,
    membership_revision bigint NOT NULL,
    membership_previous_revision_id uuid,
    membership_digest bytea NOT NULL,
    payload_digest bytea NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_state_revisions_number_check CHECK (
        revision_number >= 1
        AND membership_revision >= 1
        AND ((revision_number = 1 AND previous_revision_id IS NULL) OR (revision_number > 1 AND previous_revision_id IS NOT NULL))
    ),
    CONSTRAINT golden_state_revisions_digest_check CHECK (
        octet_length(membership_digest) = 32
        AND octet_length(payload_digest) = 32
        AND membership_digest <> decode(repeat('00', 32), 'hex')
        AND payload_digest <> decode(repeat('00', 32), 'hex')
    )
);

CREATE TABLE public.golden_state_transitions (
    state_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    group_id uuid NOT NULL,
    group_revision_id uuid NOT NULL,
    transition_kind character varying(24) NOT NULL,
    command_id uuid,
    previous_state_revision_id uuid,
    occurred_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_state_transitions_kind_check CHECK (
        (transition_kind = 'initial' AND command_id IS NULL AND previous_state_revision_id IS NULL)
        OR (transition_kind IN ('ready', 'no_show', 'allocation') AND command_id IS NOT NULL AND previous_state_revision_id IS NOT NULL)
    )
);

CREATE TABLE public.golden_state_members (
    state_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    excluded boolean NOT NULL,
    position smallint NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_state_members_position_check CHECK (position >= 1 AND position <= 16)
);

CREATE TABLE public.golden_state_attempts (
    state_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    group_revision_id uuid NOT NULL,
    attempt_id uuid NOT NULL,
    attempt_number integer NOT NULL,
    previous_attempt_id uuid,
    state character varying(24) NOT NULL,
    retained_at timestamp with time zone,
    started_at timestamp with time zone,
    finished_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_state_attempts_number_check CHECK (attempt_number >= 1),
    CONSTRAINT golden_state_attempts_state_check CHECK (
        state IN ('planned', 'waiting_ready', 'active', 'completed', 'void', 'cancelled')
    )
);

CREATE TABLE public.golden_state_attempt_members (
    state_revision_id uuid NOT NULL,
    attempt_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    position smallint NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_state_attempt_members_position_check CHECK (position >= 1 AND position <= 16)
);

CREATE TABLE public.golden_state_ready_windows (
    state_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    window_id uuid NOT NULL,
    window_revision_id uuid NOT NULL,
    window_revision bigint NOT NULL,
    window_previous_revision_id uuid,
    attempt_id uuid NOT NULL,
    attempt_number integer NOT NULL,
    opened_at timestamp with time zone NOT NULL,
    deadline timestamp with time zone NOT NULL,
    state character varying(24) NOT NULL,
    readiness_revision_id uuid NOT NULL,
    readiness_revision bigint NOT NULL,
    readiness_previous_revision_id uuid,
    readiness_digest bytea NOT NULL,
    presence_revision_id uuid NOT NULL,
    presence_revision bigint NOT NULL,
    presence_previous_revision_id uuid,
    presence_digest bytea NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_state_ready_windows_check CHECK (
        window_revision >= 1
        AND readiness_revision >= 1
        AND presence_revision >= 1
        AND attempt_number >= 1
        AND deadline >= opened_at
        AND state IN ('open', 'expired')
        AND octet_length(readiness_digest) = 32
        AND octet_length(presence_digest) = 32
        AND readiness_digest <> decode(repeat('00', 32), 'hex')
        AND presence_digest <> decode(repeat('00', 32), 'hex')
    )
);

CREATE TABLE public.golden_state_ready_window_participants (
    state_revision_id uuid NOT NULL,
    window_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    membership_kind character varying(16) NOT NULL,
    position smallint NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_state_ready_window_participants_check CHECK (
        membership_kind IN ('ready', 'base_present', 'present')
        AND position >= 1 AND position <= 16
    )
);

CREATE TABLE public.golden_state_ready_events (
    command_id uuid NOT NULL,
    state_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    group_id uuid NOT NULL,
    group_revision_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    event_type character varying(24) NOT NULL,
    attempt_id uuid NOT NULL,
    window_id uuid NOT NULL,
    expected_state_revision_id uuid NOT NULL,
    expected_state_revision bigint NOT NULL,
    expected_state_payload_digest bytea NOT NULL,
    expected_window_revision_id uuid NOT NULL,
    expected_window_revision bigint NOT NULL,
    expected_readiness_revision_id uuid NOT NULL,
    expected_readiness_revision bigint NOT NULL,
    expected_readiness_digest bytea NOT NULL,
    expected_presence_revision_id uuid NOT NULL,
    expected_presence_revision bigint NOT NULL,
    expected_presence_digest bytea NOT NULL,
    command_digest bytea NOT NULL,
    result_window_revision_id uuid NOT NULL,
    result_readiness_revision_id uuid NOT NULL,
    result_presence_revision_id uuid NOT NULL,
    occurred_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_state_ready_events_check CHECK (
        event_type IN ('accepted', 'disconnected', 'already_ready', 'already_absent')
        AND expected_state_revision >= 1
        AND expected_window_revision >= 1
        AND expected_readiness_revision >= 1
        AND expected_presence_revision >= 1
        AND octet_length(expected_state_payload_digest) = 32
        AND octet_length(expected_readiness_digest) = 32
        AND octet_length(expected_presence_digest) = 32
        AND octet_length(command_digest) = 32
    )
);

CREATE TABLE public.golden_state_no_show_resolutions (
    command_id uuid NOT NULL,
    state_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    group_id uuid NOT NULL,
    group_revision_id uuid NOT NULL,
    attempt_id uuid NOT NULL,
    attempt_number integer NOT NULL,
    window_id uuid NOT NULL,
    expected_state_revision_id uuid NOT NULL,
    expected_state_revision bigint NOT NULL,
    expected_state_payload_digest bytea NOT NULL,
    expected_window_revision_id uuid NOT NULL,
    expected_window_revision bigint NOT NULL,
    expected_readiness_revision_id uuid NOT NULL,
    expected_readiness_revision bigint NOT NULL,
    expected_readiness_digest bytea NOT NULL,
    expected_presence_revision_id uuid NOT NULL,
    expected_presence_revision bigint NOT NULL,
    expected_presence_digest bytea NOT NULL,
    result_window_revision_id uuid NOT NULL,
    result_membership_revision_id uuid NOT NULL,
    deadline timestamp with time zone NOT NULL,
    resolved_at timestamp with time zone NOT NULL,
    readiness_revision_id uuid NOT NULL,
    readiness_revision bigint NOT NULL,
    readiness_digest bytea NOT NULL,
    presence_revision_id uuid NOT NULL,
    presence_revision bigint NOT NULL,
    presence_digest bytea NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_state_no_show_resolutions_check CHECK (
        attempt_number >= 1
        AND expected_state_revision >= 1
        AND expected_window_revision >= 1
        AND expected_readiness_revision >= 1
        AND expected_presence_revision >= 1
        AND readiness_revision >= 1
        AND presence_revision >= 1
        AND resolved_at >= deadline
        AND octet_length(expected_state_payload_digest) = 32
        AND octet_length(expected_readiness_digest) = 32
        AND octet_length(expected_presence_digest) = 32
        AND octet_length(readiness_digest) = 32
        AND octet_length(presence_digest) = 32
    )
);

CREATE TABLE public.golden_state_no_show_participants (
    state_revision_id uuid NOT NULL,
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    membership_kind character varying(16) NOT NULL,
    position smallint NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_state_no_show_participants_check CHECK (
        membership_kind IN ('ready', 'present', 'excluded')
        AND position >= 1 AND position <= 16
    )
);

CREATE TABLE public.golden_state_allocations (
    allocation_id uuid NOT NULL,
    command_id uuid NOT NULL,
    state_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    group_id uuid NOT NULL,
    group_revision_id uuid NOT NULL,
    expected_state_revision_id uuid NOT NULL,
    expected_state_revision bigint NOT NULL,
    expected_state_payload_digest bytea NOT NULL,
    allocated_at timestamp with time zone NOT NULL,
    payload_digest bytea NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_state_allocations_check CHECK (
        expected_state_revision >= 1
        AND octet_length(expected_state_payload_digest) = 32
        AND octet_length(payload_digest) = 32
        AND expected_state_payload_digest <> decode(repeat('00', 32), 'hex')
        AND payload_digest <> decode(repeat('00', 32), 'hex')
    )
);

CREATE TABLE public.golden_state_allocation_inputs (
    allocation_id uuid NOT NULL,
    state_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    points integer NOT NULL,
    buchholz integer NOT NULL,
    head_to_head_points integer NOT NULL,
    head_to_head_applied boolean NOT NULL,
    effective_time_milliseconds bigint NOT NULL,
    accepted_solve_time_milliseconds bigint,
    stable_seed integer NOT NULL,
    position smallint NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_state_allocation_inputs_check CHECK (
        points >= 0 AND buchholz >= 0 AND head_to_head_points >= 0
        AND effective_time_milliseconds >= 0
        AND (accepted_solve_time_milliseconds IS NULL OR accepted_solve_time_milliseconds >= 0)
        AND stable_seed >= 1
        AND position >= 1 AND position <= 16
    )
);

CREATE TABLE public.golden_state_allocation_positions (
    allocation_id uuid NOT NULL,
    state_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    position smallint NOT NULL,
    participant_id uuid NOT NULL,
    position_kind character varying(24) NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_state_allocation_positions_check CHECK (
        position >= 1 AND position <= 16
        AND position_kind IN ('direct', 'no_show_fallback')
    )
);

CREATE TABLE public.golden_state_revision_seals (
    state_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    payload_digest bytea NOT NULL,
    sealed_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_state_revision_seals_digest_check CHECK (
        octet_length(payload_digest) = 32
        AND payload_digest <> decode(repeat('00', 32), 'hex')
    )
);

CREATE TABLE public.golden_attempt_authorities (
    attempt_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    group_revision_id uuid NOT NULL,
    wave_id uuid NOT NULL,
    assignment_id uuid NOT NULL,
    snapshot_id uuid NOT NULL,
    task_id uuid NOT NULL,
    task_version integer NOT NULL,
    source_digest bytea NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_attempt_authorities_check CHECK (
        task_version >= 1
        AND octet_length(source_digest) = 32
        AND source_digest <> decode(repeat('00', 32), 'hex')
    )
);

CREATE TABLE public.golden_attempt_submission_revisions (
    revision_id uuid NOT NULL,
    attempt_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    membership_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    revision_number bigint NOT NULL,
    previous_revision_id uuid,
    provisional_submission_id uuid NOT NULL,
    payload_digest bytea NOT NULL,
    committed_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_attempt_submission_revisions_check CHECK (
        revision_number >= 1
        AND ((revision_number = 1 AND previous_revision_id IS NULL) OR (revision_number > 1 AND previous_revision_id IS NOT NULL))
        AND octet_length(payload_digest) = 32
        AND payload_digest <> decode(repeat('00', 32), 'hex')
    )
);

CREATE TABLE public.golden_position_ledger_revisions (
    revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    group_revision_id uuid NOT NULL,
    revision_number bigint NOT NULL,
    previous_revision_id uuid,
    payload_digest bytea NOT NULL,
    finalized_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_position_ledger_revisions_check CHECK (
        revision_number >= 1
        AND ((revision_number = 1 AND previous_revision_id IS NULL) OR (revision_number > 1 AND previous_revision_id IS NOT NULL))
        AND octet_length(payload_digest) = 32
        AND payload_digest <> decode(repeat('00', 32), 'hex')
    )
);

CREATE TABLE public.golden_position_ledger_attempts (
    ledger_revision_id uuid NOT NULL,
    attempt_id uuid NOT NULL,
    submission_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    group_revision_id uuid NOT NULL,
    attempt_number integer NOT NULL,
    order_count smallint NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_position_ledger_attempts_check CHECK (attempt_number >= 1 AND order_count >= 1 AND order_count <= 16)
);

CREATE TABLE public.golden_position_ledger_commit_bindings (
    ledger_revision_id uuid NOT NULL,
    attempt_id uuid NOT NULL,
    position_commit_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    position smallint NOT NULL,
    evidence_digest bytea NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_position_ledger_commit_bindings_check CHECK (
        position >= 1 AND position <= 16
        AND octet_length(evidence_digest) = 32
        AND evidence_digest <> decode(repeat('00', 32), 'hex')
    )
);

CREATE TABLE public.golden_position_ledger_revision_seals (
    ledger_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    payload_digest bytea NOT NULL,
    sealed_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_position_ledger_revision_seals_digest_check CHECK (
        octet_length(payload_digest) = 32
        AND payload_digest <> decode(repeat('00', 32), 'hex')
    )
);

ALTER TABLE ONLY public.golden_exact_plan_snapshots
    ADD CONSTRAINT golden_exact_plan_snapshots_pkey PRIMARY KEY (plan_id);
ALTER TABLE ONLY public.golden_exact_plan_snapshots
    ADD CONSTRAINT golden_exact_plan_snapshots_scope_key UNIQUE (plan_id, tournament_id, roster_id);
ALTER TABLE ONLY public.golden_exact_plan_snapshots
    ADD CONSTRAINT golden_exact_plan_snapshots_plan_revision_key UNIQUE (plan_revision_id);
ALTER TABLE ONLY public.golden_exact_plan_snapshots
    ADD CONSTRAINT golden_exact_plan_snapshots_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_exact_plan_snapshot_groups
    ADD CONSTRAINT golden_exact_plan_snapshot_groups_pkey PRIMARY KEY (plan_id, group_revision_id);
ALTER TABLE ONLY public.golden_exact_plan_snapshot_groups
    ADD CONSTRAINT golden_exact_plan_snapshot_groups_ordinal_key UNIQUE (plan_id, group_ordinal);
ALTER TABLE ONLY public.golden_exact_plan_snapshot_groups
    ADD CONSTRAINT golden_exact_plan_snapshot_groups_group_key UNIQUE (plan_id, group_id);
ALTER TABLE ONLY public.golden_exact_plan_snapshot_groups
    ADD CONSTRAINT golden_exact_plan_snapshot_groups_scope_key UNIQUE (plan_id, group_revision_id, tournament_id, roster_id);
ALTER TABLE ONLY public.golden_exact_plan_snapshot_groups
    ADD CONSTRAINT golden_exact_plan_snapshot_groups_plan_fk FOREIGN KEY (plan_id, tournament_id, roster_id) REFERENCES public.golden_exact_plan_snapshots(plan_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_exact_plan_snapshot_groups
    ADD CONSTRAINT golden_exact_plan_snapshot_groups_group_fk FOREIGN KEY (group_revision_id, tournament_id, roster_id) REFERENCES public.golden_group_revisions(revision_id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_exact_plan_snapshot_members
    ADD CONSTRAINT golden_exact_plan_snapshot_members_pkey PRIMARY KEY (plan_id, group_revision_id, participant_id);
ALTER TABLE ONLY public.golden_exact_plan_snapshot_members
    ADD CONSTRAINT golden_exact_plan_snapshot_members_position_key UNIQUE (plan_id, group_revision_id, position);
ALTER TABLE ONLY public.golden_exact_plan_snapshot_members
    ADD CONSTRAINT golden_exact_plan_snapshot_members_group_fk FOREIGN KEY (plan_id, group_revision_id, tournament_id, roster_id) REFERENCES public.golden_exact_plan_snapshot_groups(plan_id, group_revision_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_exact_plan_snapshot_members
    ADD CONSTRAINT golden_exact_plan_snapshot_members_participant_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_exact_plan_snapshot_edges
    ADD CONSTRAINT golden_exact_plan_snapshot_edges_pkey PRIMARY KEY (plan_id, edge_id);
ALTER TABLE ONLY public.golden_exact_plan_snapshot_edges
    ADD CONSTRAINT golden_exact_plan_snapshot_edges_position_key UNIQUE (plan_id, group_revision_id, position);
ALTER TABLE ONLY public.golden_exact_plan_snapshot_edges
    ADD CONSTRAINT golden_exact_plan_snapshot_edges_group_fk FOREIGN KEY (plan_id, group_revision_id, tournament_id, roster_id) REFERENCES public.golden_exact_plan_snapshot_groups(plan_id, group_revision_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_exact_plan_snapshot_edges
    ADD CONSTRAINT golden_exact_plan_snapshot_edges_snapshot_fk FOREIGN KEY (snapshot_id, reservation_id, task_id, task_version) REFERENCES public.task_snapshots(id, reservation_id, task_id, task_version) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_exact_plan_snapshot_edges
    ADD CONSTRAINT golden_exact_plan_snapshot_edges_reservation_fk FOREIGN KEY (reservation_id, task_id, task_version) REFERENCES public.task_version_reservations(id, task_id, task_version) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_exact_plan_snapshot_candidates
    ADD CONSTRAINT golden_exact_plan_snapshot_candidates_pkey PRIMARY KEY (plan_id, task_id, task_version);
ALTER TABLE ONLY public.golden_exact_plan_snapshot_candidates
    ADD CONSTRAINT golden_exact_plan_snapshot_candidates_plan_fk FOREIGN KEY (plan_id, tournament_id, roster_id) REFERENCES public.golden_exact_plan_snapshots(plan_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_exact_plan_snapshot_candidates
    ADD CONSTRAINT golden_exact_plan_snapshot_candidates_task_fk FOREIGN KEY (task_id, task_version) REFERENCES public.task_versions(task_id, version) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_exact_plan_snapshot_history
    ADD CONSTRAINT golden_exact_plan_snapshot_history_pkey PRIMARY KEY (plan_id, participant_id, task_id, task_version);
ALTER TABLE ONLY public.golden_exact_plan_snapshot_history
    ADD CONSTRAINT golden_exact_plan_snapshot_history_plan_fk FOREIGN KEY (plan_id, tournament_id, roster_id) REFERENCES public.golden_exact_plan_snapshots(plan_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_exact_plan_snapshot_history
    ADD CONSTRAINT golden_exact_plan_snapshot_history_participant_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_exact_plan_snapshot_history
    ADD CONSTRAINT golden_exact_plan_snapshot_history_task_fk FOREIGN KEY (task_id, task_version) REFERENCES public.task_versions(task_id, version) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_exact_plan_snapshot_participant_reservations
    ADD CONSTRAINT golden_exact_plan_snapshot_participant_reservations_pkey PRIMARY KEY (plan_id, participant_id);
ALTER TABLE ONLY public.golden_exact_plan_snapshot_participant_reservations
    ADD CONSTRAINT golden_exact_plan_snapshot_participant_reservations_plan_fk FOREIGN KEY (plan_id, tournament_id, roster_id) REFERENCES public.golden_exact_plan_snapshots(plan_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_exact_plan_snapshot_participant_reservations
    ADD CONSTRAINT golden_plan_snapshot_participant_reservation_part_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_exact_plan_snapshot_participant_reservations
    ADD CONSTRAINT golden_plan_snapshot_participant_reservation_lock_fk FOREIGN KEY (player_id) REFERENCES public.participant_reservations(player_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_exact_plan_snapshot_reservations
    ADD CONSTRAINT golden_exact_plan_snapshot_reservations_pkey PRIMARY KEY (plan_id, task_id, task_version);
ALTER TABLE ONLY public.golden_exact_plan_snapshot_reservations
    ADD CONSTRAINT golden_exact_plan_snapshot_reservations_plan_fk FOREIGN KEY (plan_id, tournament_id, roster_id) REFERENCES public.golden_exact_plan_snapshots(plan_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_exact_plan_snapshot_reservations
    ADD CONSTRAINT golden_exact_plan_snapshot_reservations_reservation_fk FOREIGN KEY (reservation_id, task_id, task_version) REFERENCES public.task_version_reservations(id, task_id, task_version) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_exact_plan_snapshot_seals
    ADD CONSTRAINT golden_exact_plan_snapshot_seals_pkey PRIMARY KEY (plan_id);
ALTER TABLE ONLY public.golden_exact_plan_snapshot_seals
    ADD CONSTRAINT golden_exact_plan_snapshot_seals_scope_key UNIQUE (plan_id, tournament_id, roster_id);
ALTER TABLE ONLY public.golden_exact_plan_snapshot_seals
    ADD CONSTRAINT golden_exact_plan_snapshot_seals_plan_fk FOREIGN KEY (plan_id, tournament_id, roster_id) REFERENCES public.golden_exact_plan_snapshots(plan_id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_state_revisions
    ADD CONSTRAINT golden_state_revisions_pkey PRIMARY KEY (revision_id);
ALTER TABLE ONLY public.golden_state_revisions
    ADD CONSTRAINT golden_state_revisions_scope_revision_key UNIQUE (tournament_id, roster_id, group_id, group_revision_id, revision_number);
ALTER TABLE ONLY public.golden_state_revisions
    ADD CONSTRAINT golden_state_revisions_scope_key UNIQUE (revision_id, tournament_id, roster_id, group_id, group_revision_id);
ALTER TABLE ONLY public.golden_state_revisions
    ADD CONSTRAINT golden_state_revisions_identity_key UNIQUE (revision_id, tournament_id, roster_id);
ALTER TABLE ONLY public.golden_state_revisions
    ADD CONSTRAINT golden_state_revisions_exact_transition_key UNIQUE (revision_id, tournament_id, roster_id, group_id, group_revision_id, previous_revision_id);
ALTER TABLE ONLY public.golden_state_revisions
    ADD CONSTRAINT golden_state_revisions_group_scope_key UNIQUE (revision_id, tournament_id, roster_id, group_revision_id);
ALTER TABLE ONLY public.golden_state_revisions
    ADD CONSTRAINT golden_state_revisions_group_fk FOREIGN KEY (group_revision_id, tournament_id, roster_id) REFERENCES public.golden_group_revisions(revision_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_state_revisions
    ADD CONSTRAINT golden_state_revisions_plan_fk FOREIGN KEY (plan_id, tournament_id, roster_id) REFERENCES public.golden_exact_plan_snapshots(plan_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_state_revisions
    ADD CONSTRAINT golden_state_revisions_previous_fk FOREIGN KEY (previous_revision_id, tournament_id, roster_id, group_id, group_revision_id) REFERENCES public.golden_state_revisions(revision_id, tournament_id, roster_id, group_id, group_revision_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_state_transitions
    ADD CONSTRAINT golden_state_transitions_pkey PRIMARY KEY (state_revision_id);
ALTER TABLE ONLY public.golden_state_transitions
    ADD CONSTRAINT golden_state_transitions_command_key UNIQUE (command_id);
ALTER TABLE ONLY public.golden_state_transitions
    ADD CONSTRAINT golden_state_transitions_state_fk FOREIGN KEY (state_revision_id, tournament_id, roster_id, group_id, group_revision_id, previous_state_revision_id) REFERENCES public.golden_state_revisions(revision_id, tournament_id, roster_id, group_id, group_revision_id, previous_revision_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_state_transitions
    ADD CONSTRAINT golden_state_transitions_previous_fk FOREIGN KEY (previous_state_revision_id, tournament_id, roster_id, group_revision_id) REFERENCES public.golden_state_revisions(revision_id, tournament_id, roster_id, group_revision_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_state_members
    ADD CONSTRAINT golden_state_members_pkey PRIMARY KEY (state_revision_id, participant_id);
ALTER TABLE ONLY public.golden_state_members
    ADD CONSTRAINT golden_state_members_position_key UNIQUE (state_revision_id, position);
ALTER TABLE ONLY public.golden_state_members
    ADD CONSTRAINT golden_state_members_state_fk FOREIGN KEY (state_revision_id, tournament_id, roster_id) REFERENCES public.golden_state_revisions(revision_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_state_members
    ADD CONSTRAINT golden_state_members_participant_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_state_attempts
    ADD CONSTRAINT golden_state_attempts_pkey PRIMARY KEY (state_revision_id, attempt_id);
ALTER TABLE ONLY public.golden_state_attempts
    ADD CONSTRAINT golden_state_attempts_number_key UNIQUE (state_revision_id, attempt_number);
ALTER TABLE ONLY public.golden_state_attempts
    ADD CONSTRAINT golden_state_attempts_scope_key UNIQUE (state_revision_id, attempt_id, tournament_id, roster_id);
ALTER TABLE ONLY public.golden_state_attempts
    ADD CONSTRAINT golden_state_attempts_state_fk FOREIGN KEY (state_revision_id, tournament_id, roster_id, group_revision_id) REFERENCES public.golden_state_revisions(revision_id, tournament_id, roster_id, group_revision_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_state_attempts
    ADD CONSTRAINT golden_state_attempts_attempt_fk FOREIGN KEY (attempt_id, tournament_id, roster_id) REFERENCES public.golden_attempts(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_state_attempt_members
    ADD CONSTRAINT golden_state_attempt_members_pkey PRIMARY KEY (state_revision_id, attempt_id, participant_id);
ALTER TABLE ONLY public.golden_state_attempt_members
    ADD CONSTRAINT golden_state_attempt_members_position_key UNIQUE (state_revision_id, attempt_id, position);
ALTER TABLE ONLY public.golden_state_attempt_members
    ADD CONSTRAINT golden_state_attempt_members_attempt_fk FOREIGN KEY (state_revision_id, attempt_id, tournament_id, roster_id) REFERENCES public.golden_state_attempts(state_revision_id, attempt_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_state_attempt_members
    ADD CONSTRAINT golden_state_attempt_members_participant_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_state_ready_windows
    ADD CONSTRAINT golden_state_ready_windows_pkey PRIMARY KEY (state_revision_id, window_id);
ALTER TABLE ONLY public.golden_state_ready_windows
    ADD CONSTRAINT golden_state_ready_windows_window_revision_key UNIQUE (window_revision_id);
ALTER TABLE ONLY public.golden_state_ready_windows
    ADD CONSTRAINT golden_state_ready_windows_state_fk FOREIGN KEY (state_revision_id, tournament_id, roster_id) REFERENCES public.golden_state_revisions(revision_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_state_ready_windows
    ADD CONSTRAINT golden_state_ready_windows_scope_key UNIQUE (state_revision_id, window_id, tournament_id, roster_id);
ALTER TABLE ONLY public.golden_state_ready_windows
    ADD CONSTRAINT golden_state_ready_windows_attempt_fk FOREIGN KEY (state_revision_id, attempt_id, tournament_id, roster_id) REFERENCES public.golden_state_attempts(state_revision_id, attempt_id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_state_ready_window_participants
    ADD CONSTRAINT golden_state_ready_window_participants_pkey PRIMARY KEY (state_revision_id, window_id, membership_kind, participant_id);
ALTER TABLE ONLY public.golden_state_ready_window_participants
    ADD CONSTRAINT golden_state_ready_window_participants_position_key UNIQUE (state_revision_id, window_id, membership_kind, position);
ALTER TABLE ONLY public.golden_state_ready_window_participants
    ADD CONSTRAINT golden_state_ready_window_participants_window_fk FOREIGN KEY (state_revision_id, window_id, tournament_id, roster_id) REFERENCES public.golden_state_ready_windows(state_revision_id, window_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_state_ready_window_participants
    ADD CONSTRAINT golden_state_ready_window_participants_participant_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_state_ready_events
    ADD CONSTRAINT golden_state_ready_events_pkey PRIMARY KEY (command_id);
ALTER TABLE ONLY public.golden_state_ready_events
    ADD CONSTRAINT golden_state_ready_events_result_key UNIQUE (state_revision_id, command_id);
ALTER TABLE ONLY public.golden_state_ready_events
    ADD CONSTRAINT golden_state_ready_events_state_fk FOREIGN KEY (state_revision_id, tournament_id, roster_id, group_id, group_revision_id) REFERENCES public.golden_state_revisions(revision_id, tournament_id, roster_id, group_id, group_revision_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_state_ready_events
    ADD CONSTRAINT golden_state_ready_events_attempt_fk FOREIGN KEY (state_revision_id, attempt_id, tournament_id, roster_id) REFERENCES public.golden_state_attempts(state_revision_id, attempt_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_state_ready_events
    ADD CONSTRAINT golden_state_ready_events_window_fk FOREIGN KEY (state_revision_id, window_id, tournament_id, roster_id) REFERENCES public.golden_state_ready_windows(state_revision_id, window_id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_state_no_show_resolutions
    ADD CONSTRAINT golden_state_no_show_resolutions_pkey PRIMARY KEY (command_id);
ALTER TABLE ONLY public.golden_state_no_show_resolutions
    ADD CONSTRAINT golden_state_no_show_resolutions_result_key UNIQUE (state_revision_id, command_id);
ALTER TABLE ONLY public.golden_state_no_show_resolutions
    ADD CONSTRAINT golden_state_no_show_resolutions_state_fk FOREIGN KEY (state_revision_id, tournament_id, roster_id, group_id, group_revision_id) REFERENCES public.golden_state_revisions(revision_id, tournament_id, roster_id, group_id, group_revision_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_state_no_show_resolutions
    ADD CONSTRAINT golden_state_no_show_resolutions_scope_key UNIQUE (command_id, state_revision_id, tournament_id, roster_id);
ALTER TABLE ONLY public.golden_state_no_show_resolutions
    ADD CONSTRAINT golden_state_no_show_resolutions_attempt_fk FOREIGN KEY (state_revision_id, attempt_id, tournament_id, roster_id) REFERENCES public.golden_state_attempts(state_revision_id, attempt_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_state_no_show_resolutions
    ADD CONSTRAINT golden_state_no_show_resolutions_window_fk FOREIGN KEY (state_revision_id, window_id, tournament_id, roster_id) REFERENCES public.golden_state_ready_windows(state_revision_id, window_id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_state_no_show_participants
    ADD CONSTRAINT golden_state_no_show_participants_pkey PRIMARY KEY (state_revision_id, command_id, membership_kind, participant_id);
ALTER TABLE ONLY public.golden_state_no_show_participants
    ADD CONSTRAINT golden_state_no_show_participants_position_key UNIQUE (state_revision_id, command_id, membership_kind, position);
ALTER TABLE ONLY public.golden_state_no_show_participants
    ADD CONSTRAINT golden_state_no_show_participants_no_show_fk FOREIGN KEY (command_id, state_revision_id, tournament_id, roster_id) REFERENCES public.golden_state_no_show_resolutions(command_id, state_revision_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_state_no_show_participants
    ADD CONSTRAINT golden_state_no_show_participants_participant_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_state_allocations
    ADD CONSTRAINT golden_state_allocations_pkey PRIMARY KEY (allocation_id);
ALTER TABLE ONLY public.golden_state_allocations
    ADD CONSTRAINT golden_state_allocations_command_key UNIQUE (command_id);
ALTER TABLE ONLY public.golden_state_allocations
    ADD CONSTRAINT golden_state_allocations_state_key UNIQUE (state_revision_id);
ALTER TABLE ONLY public.golden_state_allocations
    ADD CONSTRAINT golden_state_allocations_state_fk FOREIGN KEY (state_revision_id, tournament_id, roster_id, group_id, group_revision_id) REFERENCES public.golden_state_revisions(revision_id, tournament_id, roster_id, group_id, group_revision_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_state_allocations
    ADD CONSTRAINT golden_state_allocations_scope_key UNIQUE (allocation_id, state_revision_id, tournament_id, roster_id);

ALTER TABLE ONLY public.golden_state_allocation_inputs
    ADD CONSTRAINT golden_state_allocation_inputs_pkey PRIMARY KEY (allocation_id, participant_id);
ALTER TABLE ONLY public.golden_state_allocation_inputs
    ADD CONSTRAINT golden_state_allocation_inputs_position_key UNIQUE (allocation_id, position);
ALTER TABLE ONLY public.golden_state_allocation_inputs
    ADD CONSTRAINT golden_state_allocation_inputs_allocation_fk FOREIGN KEY (allocation_id, state_revision_id, tournament_id, roster_id) REFERENCES public.golden_state_allocations(allocation_id, state_revision_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_state_allocation_inputs
    ADD CONSTRAINT golden_state_allocation_inputs_participant_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_state_allocation_positions
    ADD CONSTRAINT golden_state_allocation_positions_pkey PRIMARY KEY (allocation_id, position);
ALTER TABLE ONLY public.golden_state_allocation_positions
    ADD CONSTRAINT golden_state_allocation_positions_participant_key UNIQUE (allocation_id, participant_id);
ALTER TABLE ONLY public.golden_state_allocation_positions
    ADD CONSTRAINT golden_state_allocation_positions_allocation_fk FOREIGN KEY (allocation_id, state_revision_id, tournament_id, roster_id) REFERENCES public.golden_state_allocations(allocation_id, state_revision_id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_state_revision_seals
    ADD CONSTRAINT golden_state_revision_seals_pkey PRIMARY KEY (state_revision_id);
ALTER TABLE ONLY public.golden_state_revision_seals
    ADD CONSTRAINT golden_state_revision_seals_scope_key UNIQUE (state_revision_id, tournament_id, roster_id);
ALTER TABLE ONLY public.golden_state_revision_seals
    ADD CONSTRAINT golden_state_revision_seals_state_fk FOREIGN KEY (state_revision_id, tournament_id, roster_id) REFERENCES public.golden_state_revisions(revision_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_state_allocation_positions
    ADD CONSTRAINT golden_state_allocation_positions_participant_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_attempt_authorities
    ADD CONSTRAINT golden_attempt_authorities_pkey PRIMARY KEY (attempt_id);
ALTER TABLE ONLY public.golden_attempt_authorities
    ADD CONSTRAINT golden_attempt_authorities_scope_key UNIQUE (attempt_id, tournament_id, roster_id);
ALTER TABLE ONLY public.golden_attempt_authorities
    ADD CONSTRAINT golden_attempt_authorities_group_scope_key UNIQUE (attempt_id, tournament_id, roster_id, group_revision_id);
ALTER TABLE ONLY public.golden_attempt_authorities
    ADD CONSTRAINT golden_attempt_authorities_attempt_fk FOREIGN KEY (attempt_id, tournament_id, roster_id) REFERENCES public.golden_attempts(id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_attempt_authorities
    ADD CONSTRAINT golden_attempt_authorities_group_fk FOREIGN KEY (group_revision_id, tournament_id, roster_id) REFERENCES public.golden_group_revisions(revision_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_attempt_authorities
    ADD CONSTRAINT golden_attempt_authorities_wave_fk FOREIGN KEY (wave_id, tournament_id, roster_id) REFERENCES public.waves(id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_attempt_authorities
    ADD CONSTRAINT golden_attempt_authorities_snapshot_fk FOREIGN KEY (snapshot_id, task_id, task_version) REFERENCES public.task_snapshots(id, task_id, task_version) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_attempt_authorities
    ADD CONSTRAINT golden_attempt_authorities_assignment_fk FOREIGN KEY (assignment_id) REFERENCES public.assignments(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_attempt_submission_revisions
    ADD CONSTRAINT golden_attempt_submission_revisions_pkey PRIMARY KEY (revision_id);
ALTER TABLE ONLY public.golden_attempt_submission_revisions
    ADD CONSTRAINT golden_attempt_submission_revisions_number_key UNIQUE (attempt_id, revision_number);
ALTER TABLE ONLY public.golden_attempt_submission_revisions
    ADD CONSTRAINT golden_attempt_submission_revisions_scope_key UNIQUE (revision_id, attempt_id, tournament_id, roster_id);
ALTER TABLE ONLY public.golden_attempt_submission_revisions
    ADD CONSTRAINT golden_attempt_submission_revisions_attempt_fk FOREIGN KEY (attempt_id, tournament_id, roster_id) REFERENCES public.golden_attempt_authorities(attempt_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_attempt_submission_revisions
    ADD CONSTRAINT golden_attempt_submission_revisions_previous_fk FOREIGN KEY (previous_revision_id, attempt_id, tournament_id, roster_id) REFERENCES public.golden_attempt_submission_revisions(revision_id, attempt_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_attempt_submission_revisions
    ADD CONSTRAINT golden_attempt_submission_revisions_submission_fk FOREIGN KEY (provisional_submission_id, attempt_id, membership_id, participant_id, tournament_id, roster_id) REFERENCES public.golden_provisional_submissions(id, attempt_id, membership_id, participant_id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_position_ledger_revisions
    ADD CONSTRAINT golden_position_ledger_revisions_pkey PRIMARY KEY (revision_id);
ALTER TABLE ONLY public.golden_position_ledger_revisions
    ADD CONSTRAINT golden_position_ledger_revisions_number_key UNIQUE (tournament_id, roster_id, group_revision_id, revision_number);
ALTER TABLE ONLY public.golden_position_ledger_revisions
    ADD CONSTRAINT golden_position_ledger_revisions_scope_key UNIQUE (revision_id, tournament_id, roster_id, group_revision_id);
ALTER TABLE ONLY public.golden_position_ledger_revisions
    ADD CONSTRAINT golden_position_ledger_revisions_identity_key UNIQUE (revision_id, tournament_id, roster_id);
ALTER TABLE ONLY public.golden_position_ledger_revisions
    ADD CONSTRAINT golden_position_ledger_revisions_group_fk FOREIGN KEY (group_revision_id, tournament_id, roster_id) REFERENCES public.golden_group_revisions(revision_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_position_ledger_revisions
    ADD CONSTRAINT golden_position_ledger_revisions_previous_fk FOREIGN KEY (previous_revision_id, tournament_id, roster_id, group_revision_id) REFERENCES public.golden_position_ledger_revisions(revision_id, tournament_id, roster_id, group_revision_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_position_ledger_attempts
    ADD CONSTRAINT golden_position_ledger_attempts_pkey PRIMARY KEY (ledger_revision_id, attempt_id);
ALTER TABLE ONLY public.golden_position_ledger_attempts
    ADD CONSTRAINT golden_position_ledger_attempts_scope_key UNIQUE (ledger_revision_id, attempt_id, tournament_id, roster_id);
ALTER TABLE ONLY public.golden_position_ledger_attempts
    ADD CONSTRAINT golden_position_ledger_attempts_ledger_fk FOREIGN KEY (ledger_revision_id, tournament_id, roster_id, group_revision_id) REFERENCES public.golden_position_ledger_revisions(revision_id, tournament_id, roster_id, group_revision_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_position_ledger_attempts
    ADD CONSTRAINT golden_position_ledger_attempts_authority_fk FOREIGN KEY (attempt_id, tournament_id, roster_id, group_revision_id) REFERENCES public.golden_attempt_authorities(attempt_id, tournament_id, roster_id, group_revision_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_position_ledger_attempts
    ADD CONSTRAINT golden_position_ledger_attempts_submission_fk FOREIGN KEY (submission_revision_id, attempt_id, tournament_id, roster_id) REFERENCES public.golden_attempt_submission_revisions(revision_id, attempt_id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_position_ledger_commit_bindings
    ADD CONSTRAINT golden_position_ledger_commit_bindings_pkey PRIMARY KEY (ledger_revision_id, position_commit_id);
ALTER TABLE ONLY public.golden_position_ledger_commit_bindings
    ADD CONSTRAINT golden_position_ledger_commit_bindings_position_key UNIQUE (ledger_revision_id, position);
ALTER TABLE ONLY public.golden_position_ledger_commit_bindings
    ADD CONSTRAINT golden_position_ledger_commit_bindings_participant_key UNIQUE (ledger_revision_id, participant_id);
ALTER TABLE ONLY public.golden_position_ledger_commit_bindings
    ADD CONSTRAINT golden_position_ledger_commit_bindings_ledger_fk FOREIGN KEY (ledger_revision_id, tournament_id, roster_id) REFERENCES public.golden_position_ledger_revisions(revision_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_position_ledger_commit_bindings
    ADD CONSTRAINT golden_position_ledger_commit_bindings_attempt_fk FOREIGN KEY (ledger_revision_id, attempt_id, tournament_id, roster_id) REFERENCES public.golden_position_ledger_attempts(ledger_revision_id, attempt_id, tournament_id, roster_id) ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_position_ledger_commit_bindings
    ADD CONSTRAINT golden_position_ledger_commit_bindings_commit_fk FOREIGN KEY (position_commit_id, attempt_id, participant_id, tournament_id, roster_id) REFERENCES public.golden_position_commits(id, attempt_id, participant_id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_position_ledger_revision_seals
    ADD CONSTRAINT golden_position_ledger_revision_seals_pkey PRIMARY KEY (ledger_revision_id);
ALTER TABLE ONLY public.golden_position_ledger_revision_seals
    ADD CONSTRAINT golden_position_ledger_revision_seals_scope_key UNIQUE (ledger_revision_id, tournament_id, roster_id);
ALTER TABLE ONLY public.golden_position_ledger_revision_seals
    ADD CONSTRAINT golden_position_ledger_revision_seals_ledger_fk FOREIGN KEY (ledger_revision_id, tournament_id, roster_id) REFERENCES public.golden_position_ledger_revisions(revision_id, tournament_id, roster_id) ON DELETE RESTRICT;

CREATE TRIGGER golden_exact_plan_snapshots_append_only
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_exact_plan_snapshots
    FOR EACH ROW EXECUTE FUNCTION public.golden_exact_plan_snapshot_guard();
CREATE TRIGGER golden_exact_plan_snapshot_groups_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_exact_plan_snapshot_groups
    FOR EACH ROW EXECUTE FUNCTION public.golden_exact_plan_snapshot_child_guard();
CREATE TRIGGER golden_exact_plan_snapshot_members_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_exact_plan_snapshot_members
    FOR EACH ROW EXECUTE FUNCTION public.golden_exact_plan_snapshot_child_guard();
CREATE TRIGGER golden_exact_plan_snapshot_edges_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_exact_plan_snapshot_edges
    FOR EACH ROW EXECUTE FUNCTION public.golden_exact_plan_snapshot_child_guard();
CREATE TRIGGER golden_exact_plan_snapshot_candidates_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_exact_plan_snapshot_candidates
    FOR EACH ROW EXECUTE FUNCTION public.golden_exact_plan_snapshot_child_guard();
CREATE TRIGGER golden_exact_plan_snapshot_history_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_exact_plan_snapshot_history
    FOR EACH ROW EXECUTE FUNCTION public.golden_exact_plan_snapshot_child_guard();
CREATE TRIGGER golden_exact_plan_snapshot_participant_reservations_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_exact_plan_snapshot_participant_reservations
    FOR EACH ROW EXECUTE FUNCTION public.golden_exact_plan_snapshot_child_guard();
CREATE TRIGGER golden_exact_plan_snapshot_reservations_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_exact_plan_snapshot_reservations
    FOR EACH ROW EXECUTE FUNCTION public.golden_exact_plan_snapshot_child_guard();
CREATE TRIGGER golden_exact_plan_snapshot_seals_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_exact_plan_snapshot_seals
    FOR EACH ROW EXECUTE FUNCTION public.golden_exact_plan_snapshot_seal_guard();
CREATE TRIGGER golden_state_revisions_append_only
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_state_revisions
    FOR EACH ROW EXECUTE FUNCTION public.golden_state_revision_guard();
CREATE TRIGGER golden_state_transitions_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_state_transitions
    FOR EACH ROW EXECUTE FUNCTION public.golden_state_revision_child_guard();
CREATE TRIGGER golden_state_members_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_state_members
    FOR EACH ROW EXECUTE FUNCTION public.golden_state_revision_child_guard();
CREATE TRIGGER golden_state_attempts_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_state_attempts
    FOR EACH ROW EXECUTE FUNCTION public.golden_state_revision_child_guard();
CREATE TRIGGER golden_state_attempt_members_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_state_attempt_members
    FOR EACH ROW EXECUTE FUNCTION public.golden_state_revision_child_guard();
CREATE TRIGGER golden_state_ready_windows_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_state_ready_windows
    FOR EACH ROW EXECUTE FUNCTION public.golden_state_revision_child_guard();
CREATE TRIGGER golden_state_ready_window_participants_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_state_ready_window_participants
    FOR EACH ROW EXECUTE FUNCTION public.golden_state_revision_child_guard();
CREATE TRIGGER golden_state_ready_events_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_state_ready_events
    FOR EACH ROW EXECUTE FUNCTION public.golden_state_revision_child_guard();
CREATE TRIGGER golden_state_no_show_resolutions_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_state_no_show_resolutions
    FOR EACH ROW EXECUTE FUNCTION public.golden_state_revision_child_guard();
CREATE TRIGGER golden_state_no_show_participants_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_state_no_show_participants
    FOR EACH ROW EXECUTE FUNCTION public.golden_state_revision_child_guard();
CREATE TRIGGER golden_state_allocations_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_state_allocations
    FOR EACH ROW EXECUTE FUNCTION public.golden_state_revision_child_guard();
CREATE TRIGGER golden_state_allocation_inputs_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_state_allocation_inputs
    FOR EACH ROW EXECUTE FUNCTION public.golden_state_revision_child_guard();
CREATE TRIGGER golden_state_allocation_positions_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_state_allocation_positions
    FOR EACH ROW EXECUTE FUNCTION public.golden_state_revision_child_guard();
CREATE TRIGGER golden_state_revision_seals_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_state_revision_seals
    FOR EACH ROW EXECUTE FUNCTION public.golden_state_revision_seal_guard();
CREATE TRIGGER golden_position_ledger_revisions_append_only
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_position_ledger_revisions
    FOR EACH ROW EXECUTE FUNCTION public.golden_position_ledger_revision_guard();
CREATE TRIGGER golden_attempt_submission_revisions_append_only
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_attempt_submission_revisions
    FOR EACH ROW EXECUTE FUNCTION public.golden_attempt_submission_revision_guard();
CREATE TRIGGER golden_position_ledger_attempts_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_position_ledger_attempts
    FOR EACH ROW EXECUTE FUNCTION public.golden_position_ledger_revision_child_guard();
CREATE TRIGGER golden_position_ledger_commit_bindings_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_position_ledger_commit_bindings
    FOR EACH ROW EXECUTE FUNCTION public.golden_position_ledger_revision_child_guard();
CREATE TRIGGER golden_position_ledger_revision_seals_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_position_ledger_revision_seals
    FOR EACH ROW EXECUTE FUNCTION public.golden_position_ledger_revision_seal_guard();
CREATE CONSTRAINT TRIGGER golden_exact_plan_snapshot_coverage
    AFTER INSERT ON public.golden_exact_plan_snapshots
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION public.validate_golden_exact_plan_snapshot();
CREATE CONSTRAINT TRIGGER golden_state_revision_coverage
    AFTER INSERT ON public.golden_state_revisions
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION public.validate_golden_state_revision();
CREATE CONSTRAINT TRIGGER golden_position_ledger_revision_coverage
    AFTER INSERT ON public.golden_position_ledger_revisions
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION public.validate_golden_position_ledger_revision();
CREATE TRIGGER golden_attempt_authorities_append_only
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_attempt_authorities
    FOR EACH ROW EXECUTE FUNCTION public.golden_attempt_authority_guard();

-- A result correction never rewrites Golden execution evidence. It records a
-- sealed tombstone aggregate which proves the exact group, state, ledger, and
-- retained attempt evidence invalidated by the correction command.
ALTER TABLE ONLY public.result_correction_commits
    ADD CONSTRAINT result_correction_commits_scope_key
    UNIQUE (command_id, tournament_id, roster_id);

CREATE TABLE public.golden_correction_stage_tombstones (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    source_tournament_revision bigint NOT NULL,
    resulting_tournament_revision bigint NOT NULL,
	resulting_tournament_state character varying(32) NOT NULL,
    source_projection_revision_id uuid NOT NULL,
    source_projection_revision bigint NOT NULL,
    resulting_projection_revision_id uuid NOT NULL,
    resulting_projection_revision bigint NOT NULL,
    proof_digest bytea NOT NULL,
    corrected_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_correction_stage_tombstones_identity_check CHECK (
        command_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND tournament_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND roster_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND source_projection_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND resulting_projection_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND source_projection_revision_id <> resulting_projection_revision_id
        AND source_tournament_revision >= 1
        AND resulting_tournament_revision = source_tournament_revision + 1
		AND resulting_tournament_state IN ('golden', 'playoffs')
        AND source_projection_revision >= 1
        AND resulting_projection_revision = source_projection_revision + 1
        AND octet_length(proof_digest) = 32
        AND proof_digest <> decode(repeat('00', 32), 'hex')
        AND corrected_at <= created_at
    )
);

CREATE TABLE public.golden_correction_group_tombstones (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    group_id uuid NOT NULL,
    group_revision_id uuid NOT NULL,
    successor_revision_id uuid NOT NULL,
    replacement_group_id uuid,
    superseded_at timestamp with time zone NOT NULL,
    proof_digest bytea NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_correction_group_tombstones_identity_check CHECK (
        group_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND group_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND successor_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND successor_revision_id <> group_revision_id
        AND (replacement_group_id IS NULL OR replacement_group_id <> '00000000-0000-0000-0000-000000000000'::uuid)
        AND octet_length(proof_digest) = 32
        AND proof_digest <> decode(repeat('00', 32), 'hex')
        AND superseded_at <= created_at
    )
);

CREATE TABLE public.golden_correction_state_tombstones (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    group_revision_id uuid NOT NULL,
    state_revision_id uuid NOT NULL,
    payload_digest bytea NOT NULL,
    tombstoned_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_correction_state_tombstones_digest_check CHECK (
        octet_length(payload_digest) = 32
        AND payload_digest <> decode(repeat('00', 32), 'hex')
        AND tombstoned_at <= created_at
    )
);

CREATE TABLE public.golden_correction_position_tombstones (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    group_revision_id uuid NOT NULL,
    ledger_revision_id uuid NOT NULL,
    payload_digest bytea NOT NULL,
    tombstoned_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_correction_position_tombstones_digest_check CHECK (
        octet_length(payload_digest) = 32
        AND payload_digest <> decode(repeat('00', 32), 'hex')
        AND tombstoned_at <= created_at
    )
);

CREATE TABLE public.golden_correction_attempt_tombstones (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    group_revision_id uuid NOT NULL,
    successor_revision_id uuid NOT NULL,
    state_revision_id uuid NOT NULL,
    attempt_id uuid NOT NULL,
    prior_state character varying(24) NOT NULL,
    retained_at timestamp with time zone NOT NULL,
    cancelled_at timestamp with time zone NOT NULL,
    cancellation_reason text NOT NULL,
    state_payload_digest bytea NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_correction_attempt_tombstones_state_check CHECK (
        prior_state IN ('planned', 'waiting_ready')
        AND cancellation_reason = btrim(cancellation_reason)
        AND cancellation_reason <> ''
        AND char_length(cancellation_reason) <= 512
        AND cancelled_at >= retained_at
        AND cancelled_at <= created_at
        AND octet_length(state_payload_digest) = 32
        AND state_payload_digest <> decode(repeat('00', 32), 'hex')
    )
);

CREATE TABLE public.golden_correction_tombstone_seals (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    proof_digest bytea NOT NULL,
    sealed_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_correction_tombstone_seals_digest_check CHECK (
        octet_length(proof_digest) = 32
        AND proof_digest <> decode(repeat('00', 32), 'hex')
    )
);

ALTER TABLE ONLY public.golden_correction_stage_tombstones
    ADD CONSTRAINT golden_correction_stage_tombstones_pkey
    PRIMARY KEY (command_id);
ALTER TABLE ONLY public.golden_correction_stage_tombstones
    ADD CONSTRAINT golden_correction_stage_tombstones_scope_key
    UNIQUE (command_id, tournament_id, roster_id);
ALTER TABLE ONLY public.golden_correction_stage_tombstones
    ADD CONSTRAINT golden_correction_stage_tombstones_correction_fk
    FOREIGN KEY (command_id, tournament_id, roster_id)
    REFERENCES public.result_correction_commits(command_id, tournament_id, roster_id)
    ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.golden_correction_stage_tombstones
    ADD CONSTRAINT golden_correction_stage_tombstones_roster_fk
    FOREIGN KEY (roster_id, tournament_id)
    REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_correction_group_tombstones
    ADD CONSTRAINT golden_correction_group_tombstones_pkey
    PRIMARY KEY (command_id, group_revision_id);
ALTER TABLE ONLY public.golden_correction_group_tombstones
    ADD CONSTRAINT golden_correction_group_tombstones_successor_key
    UNIQUE (command_id, successor_revision_id);
ALTER TABLE ONLY public.golden_correction_group_tombstones
    ADD CONSTRAINT golden_correction_group_tombstones_group_scope_key
    UNIQUE (command_id, tournament_id, roster_id, group_revision_id);
ALTER TABLE ONLY public.golden_correction_group_tombstones
    ADD CONSTRAINT golden_correction_group_tombstones_scope_key
    UNIQUE (command_id, tournament_id, roster_id, group_revision_id, successor_revision_id);
ALTER TABLE ONLY public.golden_correction_group_tombstones
    ADD CONSTRAINT golden_correction_group_tombstones_correction_fk
    FOREIGN KEY (command_id, tournament_id, roster_id)
    REFERENCES public.result_correction_commits(command_id, tournament_id, roster_id)
    ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.golden_correction_group_tombstones
    ADD CONSTRAINT golden_correction_group_tombstones_stage_fk
    FOREIGN KEY (command_id, tournament_id, roster_id)
    REFERENCES public.golden_correction_stage_tombstones(command_id, tournament_id, roster_id)
    ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.golden_correction_group_tombstones
    ADD CONSTRAINT golden_correction_group_tombstones_group_fk
    FOREIGN KEY (group_revision_id, tournament_id, roster_id)
    REFERENCES public.golden_group_revisions(revision_id, tournament_id, roster_id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_correction_state_tombstones
    ADD CONSTRAINT golden_correction_state_tombstones_pkey
    PRIMARY KEY (command_id, state_revision_id);
ALTER TABLE ONLY public.golden_correction_state_tombstones
    ADD CONSTRAINT golden_correction_state_tombstones_correction_fk
    FOREIGN KEY (command_id, tournament_id, roster_id)
    REFERENCES public.result_correction_commits(command_id, tournament_id, roster_id)
    ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.golden_correction_state_tombstones
    ADD CONSTRAINT golden_correction_state_tombstones_group_fk
    FOREIGN KEY (command_id, tournament_id, roster_id, group_revision_id)
    REFERENCES public.golden_correction_group_tombstones(command_id, tournament_id, roster_id, group_revision_id)
    ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.golden_correction_state_tombstones
    ADD CONSTRAINT golden_correction_state_tombstones_state_fk
    FOREIGN KEY (state_revision_id, tournament_id, roster_id, group_revision_id)
    REFERENCES public.golden_state_revisions(revision_id, tournament_id, roster_id, group_revision_id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_correction_position_tombstones
    ADD CONSTRAINT golden_correction_position_tombstones_pkey
    PRIMARY KEY (command_id, ledger_revision_id);
ALTER TABLE ONLY public.golden_correction_position_tombstones
    ADD CONSTRAINT golden_correction_position_tombstones_correction_fk
    FOREIGN KEY (command_id, tournament_id, roster_id)
    REFERENCES public.result_correction_commits(command_id, tournament_id, roster_id)
    ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.golden_correction_position_tombstones
    ADD CONSTRAINT golden_correction_position_tombstones_group_fk
    FOREIGN KEY (command_id, tournament_id, roster_id, group_revision_id)
    REFERENCES public.golden_correction_group_tombstones(command_id, tournament_id, roster_id, group_revision_id)
    ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.golden_correction_position_tombstones
    ADD CONSTRAINT golden_correction_position_tombstones_ledger_fk
    FOREIGN KEY (ledger_revision_id, tournament_id, roster_id, group_revision_id)
    REFERENCES public.golden_position_ledger_revisions(revision_id, tournament_id, roster_id, group_revision_id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_correction_attempt_tombstones
    ADD CONSTRAINT golden_correction_attempt_tombstones_pkey
    PRIMARY KEY (command_id, attempt_id);
ALTER TABLE ONLY public.golden_correction_attempt_tombstones
    ADD CONSTRAINT golden_correction_attempt_tombstones_correction_fk
    FOREIGN KEY (command_id, tournament_id, roster_id)
    REFERENCES public.result_correction_commits(command_id, tournament_id, roster_id)
    ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.golden_correction_attempt_tombstones
    ADD CONSTRAINT golden_correction_attempt_tombstones_group_fk
    FOREIGN KEY (command_id, tournament_id, roster_id, group_revision_id, successor_revision_id)
    REFERENCES public.golden_correction_group_tombstones(
        command_id, tournament_id, roster_id, group_revision_id, successor_revision_id
    ) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.golden_correction_attempt_tombstones
    ADD CONSTRAINT golden_correction_attempt_tombstones_state_fk
    FOREIGN KEY (state_revision_id, attempt_id, tournament_id, roster_id)
    REFERENCES public.golden_state_attempts(state_revision_id, attempt_id, tournament_id, roster_id)
    ON DELETE RESTRICT;
ALTER TABLE ONLY public.golden_correction_attempt_tombstones
    ADD CONSTRAINT golden_correction_attempt_tombstones_attempt_group_fk
    FOREIGN KEY (attempt_id, tournament_id, roster_id, group_revision_id)
    REFERENCES public.golden_attempt_stage_groups(attempt_id, tournament_id, roster_id, group_revision_id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.golden_correction_tombstone_seals
    ADD CONSTRAINT golden_correction_tombstone_seals_pkey PRIMARY KEY (command_id);
ALTER TABLE ONLY public.golden_correction_tombstone_seals
    ADD CONSTRAINT golden_correction_tombstone_seals_correction_fk
    FOREIGN KEY (command_id, tournament_id, roster_id)
    REFERENCES public.result_correction_commits(command_id, tournament_id, roster_id)
    ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.golden_correction_tombstone_seals
    ADD CONSTRAINT golden_correction_tombstone_seals_stage_fk
    FOREIGN KEY (command_id, tournament_id, roster_id)
    REFERENCES public.golden_correction_stage_tombstones(command_id, tournament_id, roster_id)
    ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;

CREATE FUNCTION public.golden_correction_tombstone_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    parent_command_id uuid;
    persisted_group_id uuid;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden correction tombstones are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_TABLE_NAME = 'golden_correction_stage_tombstones' THEN
        parent_command_id := NEW.command_id;
    ELSE
        parent_command_id := NEW.command_id;
        IF EXISTS (
            SELECT 1
            FROM golden_correction_tombstone_seals AS seal
            WHERE seal.command_id = parent_command_id
        ) THEN
            RAISE EXCEPTION 'Golden correction tombstone aggregate is sealed'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF TG_TABLE_NAME = 'golden_correction_group_tombstones' THEN
        SELECT group_id
        INTO persisted_group_id
        FROM golden_group_revisions
        WHERE revision_id = NEW.group_revision_id
            AND tournament_id = NEW.tournament_id
            AND roster_id = NEW.roster_id
        FOR KEY SHARE;

        IF persisted_group_id IS DISTINCT FROM NEW.group_id THEN
            RAISE EXCEPTION 'Golden correction tombstone group identity is not exact'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.golden_correction_tombstone_seal_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    correction_plan_digest bytea;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden correction tombstone seals are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT plan_digest
    INTO correction_plan_digest
    FROM result_correction_commits
    WHERE command_id = NEW.command_id
        AND tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id
    FOR KEY SHARE;

    IF correction_plan_digest IS NULL OR correction_plan_digest IS DISTINCT FROM NEW.proof_digest THEN
        RAISE EXCEPTION 'Golden correction tombstone seal must bind the exact correction plan'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.validate_golden_correction_stage_tombstone() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    command_source_projection_id uuid;
    command_source_projection bigint;
    command_result_projection_id uuid;
    command_result_projection bigint;
    tournament_state text;
    tournament_revision bigint;
BEGIN
    SELECT source_projection_revision_id,
        source_projection_revision,
        resulting_projection_revision_id,
        resulting_projection_revision
    INTO command_source_projection_id,
        command_source_projection,
        command_result_projection_id,
        command_result_projection
    FROM result_correction_commits
    WHERE command_id = NEW.command_id
        AND tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id
    FOR KEY SHARE;

    SELECT state, revision
    INTO tournament_state, tournament_revision
    FROM tournaments
    WHERE id = NEW.tournament_id
    FOR KEY SHARE;

    IF command_source_projection_id IS DISTINCT FROM NEW.source_projection_revision_id
        OR command_source_projection IS DISTINCT FROM NEW.source_projection_revision
        OR command_result_projection_id IS DISTINCT FROM NEW.resulting_projection_revision_id
        OR command_result_projection IS DISTINCT FROM NEW.resulting_projection_revision
        OR tournament_state IS DISTINCT FROM NEW.resulting_tournament_state
        OR tournament_revision IS DISTINCT FROM NEW.resulting_tournament_revision THEN
        RAISE EXCEPTION 'Golden correction stage tombstone has stale correction lineage'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.validate_golden_correction_attempt_tombstone() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    physical_state text;
    physical_cancelled_at timestamp with time zone;
    physical_reason text;
    snapshot_state text;
    snapshot_retained_at timestamp with time zone;
    snapshot_started_at timestamp with time zone;
    snapshot_finished_at timestamp with time zone;
    snapshot_digest bytea;
BEGIN
    SELECT attempt.state, attempt.cancelled_at, attempt.cancellation_reason,
        state_attempt.state, state_attempt.retained_at,
        state_attempt.started_at, state_attempt.finished_at,
        state_revision.payload_digest
    INTO physical_state, physical_cancelled_at, physical_reason,
        snapshot_state, snapshot_retained_at,
        snapshot_started_at, snapshot_finished_at,
        snapshot_digest
    FROM golden_state_attempts AS state_attempt
    INNER JOIN golden_state_revisions AS state_revision
        ON state_revision.revision_id = state_attempt.state_revision_id
        AND state_revision.tournament_id = state_attempt.tournament_id
        AND state_revision.roster_id = state_attempt.roster_id
        AND state_revision.group_revision_id = state_attempt.group_revision_id
    INNER JOIN golden_attempts AS attempt
        ON attempt.id = state_attempt.attempt_id
        AND attempt.tournament_id = state_attempt.tournament_id
        AND attempt.roster_id = state_attempt.roster_id
    WHERE state_attempt.state_revision_id = NEW.state_revision_id
        AND state_attempt.attempt_id = NEW.attempt_id
        AND state_attempt.tournament_id = NEW.tournament_id
        AND state_attempt.roster_id = NEW.roster_id
        AND state_attempt.group_revision_id = NEW.group_revision_id
    FOR KEY SHARE OF state_attempt, state_revision, attempt;

    IF physical_state IS DISTINCT FROM 'cancelled'
        OR physical_cancelled_at IS DISTINCT FROM NEW.cancelled_at
        OR physical_reason IS DISTINCT FROM NEW.cancellation_reason
        OR snapshot_state IS DISTINCT FROM NEW.prior_state
        OR snapshot_retained_at IS DISTINCT FROM NEW.retained_at
        OR snapshot_started_at IS NOT NULL
        OR snapshot_finished_at IS NOT NULL
        OR snapshot_digest IS DISTINCT FROM NEW.state_payload_digest THEN
        RAISE EXCEPTION 'Golden correction attempt tombstone lacks exact cancelled authority'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.validate_golden_correction_tombstone_aggregate() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    incomplete_state_count integer;
    incomplete_ledger_count integer;
    incomplete_attempt_count integer;
    missing_stage_count integer;
BEGIN
    SELECT COUNT(*)
    INTO missing_stage_count
    FROM golden_correction_stage_tombstones AS stage
    WHERE stage.command_id = NEW.command_id
        AND stage.tournament_id = NEW.tournament_id
        AND stage.roster_id = NEW.roster_id;

    SELECT COUNT(*)
    INTO incomplete_state_count
    FROM golden_correction_group_tombstones AS group_tombstone
    INNER JOIN golden_state_revisions AS state_revision
        ON state_revision.group_revision_id = group_tombstone.group_revision_id
        AND state_revision.tournament_id = group_tombstone.tournament_id
        AND state_revision.roster_id = group_tombstone.roster_id
    WHERE group_tombstone.command_id = NEW.command_id
        AND group_tombstone.tournament_id = NEW.tournament_id
        AND group_tombstone.roster_id = NEW.roster_id
        AND NOT EXISTS (
            SELECT 1
            FROM golden_correction_state_tombstones AS state_tombstone
            WHERE state_tombstone.command_id = NEW.command_id
                AND state_tombstone.state_revision_id = state_revision.revision_id
                AND state_tombstone.payload_digest IS NOT DISTINCT FROM state_revision.payload_digest
        );

    SELECT COUNT(*)
    INTO incomplete_ledger_count
    FROM golden_correction_group_tombstones AS group_tombstone
    INNER JOIN golden_position_ledger_revisions AS ledger_revision
        ON ledger_revision.group_revision_id = group_tombstone.group_revision_id
        AND ledger_revision.tournament_id = group_tombstone.tournament_id
        AND ledger_revision.roster_id = group_tombstone.roster_id
    WHERE group_tombstone.command_id = NEW.command_id
        AND group_tombstone.tournament_id = NEW.tournament_id
        AND group_tombstone.roster_id = NEW.roster_id
        AND NOT EXISTS (
            SELECT 1
            FROM golden_correction_position_tombstones AS position_tombstone
            WHERE position_tombstone.command_id = NEW.command_id
                AND position_tombstone.ledger_revision_id = ledger_revision.revision_id
                AND position_tombstone.payload_digest IS NOT DISTINCT FROM ledger_revision.payload_digest
        );

    WITH latest_state AS (
        SELECT DISTINCT ON (state_revision.group_revision_id)
            state_revision.revision_id,
            state_revision.group_revision_id,
            state_revision.tournament_id,
            state_revision.roster_id
        FROM golden_state_revisions AS state_revision
        INNER JOIN golden_correction_group_tombstones AS group_tombstone
            ON group_tombstone.command_id = NEW.command_id
            AND group_tombstone.group_revision_id = state_revision.group_revision_id
            AND group_tombstone.tournament_id = state_revision.tournament_id
            AND group_tombstone.roster_id = state_revision.roster_id
        ORDER BY state_revision.group_revision_id, state_revision.revision_number DESC
    )
    SELECT COUNT(*)
    INTO incomplete_attempt_count
    FROM latest_state
    INNER JOIN golden_state_attempts AS state_attempt
        ON state_attempt.state_revision_id = latest_state.revision_id
        AND state_attempt.tournament_id = latest_state.tournament_id
        AND state_attempt.roster_id = latest_state.roster_id
    WHERE state_attempt.state IN ('planned', 'waiting_ready')
        AND state_attempt.retained_at IS NOT NULL
        AND state_attempt.started_at IS NULL
        AND state_attempt.finished_at IS NULL
        AND NOT EXISTS (
            SELECT 1
            FROM golden_correction_attempt_tombstones AS attempt_tombstone
            WHERE attempt_tombstone.command_id = NEW.command_id
                AND attempt_tombstone.state_revision_id = state_attempt.state_revision_id
                AND attempt_tombstone.attempt_id = state_attempt.attempt_id
                AND attempt_tombstone.group_revision_id = state_attempt.group_revision_id
        );

    IF missing_stage_count <> 1
        OR incomplete_state_count <> 0
        OR incomplete_ledger_count <> 0
        OR incomplete_attempt_count <> 0 THEN
        RAISE EXCEPTION 'Golden correction tombstone aggregate is incomplete'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

-- A sealed correction aggregate is also a closure boundary for the original
-- Golden group. Revalidate newly appended aggregate roots at commit so a
-- child created before its matching tombstone in the same transaction is
-- accepted, while a later transaction cannot append evidence after the seal.
CREATE FUNCTION public.validate_golden_correction_sealed_state_revision() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM golden_correction_group_tombstones AS group_tombstone
        INNER JOIN golden_correction_tombstone_seals AS seal
            ON seal.command_id = group_tombstone.command_id
            AND seal.tournament_id = group_tombstone.tournament_id
            AND seal.roster_id = group_tombstone.roster_id
        WHERE group_tombstone.tournament_id = NEW.tournament_id
            AND group_tombstone.roster_id = NEW.roster_id
            AND group_tombstone.group_revision_id = NEW.group_revision_id
            AND NOT EXISTS (
                SELECT 1
                FROM golden_correction_state_tombstones AS state_tombstone
                WHERE state_tombstone.command_id = group_tombstone.command_id
                    AND state_tombstone.tournament_id = NEW.tournament_id
                    AND state_tombstone.roster_id = NEW.roster_id
                    AND state_tombstone.group_revision_id = NEW.group_revision_id
                    AND state_tombstone.state_revision_id = NEW.revision_id
                    AND state_tombstone.payload_digest IS NOT DISTINCT FROM NEW.payload_digest
            )
    ) THEN
        RAISE EXCEPTION 'sealed Golden correction rejects a late state revision'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.validate_golden_correction_sealed_position_revision() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM golden_correction_group_tombstones AS group_tombstone
        INNER JOIN golden_correction_tombstone_seals AS seal
            ON seal.command_id = group_tombstone.command_id
            AND seal.tournament_id = group_tombstone.tournament_id
            AND seal.roster_id = group_tombstone.roster_id
        WHERE group_tombstone.tournament_id = NEW.tournament_id
            AND group_tombstone.roster_id = NEW.roster_id
            AND group_tombstone.group_revision_id = NEW.group_revision_id
            AND NOT EXISTS (
                SELECT 1
                FROM golden_correction_position_tombstones AS position_tombstone
                WHERE position_tombstone.command_id = group_tombstone.command_id
                    AND position_tombstone.tournament_id = NEW.tournament_id
                    AND position_tombstone.roster_id = NEW.roster_id
                    AND position_tombstone.group_revision_id = NEW.group_revision_id
                    AND position_tombstone.ledger_revision_id = NEW.revision_id
                    AND position_tombstone.payload_digest IS NOT DISTINCT FROM NEW.payload_digest
            )
    ) THEN
        RAISE EXCEPTION 'sealed Golden correction rejects a late position revision'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.validate_golden_correction_sealed_attempt_group() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM golden_correction_group_tombstones AS group_tombstone
        INNER JOIN golden_correction_tombstone_seals AS seal
            ON seal.command_id = group_tombstone.command_id
            AND seal.tournament_id = group_tombstone.tournament_id
            AND seal.roster_id = group_tombstone.roster_id
        WHERE group_tombstone.tournament_id = NEW.tournament_id
            AND group_tombstone.roster_id = NEW.roster_id
            AND group_tombstone.group_revision_id = NEW.group_revision_id
            AND NOT EXISTS (
                SELECT 1
                FROM golden_correction_attempt_tombstones AS attempt_tombstone
                WHERE attempt_tombstone.command_id = group_tombstone.command_id
                    AND attempt_tombstone.tournament_id = NEW.tournament_id
                    AND attempt_tombstone.roster_id = NEW.roster_id
                    AND attempt_tombstone.group_revision_id = NEW.group_revision_id
                    AND attempt_tombstone.attempt_id = NEW.attempt_id
            )
            AND NOT EXISTS (
                SELECT 1
                FROM golden_correction_position_tombstones AS position_tombstone
                INNER JOIN golden_position_ledger_attempts AS ledger_attempt
                    ON ledger_attempt.ledger_revision_id = position_tombstone.ledger_revision_id
                    AND ledger_attempt.tournament_id = position_tombstone.tournament_id
                    AND ledger_attempt.roster_id = position_tombstone.roster_id
                    AND ledger_attempt.group_revision_id = position_tombstone.group_revision_id
                WHERE position_tombstone.command_id = group_tombstone.command_id
                    AND position_tombstone.tournament_id = NEW.tournament_id
                    AND position_tombstone.roster_id = NEW.roster_id
                    AND position_tombstone.group_revision_id = NEW.group_revision_id
                    AND ledger_attempt.attempt_id = NEW.attempt_id
            )
    ) THEN
        RAISE EXCEPTION 'sealed Golden correction rejects late attempt lineage'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER golden_correction_stage_tombstones_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_correction_stage_tombstones
    FOR EACH ROW EXECUTE FUNCTION public.golden_correction_tombstone_guard();
CREATE TRIGGER golden_correction_group_tombstones_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_correction_group_tombstones
    FOR EACH ROW EXECUTE FUNCTION public.golden_correction_tombstone_guard();
CREATE TRIGGER golden_correction_state_tombstones_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_correction_state_tombstones
    FOR EACH ROW EXECUTE FUNCTION public.golden_correction_tombstone_guard();
CREATE TRIGGER golden_correction_position_tombstones_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_correction_position_tombstones
    FOR EACH ROW EXECUTE FUNCTION public.golden_correction_tombstone_guard();
CREATE TRIGGER golden_correction_attempt_tombstones_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_correction_attempt_tombstones
    FOR EACH ROW EXECUTE FUNCTION public.golden_correction_tombstone_guard();
CREATE TRIGGER golden_correction_attempt_tombstones_exact_authority
    BEFORE INSERT ON public.golden_correction_attempt_tombstones
    FOR EACH ROW EXECUTE FUNCTION public.validate_golden_correction_attempt_tombstone();
CREATE TRIGGER golden_correction_tombstone_seals_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_correction_tombstone_seals
    FOR EACH ROW EXECUTE FUNCTION public.golden_correction_tombstone_seal_guard();
CREATE CONSTRAINT TRIGGER golden_correction_stage_tombstones_lineage
    AFTER INSERT ON public.golden_correction_stage_tombstones
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION public.validate_golden_correction_stage_tombstone();
CREATE CONSTRAINT TRIGGER golden_correction_tombstone_seals_coverage
    AFTER INSERT ON public.golden_correction_tombstone_seals
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION public.validate_golden_correction_tombstone_aggregate();
CREATE CONSTRAINT TRIGGER golden_state_revisions_correction_closure
    AFTER INSERT ON public.golden_state_revisions
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION public.validate_golden_correction_sealed_state_revision();
CREATE CONSTRAINT TRIGGER golden_position_ledger_revisions_correction_closure
    AFTER INSERT ON public.golden_position_ledger_revisions
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION public.validate_golden_correction_sealed_position_revision();
CREATE CONSTRAINT TRIGGER golden_attempt_stage_groups_correction_closure
    AFTER INSERT ON public.golden_attempt_stage_groups
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION public.validate_golden_correction_sealed_attempt_group();

-- Golden application repositories retain complete aggregate documents only
-- where the normalized Golden evidence cannot express replay state. The
-- normalized plan, state, attempt, submission and position relations remain
-- the source authority. These rows bind a validated application aggregate to
-- its immutable evidence and make command replay durable without publishing
-- private task material in the command journal.

CREATE TABLE public.golden_repository_scopes (
    id uuid NOT NULL,
    aggregate_kind character varying(16) NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    plan_set_id uuid,
    group_id uuid,
    group_revision_id uuid,
    attempt_id uuid,
    wave_id uuid,
    assignment_id uuid,
    snapshot_id uuid,
    task_id uuid,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_repository_scopes_pkey PRIMARY KEY (id),
    CONSTRAINT golden_repository_scopes_kind_check CHECK (
        aggregate_kind IN (
            'plan', 'state', 'wave', 'submission', 'attempt', 'connection',
            'continuation', 'failure', 'prestart'
        )
    ),
    CONSTRAINT golden_repository_scopes_identity_check CHECK (
        id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND tournament_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND roster_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND (plan_set_id IS NULL OR plan_set_id <> '00000000-0000-0000-0000-000000000000'::uuid)
        AND (group_id IS NULL OR group_id <> '00000000-0000-0000-0000-000000000000'::uuid)
        AND (
            group_revision_id IS NULL
            OR group_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        )
        AND (attempt_id IS NULL OR attempt_id <> '00000000-0000-0000-0000-000000000000'::uuid)
        AND (wave_id IS NULL OR wave_id <> '00000000-0000-0000-0000-000000000000'::uuid)
        AND (
            assignment_id IS NULL
            OR assignment_id <> '00000000-0000-0000-0000-000000000000'::uuid
        )
        AND (snapshot_id IS NULL OR snapshot_id <> '00000000-0000-0000-0000-000000000000'::uuid)
        AND (task_id IS NULL OR task_id <> '00000000-0000-0000-0000-000000000000'::uuid)
    ),
    CONSTRAINT golden_repository_scopes_shape_check CHECK (
        (
            aggregate_kind = 'plan'
            AND plan_set_id IS NOT NULL
            AND group_id IS NULL
            AND group_revision_id IS NULL
            AND attempt_id IS NULL
            AND wave_id IS NULL
            AND assignment_id IS NULL
            AND snapshot_id IS NULL
            AND task_id IS NULL
        )
        OR (
            aggregate_kind IN ('state', 'continuation', 'prestart')
            AND plan_set_id IS NULL
            AND group_id IS NOT NULL
            AND group_revision_id IS NOT NULL
            AND attempt_id IS NULL
            AND wave_id IS NULL
            AND assignment_id IS NULL
            AND snapshot_id IS NULL
            AND task_id IS NULL
        )
        OR (
            aggregate_kind IN ('wave', 'submission', 'attempt', 'connection', 'failure')
            AND plan_set_id IS NULL
            AND group_id IS NOT NULL
            AND group_revision_id IS NOT NULL
            AND attempt_id IS NOT NULL
            AND wave_id IS NOT NULL
            AND assignment_id IS NOT NULL
            AND snapshot_id IS NOT NULL
            AND task_id IS NOT NULL
        )
    ),
    CONSTRAINT golden_repository_scopes_roster_fk FOREIGN KEY (roster_id, tournament_id)
        REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT,
    CONSTRAINT golden_repository_scopes_group_revision_fk FOREIGN KEY (
        group_revision_id, tournament_id, roster_id
    ) REFERENCES public.golden_group_revisions(revision_id, tournament_id, roster_id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX golden_repository_scopes_natural_key
    ON public.golden_repository_scopes USING btree (
        aggregate_kind,
        tournament_id,
        roster_id,
        plan_set_id,
        group_id,
        group_revision_id,
        attempt_id,
        wave_id,
        assignment_id,
        snapshot_id,
        task_id
    ) NULLS NOT DISTINCT;

CREATE TABLE public.golden_repository_revisions (
    scope_id uuid NOT NULL,
    aggregate_kind character varying(16) NOT NULL,
    revision_id uuid NOT NULL,
    revision_number bigint NOT NULL,
    previous_revision_id uuid,
    payload jsonb NOT NULL,
    payload_digest bytea NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_repository_revisions_pkey PRIMARY KEY (scope_id, revision_id),
    CONSTRAINT golden_repository_revisions_number_key UNIQUE (scope_id, revision_number),
    CONSTRAINT golden_repository_revisions_head_key UNIQUE (
        scope_id, revision_id, revision_number, payload_digest
    ),
    CONSTRAINT golden_repository_revisions_identity_check CHECK (
        revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND (previous_revision_id IS NULL OR previous_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid)
        AND revision_number >= 1
        AND (
            (revision_number = 1 AND previous_revision_id IS NULL)
            OR (revision_number > 1 AND previous_revision_id IS NOT NULL)
        )
    ),
    CONSTRAINT golden_repository_revisions_payload_check CHECK (
        jsonb_typeof(payload) = 'object'
        AND payload ->> 'schema' = 'golden-aggregate-v1'
        AND payload ->> 'kind' = aggregate_kind
        AND payload ->> 'revision_id' = revision_id::text
        AND payload ->> 'revision_number' = revision_number::text
        AND payload ->> 'payload_digest' = encode(payload_digest, 'hex')
        AND jsonb_typeof(payload -> 'document') = 'object'
        AND octet_length(payload_digest) = 32
        AND payload_digest <> decode(repeat('00', 32), 'hex')
    ),
    CONSTRAINT golden_repository_revisions_scope_fk FOREIGN KEY (scope_id)
        REFERENCES public.golden_repository_scopes(id) ON DELETE RESTRICT,
    CONSTRAINT golden_repository_revisions_previous_fk FOREIGN KEY (scope_id, previous_revision_id)
        REFERENCES public.golden_repository_revisions(scope_id, revision_id) ON DELETE RESTRICT
);

CREATE TABLE public.golden_repository_heads (
    scope_id uuid NOT NULL,
    revision_id uuid NOT NULL,
    revision_number bigint NOT NULL,
    payload_digest bytea NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_repository_heads_pkey PRIMARY KEY (scope_id),
    CONSTRAINT golden_repository_heads_revision_fk FOREIGN KEY (
        scope_id, revision_id, revision_number, payload_digest
    ) REFERENCES public.golden_repository_revisions(
        scope_id, revision_id, revision_number, payload_digest
    ) ON DELETE RESTRICT
);

CREATE TABLE public.golden_repository_command_journal (
    scope_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    command_id uuid NOT NULL,
    command_kind character varying(32) NOT NULL,
    command_digest bytea NOT NULL,
    result_revision_id uuid NOT NULL,
    occurred_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_repository_command_journal_pkey PRIMARY KEY (scope_id, command_id),
    CONSTRAINT golden_repository_command_journal_tournament_command_key UNIQUE (tournament_id, command_id),
    CONSTRAINT golden_repository_command_journal_kind_check CHECK (
        command_kind IN (
            'state_ready', 'state_no_show', 'state_allocation',
            'wave_opened', 'wave_ready', 'wave_disconnected', 'wave_reconnected', 'wave_started',
            'submission', 'attempt_terminal', 'connection_disconnect', 'connection_reconnect',
            'continuation', 'failure', 'prestart_pause', 'prestart_resume'
        )
    ),
    CONSTRAINT golden_repository_command_journal_identity_check CHECK (
        command_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND result_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND octet_length(command_digest) = 32
        AND command_digest <> decode(repeat('00', 32), 'hex')
    ),
    CONSTRAINT golden_repository_command_journal_scope_fk FOREIGN KEY (scope_id)
        REFERENCES public.golden_repository_scopes(id) ON DELETE RESTRICT,
    CONSTRAINT golden_repository_command_journal_result_fk FOREIGN KEY (scope_id, result_revision_id)
        REFERENCES public.golden_repository_revisions(scope_id, revision_id) ON DELETE RESTRICT
);

CREATE INDEX golden_repository_command_journal_lookup_idx
    ON public.golden_repository_command_journal USING btree (tournament_id, command_id);

CREATE FUNCTION public.golden_repository_scope_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden repository scope identity is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.golden_repository_revision_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    scope_kind character varying(16);
    previous_number bigint;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden repository revision evidence is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT aggregate_kind
    INTO scope_kind
    FROM golden_repository_scopes
    WHERE id = NEW.scope_id
    FOR KEY SHARE;

    IF scope_kind IS DISTINCT FROM NEW.aggregate_kind THEN
        RAISE EXCEPTION 'Golden repository revision kind must match its scope'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.revision_number = 1 THEN
        IF NEW.previous_revision_id IS NOT NULL THEN
            RAISE EXCEPTION 'initial Golden repository revision has a predecessor'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NEW;
    END IF;

    SELECT revision_number
    INTO previous_number
    FROM golden_repository_revisions
    WHERE scope_id = NEW.scope_id
        AND revision_id = NEW.previous_revision_id
    FOR KEY SHARE;

    IF previous_number IS DISTINCT FROM NEW.revision_number - 1 THEN
        RAISE EXCEPTION 'Golden repository revision lineage is not consecutive'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.golden_repository_head_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.revision_number <> 1 THEN
            RAISE EXCEPTION 'initial Golden repository head must reference revision one'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Golden repository head is retained'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.scope_id IS DISTINCT FROM OLD.scope_id
        OR NEW.revision_number <> OLD.revision_number + 1 THEN
        RAISE EXCEPTION 'Golden repository head transition is not consecutive'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.golden_repository_command_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    scope_kind character varying(16);
    scope_tournament_id uuid;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden repository command replay is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT aggregate_kind, tournament_id
    INTO scope_kind, scope_tournament_id
    FROM golden_repository_scopes
    WHERE id = NEW.scope_id
    FOR KEY SHARE;

    IF scope_tournament_id IS DISTINCT FROM NEW.tournament_id THEN
        RAISE EXCEPTION 'Golden repository command must retain its scope tournament'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NOT (
        (scope_kind = 'state' AND NEW.command_kind IN ('state_ready', 'state_no_show', 'state_allocation'))
        OR (
            scope_kind = 'wave'
            AND NEW.command_kind IN ('wave_opened', 'wave_ready', 'wave_disconnected', 'wave_reconnected', 'wave_started')
        )
        OR (scope_kind = 'submission' AND NEW.command_kind = 'submission')
        OR (scope_kind = 'attempt' AND NEW.command_kind = 'attempt_terminal')
        OR (scope_kind = 'connection' AND NEW.command_kind IN ('connection_disconnect', 'connection_reconnect'))
        OR (scope_kind = 'continuation' AND NEW.command_kind = 'continuation')
        OR (scope_kind = 'failure' AND NEW.command_kind = 'failure')
        OR (scope_kind = 'prestart' AND NEW.command_kind IN ('prestart_pause', 'prestart_resume'))
    ) THEN
        RAISE EXCEPTION 'Golden repository command kind does not match its aggregate'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER golden_repository_scopes_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_repository_scopes
    FOR EACH ROW EXECUTE FUNCTION public.golden_repository_scope_guard();
CREATE TRIGGER golden_repository_revisions_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_repository_revisions
    FOR EACH ROW EXECUTE FUNCTION public.golden_repository_revision_guard();
CREATE TRIGGER golden_repository_heads_lineage
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_repository_heads
    FOR EACH ROW EXECUTE FUNCTION public.golden_repository_head_guard();
CREATE TRIGGER golden_repository_command_journal_immutable
    BEFORE INSERT OR DELETE OR UPDATE ON public.golden_repository_command_journal
    FOR EACH ROW EXECUTE FUNCTION public.golden_repository_command_guard();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS golden_stage_progression_group_coverage
    ON public.tournament_stage_progressions;

DROP TABLE IF EXISTS
    public.golden_repository_command_journal,
    public.golden_repository_heads,
    public.golden_repository_revisions,
    public.golden_repository_scopes,
    public.golden_correction_tombstone_seals,
    public.golden_correction_attempt_tombstones,
    public.golden_correction_position_tombstones,
    public.golden_correction_state_tombstones,
    public.golden_correction_group_tombstones,
    public.golden_correction_stage_tombstones,
    public.golden_position_ledger_revision_seals,
    public.golden_position_ledger_commit_bindings,
    public.golden_position_ledger_attempts,
    public.golden_position_ledger_revisions,
    public.golden_attempt_submission_revisions,
    public.golden_attempt_authorities,
    public.golden_state_revision_seals,
    public.golden_state_allocation_positions,
    public.golden_state_allocation_inputs,
    public.golden_state_allocations,
    public.golden_state_no_show_participants,
    public.golden_state_no_show_resolutions,
    public.golden_state_ready_events,
    public.golden_state_ready_window_participants,
    public.golden_state_ready_windows,
    public.golden_state_attempt_members,
    public.golden_state_attempts,
    public.golden_state_members,
    public.golden_state_transitions,
    public.golden_state_revisions,
    public.golden_exact_plan_snapshot_seals,
    public.golden_exact_plan_snapshot_reservations,
    public.golden_exact_plan_snapshot_participant_reservations,
    public.golden_exact_plan_snapshot_history,
    public.golden_exact_plan_snapshot_candidates,
    public.golden_exact_plan_snapshot_edges,
    public.golden_exact_plan_snapshot_members,
    public.golden_exact_plan_snapshot_groups,
    public.golden_exact_plan_snapshots,
    public.golden_attempt_stage_groups,
    public.golden_group_revisions,
    public.golden_position_commits,
    public.golden_provisional_submissions,
    public.golden_recovery_revisions,
    public.golden_ready_disconnects,
    public.golden_reserve_promotions,
    public.golden_memberships,
    public.golden_attempts;

ALTER TABLE ONLY public.result_correction_commits
    DROP CONSTRAINT IF EXISTS result_correction_commits_scope_key;

DROP FUNCTION IF EXISTS
    public.golden_repository_command_guard(),
    public.golden_repository_head_guard(),
    public.golden_repository_revision_guard(),
    public.golden_repository_scope_guard(),
    public.validate_golden_correction_sealed_attempt_group(),
    public.validate_golden_correction_sealed_position_revision(),
    public.validate_golden_correction_sealed_state_revision(),
    public.validate_golden_correction_tombstone_aggregate(),
    public.validate_golden_correction_attempt_tombstone(),
    public.validate_golden_correction_stage_tombstone(),
    public.golden_correction_tombstone_seal_guard(),
    public.golden_correction_tombstone_guard(),
    public.golden_attempt_authority_guard(),
    public.validate_golden_position_ledger_revision(),
    public.validate_golden_state_revision(),
    public.validate_golden_exact_plan_snapshot(),
    public.golden_position_ledger_revision_seal_guard(),
    public.golden_position_ledger_revision_child_guard(),
    public.golden_position_ledger_revision_guard(),
    public.golden_attempt_submission_revision_guard(),
    public.golden_state_revision_seal_guard(),
    public.golden_state_revision_child_guard(),
    public.golden_state_revision_guard(),
    public.golden_exact_plan_snapshot_seal_guard(),
    public.golden_exact_plan_snapshot_child_guard(),
    public.golden_exact_plan_snapshot_guard(),
    public.validate_golden_stage_progression_groups(),
    public.golden_attempt_stage_group_guard(),
    public.validate_golden_group_revision_progression_open(),
    public.golden_group_revision_guard(),
    public.golden_submission_guard(),
    public.golden_reserve_promotion_guard(),
    public.golden_recovery_revision_guard(),
    public.golden_position_commit_guard(),
    public.golden_membership_guard(),
    public.golden_disconnect_guard(),
    public.golden_attempt_guard();

-- +goose StatementEnd
