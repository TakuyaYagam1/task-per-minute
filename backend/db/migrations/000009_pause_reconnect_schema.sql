-- +goose Up
-- +goose StatementBegin

-- Initial pause and reconnect domain schema.
SET LOCAL check_function_bodies = false;

--
-- Name: game_attempt_revision_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.game_attempt_revision_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.state IS DISTINCT FROM OLD.state
        OR NEW.result_reason IS DISTINCT FROM OLD.result_reason
        OR NEW.winner_id IS DISTINCT FROM OLD.winner_id
        OR NEW.result_revision_id IS DISTINCT FROM OLD.result_revision_id
        OR NEW.started_at IS DISTINCT FROM OLD.started_at
        OR NEW.finished_at IS DISTINCT FROM OLD.finished_at
        OR NEW.updated_at IS DISTINCT FROM OLD.updated_at THEN
        IF NEW.revision <> OLD.revision + 1 OR NEW.updated_at <= OLD.updated_at THEN
            RAISE EXCEPTION 'invalid Game revision CAS transition'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.revision IS DISTINCT FROM OLD.revision THEN
        RAISE EXCEPTION 'Game revision cannot advance without state evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: normal_wave_reconnect_lock(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.normal_wave_reconnect_lock() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.state IS DISTINCT FROM 'active'
        OR NEW.scope_kind IS DISTINCT FROM 'wave'
        OR NEW.parent_pause_id IS NOT NULL THEN
        RETURN NEW;
    END IF;

    PERFORM 1
    FROM rosters
    WHERE id = NEW.roster_id
        AND tournament_id = NEW.tournament_id
    FOR UPDATE;

    PERFORM 1
    FROM waves
    WHERE id = NEW.wave_id
        AND roster_id = NEW.roster_id
    FOR UPDATE;

    PERFORM 1
    FROM pauses AS game_pause
    JOIN series AS series ON series.id = game_pause.series_id
    WHERE game_pause.state = 'active'
        AND game_pause.scope_kind = 'game_attempt'
        AND game_pause.roster_id = NEW.roster_id
        AND EXISTS (
            SELECT 1
            FROM wave_members AS member
            WHERE member.wave_id = NEW.wave_id
                AND member.roster_id = NEW.roster_id
                AND member.participant_id IN (
                    series.first_participant_id,
                    series.second_participant_id
                )
        )
    ORDER BY game_pause.id
    FOR UPDATE OF game_pause;

    PERFORM 1
    FROM reconnect_intervals AS reconnect_interval
    JOIN pauses AS game_pause
        ON game_pause.id = reconnect_interval.pause_id
    JOIN wave_members AS member
        ON member.wave_id = NEW.wave_id
        AND member.roster_id = NEW.roster_id
        AND member.participant_id = reconnect_interval.participant_id
    WHERE game_pause.state = 'active'
        AND game_pause.scope_kind = 'game_attempt'
        AND game_pause.roster_id = NEW.roster_id
        AND reconnect_interval.state = 'open'
        AND reconnect_interval.opened_at < NEW.started_at
        AND NEW.started_at < reconnect_interval.deadline_at
    ORDER BY
        reconnect_interval.pause_id,
        reconnect_interval.participant_id,
        reconnect_interval.interval_number,
        reconnect_interval.continuation_number,
        reconnect_interval.id
    FOR UPDATE OF reconnect_interval;

    RETURN NEW;
END;
$$;

--
-- Name: pause_cancel_reconnect_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.pause_cancel_reconnect_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    PERFORM 1
    FROM reconnect_intervals
    WHERE pause_id = NEW.id
    ORDER BY participant_id, interval_number, id
    FOR UPDATE;

    IF EXISTS (
        SELECT 1
        FROM reconnect_intervals
        WHERE pause_id = NEW.id AND state = 'open'
    ) THEN
        RAISE EXCEPTION 'pause cancellation requires closed reconnect intervals'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: pause_clock_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.pause_clock_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'frozen clock evidence is durable CAS state'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.pause_id IS DISTINCT FROM OLD.pause_id
        OR NEW.game_attempt_id IS DISTINCT FROM OLD.game_attempt_id
        OR NEW.original_deadline IS DISTINCT FROM OLD.original_deadline
        OR NEW.frozen_at IS DISTINCT FROM OLD.frozen_at
        OR NEW.frozen_remaining_ms IS DISTINCT FROM OLD.frozen_remaining_ms
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR OLD.resumed_at IS NOT NULL
        OR NEW.resumed_at IS NULL
        OR NEW.resumed_deadline IS NULL
        OR NEW.revision <> OLD.revision + 1
        OR NEW.updated_at <= OLD.updated_at THEN
        RAISE EXCEPTION 'invalid frozen clock CAS transition'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: pause_graph_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.pause_graph_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    parent_row pauses%ROWTYPE;
    active_descendants INTEGER;
    latest_action VARCHAR(16);
    clock_resumed_at TIMESTAMPTZ;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'pauses are durable recovery state'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_OP = 'INSERT' THEN
        IF NEW.parent_pause_id IS NULL THEN
            IF NEW.depth <> 0 THEN
                RAISE EXCEPTION 'root pause depth must be zero'
                    USING ERRCODE = 'check_violation';
            END IF;
            RETURN NEW;
        END IF;

        SELECT *
        INTO parent_row
        FROM pauses
        WHERE id = NEW.parent_pause_id
        FOR UPDATE;

        IF parent_row.state <> 'active'
            OR NEW.tournament_id IS DISTINCT FROM parent_row.tournament_id
            OR NEW.roster_id IS DISTINCT FROM parent_row.roster_id
            OR NEW.depth <> parent_row.depth + 1
            OR NOT (
                (parent_row.scope_kind = 'tournament' AND NEW.scope_kind IN ('wave', 'series'))
                OR (parent_row.scope_kind = 'wave' AND NEW.scope_kind = 'series')
                OR (parent_row.scope_kind = 'series' AND NEW.scope_kind = 'game_attempt')
            ) THEN
            RAISE EXCEPTION 'invalid child pause graph edge'
                USING ERRCODE = 'check_violation';
        END IF;

        IF parent_row.series_id IS NOT NULL
            AND NEW.series_id IS DISTINCT FROM parent_row.series_id THEN
            RAISE EXCEPTION 'child pause must retain Series identity'
                USING ERRCODE = 'check_violation';
        END IF;

        RETURN NEW;
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.scope_kind IS DISTINCT FROM OLD.scope_kind
        OR NEW.scope_id IS DISTINCT FROM OLD.scope_id
        OR NEW.wave_id IS DISTINCT FROM OLD.wave_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.game_attempt_id IS DISTINCT FROM OLD.game_attempt_id
        OR NEW.parent_pause_id IS DISTINCT FROM OLD.parent_pause_id
        OR NEW.depth IS DISTINCT FROM OLD.depth
        OR NEW.reason IS DISTINCT FROM OLD.reason
        OR NEW.paused_from_state IS DISTINCT FROM OLD.paused_from_state
        OR NEW.started_at IS DISTINCT FROM OLD.started_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR OLD.state <> 'active'
        OR NEW.state NOT IN ('resumed', 'cancelled')
        OR NEW.revision <> OLD.revision + 1
        OR NEW.current_revision_id IS NOT DISTINCT FROM OLD.current_revision_id
        OR NEW.updated_at <= OLD.updated_at THEN
        RAISE EXCEPTION 'invalid pause CAS transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'resumed' THEN
        WITH RECURSIVE descendants AS (
            SELECT id, state
            FROM pauses
            WHERE parent_pause_id = NEW.id
            UNION ALL
            SELECT child.id, child.state
            FROM pauses AS child
            JOIN descendants AS parent ON child.parent_pause_id = parent.id
        )
        SELECT COUNT(*)
        INTO active_descendants
        FROM descendants
        WHERE state = 'active';

        IF active_descendants <> 0 THEN
            RAISE EXCEPTION 'parent pause cannot resume with active descendants'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.series_id IS NOT NULL THEN
            SELECT action
            INTO latest_action
            FROM resume_decisions
            WHERE pause_id = NEW.id
            ORDER BY decision_number DESC
            LIMIT 1;

            IF latest_action <> 'resume' THEN
                RAISE EXCEPTION 'pause resume requires a complete resume decision'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;

        IF NEW.scope_kind = 'game_attempt' THEN
            SELECT resumed_at
            INTO clock_resumed_at
            FROM pause_clocks
            WHERE pause_id = NEW.id;

            IF clock_resumed_at IS NULL THEN
                RAISE EXCEPTION 'Game resume requires restored frozen time'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: pause_presence_snapshot_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.pause_presence_snapshot_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    pause_series_id UUID;
    pause_roster_id UUID;
    pause_tournament_id UUID;
    live_tournament_id UUID;
    live_roster_id UUID;
    live_state VARCHAR(16);
    live_epoch BIGINT;
    live_revision BIGINT;
BEGIN
    SELECT series_id, roster_id, tournament_id
    INTO pause_series_id, pause_roster_id, pause_tournament_id
    FROM pauses
    WHERE id = NEW.pause_id
    FOR UPDATE;

    IF pause_roster_id IS DISTINCT FROM NEW.roster_id
        OR (
            pause_series_id IS NOT NULL
            AND pause_series_id IS DISTINCT FROM NEW.series_id
        ) THEN
        RAISE EXCEPTION 'pause snapshot crosses roster boundary'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT tournament_id, roster_id, state, presence_epoch, revision
    INTO live_tournament_id, live_roster_id, live_state, live_epoch, live_revision
    FROM presence_states
    WHERE series_id = NEW.series_id AND participant_id = NEW.participant_id
    FOR UPDATE;

    IF live_tournament_id IS DISTINCT FROM pause_tournament_id
        OR live_roster_id IS DISTINCT FROM pause_roster_id
        OR live_state IS DISTINCT FROM NEW.presence_state
        OR live_epoch IS DISTINCT FROM NEW.presence_epoch
        OR live_revision IS DISTINCT FROM NEW.presence_revision THEN
        RAISE EXCEPTION 'pre-pause snapshot must capture current presence CAS'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: pause_resume_current_evidence_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.pause_resume_current_evidence_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    latest_decision resume_decisions%ROWTYPE;
    first_live presence_states%ROWTYPE;
    second_live presence_states%ROWTYPE;
    open_interval_count INTEGER;
BEGIN
    IF NEW.series_id IS NULL THEN
        RETURN NEW;
    END IF;

    SELECT *
    INTO latest_decision
    FROM resume_decisions
    WHERE pause_id = NEW.id
    ORDER BY decision_number DESC
    LIMIT 1;

    IF latest_decision.id IS NULL
        OR latest_decision.action IS DISTINCT FROM 'resume' THEN
        RAISE EXCEPTION 'pause resume requires a current resume decision'
            USING ERRCODE = 'check_violation';
    END IF;

    PERFORM 1
    FROM reconnect_intervals
    WHERE pause_id = NEW.id
    ORDER BY participant_id, interval_number, id
    FOR UPDATE;

    SELECT COUNT(*)
    INTO open_interval_count
    FROM reconnect_intervals
    WHERE pause_id = NEW.id AND state = 'open';

    SELECT *
    INTO first_live
    FROM presence_states
    WHERE series_id = NEW.series_id
        AND participant_id = latest_decision.first_participant_id
    FOR UPDATE;

    SELECT *
    INTO second_live
    FROM presence_states
    WHERE series_id = NEW.series_id
        AND participant_id = latest_decision.second_participant_id
    FOR UPDATE;

    IF open_interval_count <> 0
        OR first_live.id IS NULL
        OR second_live.id IS NULL
        OR first_live.state IS DISTINCT FROM 'connected'
        OR second_live.state IS DISTINCT FROM 'connected'
        OR latest_decision.first_live_state IS DISTINCT FROM first_live.state
        OR latest_decision.second_live_state IS DISTINCT FROM second_live.state
        OR latest_decision.first_presence_epoch IS DISTINCT FROM first_live.presence_epoch
        OR latest_decision.second_presence_epoch IS DISTINCT FROM second_live.presence_epoch
        OR latest_decision.first_presence_revision IS DISTINCT FROM first_live.revision
        OR latest_decision.second_presence_revision IS DISTINCT FROM second_live.revision THEN
        RAISE EXCEPTION 'pause resume decision does not match current reconnect evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: pause_revision_insert_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.pause_revision_insert_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    previous_number BIGINT;
BEGIN
    PERFORM 1 FROM pauses WHERE id = NEW.pause_id FOR UPDATE;

    IF NEW.previous_revision_id IS NULL THEN
        IF NEW.revision_number <> 1 OR NEW.state <> 'active' THEN
            RAISE EXCEPTION 'pause lineage must start active at revision one'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSE
        SELECT revision_number
        INTO previous_number
        FROM pause_revisions
        WHERE id = NEW.previous_revision_id;

        IF previous_number IS NULL OR NEW.revision_number <> previous_number + 1 THEN
            RAISE EXCEPTION 'pause revisions must be contiguous'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: pause_terminal_descendant_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.pause_terminal_descendant_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF EXISTS (
        WITH RECURSIVE pause_descendants AS (
            SELECT id, state
            FROM pauses
            WHERE parent_pause_id = NEW.id

            UNION ALL

            SELECT child.id, child.state
            FROM pauses AS child
            JOIN pause_descendants AS parent ON child.parent_pause_id = parent.id
        )
        SELECT 1
        FROM pause_descendants
        WHERE state = 'active'
    ) THEN
        RAISE EXCEPTION 'pause cannot terminate with active descendants'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: presence_state_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.presence_state_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    series_first UUID;
    series_second UUID;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'live presence is durable recovery state'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_OP = 'INSERT' THEN
        SELECT first_participant_id, second_participant_id
        INTO series_first, series_second
        FROM series
        WHERE id = NEW.series_id;

        IF NEW.participant_id NOT IN (series_first, series_second) THEN
            RAISE EXCEPTION 'presence participant is outside the Series'
                USING ERRCODE = 'check_violation';
        END IF;

        RETURN NEW;
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.participant_id IS DISTINCT FROM OLD.participant_id
        OR NEW.state IS NOT DISTINCT FROM OLD.state
        OR NEW.presence_epoch <> OLD.presence_epoch + 1
        OR NEW.revision <> OLD.revision + 1
        OR NEW.updated_at <= OLD.updated_at THEN
        RAISE EXCEPTION 'invalid presence CAS transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'connected'
        AND NEW.connected_at <= OLD.connected_at THEN
        RAISE EXCEPTION 'reconnect must advance connected_at'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'disconnected'
        AND NEW.connected_at IS DISTINCT FROM OLD.connected_at THEN
        RAISE EXCEPTION 'disconnect cannot rewrite connection start'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: reconnect_interval_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.reconnect_interval_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    owning_pause pauses%ROWTYPE;
    suspending_pause pauses%ROWTYPE;
    predecessor reconnect_intervals%ROWTYPE;
    live_presence presence_states%ROWTYPE;
    pause_snapshot pause_presence_snapshots%ROWTYPE;
    counter_slots SMALLINT;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'reconnect intervals are durable history'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_OP = 'INSERT' THEN
        -- Let the named lineage constraint report the two nullability axes and
        -- let the named foreign key report a missing predecessor.
        IF (
            NEW.continuation_number = 0
            AND NEW.continued_from_id IS NOT NULL
        ) OR (
            NEW.continuation_number > 0
            AND NEW.continued_from_id IS NULL
        ) THEN
            RETURN NEW;
        END IF;

        IF NEW.continuation_number = 0
            AND NEW.suspended_by_pause_id IS NOT NULL THEN
            RAISE EXCEPTION
                'reconnect interval insert cannot carry suspension provenance'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.state = 'open' AND NEW.suspended_by_pause_id IS NOT NULL THEN
            RAISE EXCEPTION
                'open reconnect continuation cannot carry suspension provenance'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.continuation_number > 0
            AND (
                NEW.state IS DISTINCT FROM 'open'
                OR NEW.closed_at IS NOT NULL
                OR NEW.suspended_by_pause_id IS NOT NULL
                OR NEW.revision IS DISTINCT FROM 1
                OR NEW.created_at IS DISTINCT FROM NEW.opened_at
                OR NEW.updated_at IS DISTINCT FROM NEW.opened_at
            ) THEN
            RAISE EXCEPTION
                'reconnect continuation must start as a canonical open segment'
                USING ERRCODE = 'check_violation';
        END IF;

        -- The owning Game pause remains the first explicit lock for reconnect
        -- writers and is always locked before a continuation predecessor.
        SELECT *
        INTO owning_pause
        FROM pauses
        WHERE id = NEW.pause_id
            AND series_id = NEW.series_id
            AND roster_id = NEW.roster_id
        FOR UPDATE;

        IF NEW.continuation_number = 0 THEN
            IF owning_pause.state IS DISTINCT FROM 'active'
                OR owning_pause.scope_kind IS DISTINCT FROM 'game_attempt'
                OR owning_pause.game_attempt_id
                    IS DISTINCT FROM NEW.game_attempt_id
                OR NEW.opened_at < owning_pause.started_at THEN
                RAISE EXCEPTION
                    'reconnect interval requires its active Game pause'
                    USING ERRCODE = 'check_violation';
            END IF;

            IF EXISTS (
                SELECT 1
                FROM pauses AS normal_pause
                JOIN pause_presence_snapshots AS snapshot
                    ON snapshot.pause_id = normal_pause.id
                    AND snapshot.roster_id = NEW.roster_id
                    AND snapshot.series_id = NEW.series_id
                    AND snapshot.participant_id = NEW.participant_id
                WHERE normal_pause.scope_kind = 'wave'
                    AND normal_pause.parent_pause_id IS NULL
                    AND normal_pause.roster_id = NEW.roster_id
                    AND NEW.opened_at < normal_pause.started_at
                    AND normal_pause.started_at < NEW.deadline_at
                    AND (
                        NEW.closed_at IS NULL
                        OR NEW.closed_at >= normal_pause.started_at
                    )
            ) THEN
                RAISE EXCEPTION
                    'reconnect interval cannot backdate across normal Wave pause history'
                    USING ERRCODE = 'check_violation';
            END IF;

            SELECT *
            INTO live_presence
            FROM presence_states
            WHERE series_id = NEW.series_id
                AND participant_id = NEW.participant_id
            FOR UPDATE;

            SELECT slots_used
            INTO counter_slots
            FROM reconnect_slot_counters
            WHERE pause_id = NEW.pause_id
                AND participant_id = NEW.participant_id
            FOR UPDATE;

            IF live_presence.state IS DISTINCT FROM 'disconnected'
                OR live_presence.presence_epoch
                    IS DISTINCT FROM NEW.presence_epoch
                OR NEW.interval_number IS DISTINCT FROM counter_slots + 1 THEN
                RAISE EXCEPTION
                    'reconnect interval must consume the next live slot'
                    USING ERRCODE = 'check_violation';
            END IF;

            RETURN NEW;
        END IF;

        SELECT *
        INTO predecessor
        FROM reconnect_intervals
        WHERE id = NEW.continued_from_id
        FOR UPDATE;

        IF NOT FOUND THEN
            RETURN NEW;
        END IF;

        IF NEW.pause_id IS DISTINCT FROM predecessor.pause_id
            OR NEW.roster_id IS DISTINCT FROM predecessor.roster_id
            OR NEW.series_id IS DISTINCT FROM predecessor.series_id
            OR NEW.game_attempt_id
                IS DISTINCT FROM predecessor.game_attempt_id
            OR NEW.participant_id
                IS DISTINCT FROM predecessor.participant_id
            OR NEW.presence_epoch
                IS DISTINCT FROM predecessor.presence_epoch
            OR NEW.interval_number
                IS DISTINCT FROM predecessor.interval_number
            OR NEW.continuation_number
                IS DISTINCT FROM predecessor.continuation_number + 1 THEN
            RAISE EXCEPTION
                'reconnect continuation does not match predecessor identity'
                USING ERRCODE = 'check_violation';
        END IF;

        IF predecessor.state IS DISTINCT FROM 'cancelled'
            OR predecessor.closed_at IS NULL
            OR predecessor.suspended_by_pause_id IS NULL THEN
            RAISE EXCEPTION
                'reconnect continuation requires a cancelled predecessor with suspension provenance'
                USING ERRCODE = 'check_violation';
        END IF;

        IF owning_pause.state IS DISTINCT FROM 'active'
            OR owning_pause.scope_kind IS DISTINCT FROM 'game_attempt'
            OR owning_pause.game_attempt_id
                IS DISTINCT FROM NEW.game_attempt_id
            OR NEW.opened_at < owning_pause.started_at THEN
            RAISE EXCEPTION
                'reconnect interval requires its active Game pause'
                USING ERRCODE = 'check_violation';
        END IF;

        IF EXISTS (
            SELECT 1
            FROM pauses AS normal_pause
            JOIN pause_presence_snapshots AS snapshot
                ON snapshot.pause_id = normal_pause.id
                AND snapshot.roster_id = NEW.roster_id
                AND snapshot.series_id = NEW.series_id
                AND snapshot.participant_id = NEW.participant_id
            WHERE normal_pause.scope_kind = 'wave'
                AND normal_pause.parent_pause_id IS NULL
                AND normal_pause.roster_id = NEW.roster_id
                AND NEW.opened_at < normal_pause.started_at
                AND normal_pause.started_at < NEW.deadline_at
                AND (
                    NEW.closed_at IS NULL
                    OR NEW.closed_at >= normal_pause.started_at
                )
        ) THEN
            RAISE EXCEPTION
                'reconnect interval cannot backdate across normal Wave pause history'
                USING ERRCODE = 'check_violation';
        END IF;

        SELECT *
        INTO suspending_pause
        FROM pauses
        WHERE id = predecessor.suspended_by_pause_id
        FOR UPDATE;

        IF suspending_pause.state IS DISTINCT FROM 'active'
            OR suspending_pause.scope_kind IS DISTINCT FROM 'wave'
            OR suspending_pause.parent_pause_id IS NOT NULL
            OR suspending_pause.tournament_id
                IS DISTINCT FROM owning_pause.tournament_id
            OR suspending_pause.roster_id IS DISTINCT FROM NEW.roster_id
            OR predecessor.closed_at
                IS DISTINCT FROM suspending_pause.started_at THEN
            RAISE EXCEPTION
                'reconnect continuation requires a cancelled predecessor with suspension provenance'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.opened_at <= predecessor.closed_at
            OR NEW.deadline_at IS DISTINCT FROM NEW.opened_at
                + (predecessor.deadline_at - predecessor.closed_at) THEN
            RAISE EXCEPTION
                'reconnect continuation does not match suspended predecessor'
                USING ERRCODE = 'check_violation';
        END IF;

        SELECT *
        INTO live_presence
        FROM presence_states
        WHERE series_id = NEW.series_id
            AND participant_id = NEW.participant_id
        FOR UPDATE;

        SELECT slots_used
        INTO counter_slots
        FROM reconnect_slot_counters
        WHERE pause_id = NEW.pause_id
            AND participant_id = NEW.participant_id
        FOR UPDATE;

        IF live_presence.state IS DISTINCT FROM 'disconnected'
            OR live_presence.presence_epoch
                IS DISTINCT FROM NEW.presence_epoch
            OR counter_slots IS NULL
            OR counter_slots < NEW.interval_number THEN
            RAISE EXCEPTION
                'reconnect continuation does not match predecessor identity'
                USING ERRCODE = 'check_violation';
        END IF;

        RETURN NEW;
    END IF;

    IF NEW.suspended_by_pause_id IS DISTINCT FROM OLD.suspended_by_pause_id
        AND NOT (
            OLD.state = 'open'
            AND OLD.suspended_by_pause_id IS NULL
            AND NEW.state = 'cancelled'
            AND NEW.suspended_by_pause_id IS NOT NULL
        ) THEN
        RAISE EXCEPTION 'reconnect suspension provenance is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.pause_id IS DISTINCT FROM OLD.pause_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.game_attempt_id IS DISTINCT FROM OLD.game_attempt_id
        OR NEW.participant_id IS DISTINCT FROM OLD.participant_id
        OR NEW.presence_epoch IS DISTINCT FROM OLD.presence_epoch
        OR NEW.interval_number IS DISTINCT FROM OLD.interval_number
        OR NEW.continuation_number IS DISTINCT FROM OLD.continuation_number
        OR NEW.continued_from_id IS DISTINCT FROM OLD.continued_from_id
        OR NEW.opened_at IS DISTINCT FROM OLD.opened_at
        OR NEW.deadline_at IS DISTINCT FROM OLD.deadline_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR OLD.state <> 'open'
        OR NEW.state NOT IN ('reconnected', 'expired', 'cancelled')
        OR NEW.revision <> OLD.revision + 1
        OR NEW.updated_at <= OLD.updated_at THEN
        RAISE EXCEPTION 'invalid reconnect interval CAS transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'cancelled' THEN
        IF OLD.suspended_by_pause_id IS NOT NULL THEN
            RAISE EXCEPTION 'reconnect suspension provenance is immutable'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.suspended_by_pause_id IS NULL THEN
            -- A terminal cancellation outside a normal Wave pause cannot be
            -- used as a continuation predecessor.
            RETURN NEW;
        END IF;

        SELECT *
        INTO owning_pause
        FROM pauses
        WHERE id = OLD.pause_id
            AND series_id = OLD.series_id
            AND roster_id = OLD.roster_id;

        SELECT *
        INTO suspending_pause
        FROM pauses
        WHERE id = NEW.suspended_by_pause_id
        FOR UPDATE;

        SELECT *
        INTO pause_snapshot
        FROM pause_presence_snapshots
        WHERE pause_id = NEW.suspended_by_pause_id
            AND participant_id = OLD.participant_id;

        SELECT *
        INTO live_presence
        FROM presence_states
        WHERE series_id = OLD.series_id
            AND participant_id = OLD.participant_id
        FOR UPDATE;

        IF owning_pause.state IS DISTINCT FROM 'active'
            OR owning_pause.scope_kind IS DISTINCT FROM 'game_attempt'
            OR owning_pause.game_attempt_id
                IS DISTINCT FROM OLD.game_attempt_id
            OR OLD.opened_at < owning_pause.started_at
            OR suspending_pause.state IS DISTINCT FROM 'active'
            OR suspending_pause.scope_kind IS DISTINCT FROM 'wave'
            OR suspending_pause.parent_pause_id IS NOT NULL
            OR suspending_pause.tournament_id
                IS DISTINCT FROM owning_pause.tournament_id
            OR suspending_pause.roster_id IS DISTINCT FROM OLD.roster_id
            OR suspending_pause.started_at <= OLD.opened_at
            OR suspending_pause.started_at >= OLD.deadline_at
            OR NEW.closed_at IS DISTINCT FROM suspending_pause.started_at
            OR NEW.updated_at IS DISTINCT FROM NEW.closed_at
            OR NOT EXISTS (
                SELECT 1
                FROM wave_members AS member
                WHERE member.wave_id = suspending_pause.wave_id
                    AND member.roster_id = OLD.roster_id
                    AND member.participant_id = OLD.participant_id
            )
            OR pause_snapshot.pause_id IS NULL
            OR pause_snapshot.roster_id IS DISTINCT FROM OLD.roster_id
            OR pause_snapshot.series_id IS DISTINCT FROM OLD.series_id
            OR pause_snapshot.presence_state IS DISTINCT FROM 'disconnected'
            OR pause_snapshot.presence_epoch
                IS DISTINCT FROM OLD.presence_epoch
            OR live_presence.state IS DISTINCT FROM 'disconnected'
            OR live_presence.presence_epoch
                IS DISTINCT FROM OLD.presence_epoch
            OR pause_snapshot.presence_revision
                IS DISTINCT FROM live_presence.revision THEN
            RAISE EXCEPTION
                'reconnect cancellation requires a covering active normal Wave pause'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSE
        IF NEW.suspended_by_pause_id IS NOT NULL THEN
            RAISE EXCEPTION 'reconnect suspension provenance is immutable'
                USING ERRCODE = 'check_violation';
        END IF;

        SELECT *
        INTO live_presence
        FROM presence_states
        WHERE series_id = NEW.series_id
            AND participant_id = NEW.participant_id
        FOR UPDATE;

        IF live_presence.state IS DISTINCT FROM 'disconnected'
            OR live_presence.presence_epoch
                IS DISTINCT FROM NEW.presence_epoch THEN
            RAISE EXCEPTION
                'reconnect terminal transition has stale presence CAS'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: reconnect_slot_counter_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.reconnect_slot_counter_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'reconnect slot counters are durable CAS state'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.pause_id IS DISTINCT FROM OLD.pause_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.participant_id IS DISTINCT FROM OLD.participant_id
        OR NEW.slot_limit IS DISTINCT FROM OLD.slot_limit
        OR NEW.slots_used <> OLD.slots_used + 1
        OR NEW.revision <> OLD.revision + 1
        OR NEW.updated_at <= OLD.updated_at THEN
        RAISE EXCEPTION 'invalid reconnect slot CAS transition'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: resume_decision_insert_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.resume_decision_insert_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    pause_row pauses%ROWTYPE;
    series_first UUID;
    series_second UUID;
    first_snapshot_state VARCHAR(16);
    second_snapshot_state VARCHAR(16);
    first_live presence_states%ROWTYPE;
    second_live presence_states%ROWTYPE;
    first_interval_state VARCHAR(16);
    second_interval_state VARCHAR(16);
    previous_number BIGINT;
BEGIN
    SELECT *
    INTO pause_row
    FROM pauses
    WHERE id = NEW.pause_id
    FOR UPDATE;

    IF pause_row.state <> 'active' OR pause_row.series_id IS NULL THEN
        RAISE EXCEPTION 'resume decision requires an active Series pause'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT first_participant_id, second_participant_id
    INTO series_first, series_second
    FROM series
    WHERE id = pause_row.series_id;

    SELECT presence_state
    INTO first_snapshot_state
    FROM pause_presence_snapshots
    WHERE pause_id = NEW.pause_id AND participant_id = NEW.first_participant_id;

    SELECT presence_state
    INTO second_snapshot_state
    FROM pause_presence_snapshots
    WHERE pause_id = NEW.pause_id AND participant_id = NEW.second_participant_id;

    SELECT state
    INTO first_interval_state
    FROM reconnect_intervals
    WHERE id = NEW.first_reconnect_interval_id
    FOR UPDATE;

    SELECT state
    INTO second_interval_state
    FROM reconnect_intervals
    WHERE id = NEW.second_reconnect_interval_id
    FOR UPDATE;

    SELECT *
    INTO first_live
    FROM presence_states
    WHERE series_id = pause_row.series_id
        AND participant_id = NEW.first_participant_id
    FOR UPDATE;

    SELECT *
    INTO second_live
    FROM presence_states
    WHERE series_id = pause_row.series_id
        AND participant_id = NEW.second_participant_id
    FOR UPDATE;

    SELECT MAX(decision_number)
    INTO previous_number
    FROM resume_decisions
    WHERE pause_id = NEW.pause_id;

    IF NEW.first_participant_id IS DISTINCT FROM series_first
        OR NEW.second_participant_id IS DISTINCT FROM series_second
        OR NEW.first_pre_pause_state IS DISTINCT FROM first_snapshot_state
        OR NEW.second_pre_pause_state IS DISTINCT FROM second_snapshot_state
        OR NEW.first_live_state IS DISTINCT FROM first_live.state
        OR NEW.second_live_state IS DISTINCT FROM second_live.state
        OR NEW.first_presence_epoch IS DISTINCT FROM first_live.presence_epoch
        OR NEW.second_presence_epoch IS DISTINCT FROM second_live.presence_epoch
        OR NEW.first_presence_revision IS DISTINCT FROM first_live.revision
        OR NEW.second_presence_revision IS DISTINCT FROM second_live.revision
        OR NEW.decision_number <> COALESCE(previous_number, 0) + 1
        OR (
            NEW.first_reconnect_interval_id IS NOT NULL
            AND first_interval_state <> 'open'
        )
        OR (
            NEW.second_reconnect_interval_id IS NOT NULL
            AND second_interval_state <> 'open'
        ) THEN
        RAISE EXCEPTION 'resume decision does not match durable presence evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: validate_game_pause_clock(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.validate_game_pause_clock() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    target_pause_id UUID;
    pause_row pauses%ROWTYPE;
    clock_row pause_clocks%ROWTYPE;
BEGIN
    IF TG_TABLE_NAME = 'pauses' THEN
        IF NEW.scope_kind <> 'game_attempt' THEN
            RETURN NULL;
        END IF;
        target_pause_id := NEW.id;
    ELSE
        target_pause_id := NEW.pause_id;
    END IF;

    SELECT * INTO pause_row FROM pauses WHERE id = target_pause_id;
    SELECT * INTO clock_row FROM pause_clocks WHERE pause_id = target_pause_id;

    IF clock_row.pause_id IS NULL
        OR clock_row.game_attempt_id IS DISTINCT FROM pause_row.game_attempt_id
        OR clock_row.frozen_at IS DISTINCT FROM pause_row.started_at
        OR (pause_row.state = 'active' AND clock_row.resumed_at IS NOT NULL)
        OR (pause_row.state = 'resumed' AND clock_row.resumed_at IS NULL) THEN
        RAISE EXCEPTION 'Game pause requires complete frozen clock evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

--
-- Name: validate_game_pause_state(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.validate_game_pause_state() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    target_attempt_id UUID;
    game_state VARCHAR(32);
    pause_state VARCHAR(16);
BEGIN
    IF TG_TABLE_NAME = 'pauses' THEN
        IF NEW.scope_kind <> 'game_attempt' THEN
            RETURN NULL;
        END IF;
        target_attempt_id := NEW.game_attempt_id;
    ELSE
        target_attempt_id := NEW.id;
    END IF;

    SELECT state
    INTO game_state
    FROM game_attempts
    WHERE id = target_attempt_id;

    SELECT state
    INTO pause_state
    FROM pauses
    WHERE scope_kind = 'game_attempt' AND game_attempt_id = target_attempt_id
    ORDER BY created_at DESC, id DESC
    LIMIT 1;

    IF pause_state = 'active' AND game_state <> 'paused' THEN
        RAISE EXCEPTION 'active Game pause requires paused Game CAS state'
            USING ERRCODE = 'check_violation';
    END IF;

    IF pause_state = 'resumed' AND game_state = 'paused' THEN
        RAISE EXCEPTION 'resumed Game pause requires restored Game CAS state'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

--
-- Name: validate_normal_wave_reconnect_suspension(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.validate_normal_wave_reconnect_suspension() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_TABLE_NAME = 'pauses' THEN
        IF NEW.state IS DISTINCT FROM 'active'
            OR NEW.scope_kind IS DISTINCT FROM 'wave'
            OR NEW.parent_pause_id IS NOT NULL THEN
            RETURN NULL;
        END IF;

        IF EXISTS (
            SELECT 1
            FROM wave_members AS member
            WHERE member.wave_id = NEW.wave_id
                AND member.roster_id = NEW.roster_id
                AND NOT EXISTS (
                    SELECT 1
                    FROM pause_presence_snapshots AS snapshot
                    JOIN presence_states AS presence
                        ON presence.series_id = snapshot.series_id
                        AND presence.roster_id = snapshot.roster_id
                        AND presence.participant_id = snapshot.participant_id
                    WHERE snapshot.pause_id = NEW.id
                        AND snapshot.roster_id = member.roster_id
                        AND snapshot.participant_id = member.participant_id
                )
        ) OR EXISTS (
            SELECT 1
            FROM pause_presence_snapshots AS snapshot
            WHERE snapshot.pause_id = NEW.id
                AND NOT EXISTS (
                    SELECT 1
                    FROM wave_members AS member
                    WHERE member.wave_id = NEW.wave_id
                        AND member.roster_id = snapshot.roster_id
                        AND member.participant_id = snapshot.participant_id
                )
        ) THEN
            RAISE EXCEPTION
                'normal Wave pause requires exact membership snapshot coverage'
                USING ERRCODE = 'check_violation';
        END IF;

        IF EXISTS (
            SELECT 1
            FROM pause_presence_snapshots AS snapshot
            JOIN reconnect_intervals AS reconnect_interval
                ON reconnect_interval.roster_id = snapshot.roster_id
                AND reconnect_interval.series_id = snapshot.series_id
                AND reconnect_interval.participant_id = snapshot.participant_id
            WHERE snapshot.pause_id = NEW.id
                AND reconnect_interval.opened_at < NEW.started_at
                AND NEW.started_at < reconnect_interval.deadline_at
                AND (
                    reconnect_interval.closed_at IS NULL
                    OR reconnect_interval.closed_at >= NEW.started_at
                )
                AND NOT (
                    reconnect_interval.state = 'cancelled'
                    AND reconnect_interval.closed_at
                        IS NOT DISTINCT FROM NEW.started_at
                    AND reconnect_interval.suspended_by_pause_id
                        IS NOT DISTINCT FROM NEW.id
                    AND reconnect_interval.updated_at
                        IS NOT DISTINCT FROM NEW.started_at
                )
        ) THEN
            RAISE EXCEPTION
                'normal Wave pause requires atomic reconnect suspension'
                USING ERRCODE = 'check_violation';
        END IF;

        RETURN NULL;
    END IF;

    IF EXISTS (
        SELECT 1
        FROM reconnect_intervals AS current_interval
        CROSS JOIN pauses AS normal_pause
        JOIN pause_presence_snapshots AS snapshot
            ON snapshot.pause_id = normal_pause.id
            AND snapshot.roster_id = current_interval.roster_id
            AND snapshot.series_id = current_interval.series_id
            AND snapshot.participant_id = current_interval.participant_id
        WHERE normal_pause.scope_kind = 'wave'
            AND normal_pause.parent_pause_id IS NULL
            AND normal_pause.roster_id = current_interval.roster_id
            AND current_interval.id = NEW.id
            AND current_interval.opened_at < normal_pause.started_at
            AND normal_pause.started_at < current_interval.deadline_at
            AND (
                current_interval.closed_at IS NULL
                OR current_interval.closed_at >= normal_pause.started_at
            )
            AND NOT (
                current_interval.state = 'cancelled'
                AND current_interval.closed_at
                    IS NOT DISTINCT FROM normal_pause.started_at
                AND current_interval.suspended_by_pause_id
                    IS NOT DISTINCT FROM normal_pause.id
                AND current_interval.updated_at
                    IS NOT DISTINCT FROM normal_pause.started_at
            )
    ) THEN
        RAISE EXCEPTION
            'reconnect interval cannot backdate across normal Wave pause history'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

--
-- Name: validate_pause_presence_snapshot(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.validate_pause_presence_snapshot() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    target_pause_id UUID;
    pause_row pauses%ROWTYPE;
    expected_count INTEGER;
    actual_count INTEGER;
    invalid_count INTEGER;
BEGIN
    IF TG_TABLE_NAME = 'pauses' THEN
        target_pause_id := NEW.id;
    ELSE
        target_pause_id := NEW.pause_id;
    END IF;

    SELECT * INTO pause_row FROM pauses WHERE id = target_pause_id;

    IF pause_row.scope_kind IN ('series', 'game_attempt') THEN
        expected_count := 2;
        SELECT COUNT(*)
        INTO invalid_count
        FROM pause_presence_snapshots AS snapshot
        JOIN series AS series ON series.id = pause_row.series_id
        WHERE snapshot.pause_id = target_pause_id
            AND snapshot.participant_id NOT IN (
                series.first_participant_id,
                series.second_participant_id
            );
    ELSIF pause_row.scope_kind = 'wave' THEN
        SELECT COUNT(*) INTO expected_count
        FROM wave_members
        WHERE wave_id = pause_row.wave_id;

        SELECT COUNT(*) INTO invalid_count
        FROM pause_presence_snapshots AS snapshot
        WHERE snapshot.pause_id = target_pause_id
            AND NOT EXISTS (
                SELECT 1
                FROM wave_members AS member
                WHERE member.wave_id = pause_row.wave_id
                    AND member.participant_id = snapshot.participant_id
            );
    ELSE
        SELECT COUNT(*) INTO expected_count
        FROM participants
        WHERE roster_id = pause_row.roster_id AND attendance <> 'withdrawn';

        SELECT COUNT(*) INTO invalid_count
        FROM pause_presence_snapshots AS snapshot
        WHERE snapshot.pause_id = target_pause_id
            AND NOT EXISTS (
                SELECT 1
                FROM participants AS participant
                WHERE participant.roster_id = pause_row.roster_id
                    AND participant.id = snapshot.participant_id
                    AND participant.attendance <> 'withdrawn'
            );
    END IF;

    SELECT COUNT(*) INTO actual_count
    FROM pause_presence_snapshots
    WHERE pause_id = target_pause_id;

    IF expected_count < 1 OR actual_count <> expected_count OR invalid_count <> 0 THEN
        RAISE EXCEPTION 'pause requires a complete pre-pause presence snapshot'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

--
-- Name: validate_pause_revision(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.validate_pause_revision() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    target_pause_id UUID;
    pause_row pauses%ROWTYPE;
    revision_row pause_revisions%ROWTYPE;
BEGIN
    IF TG_TABLE_NAME = 'pauses' THEN
        target_pause_id := NEW.id;
    ELSE
        target_pause_id := NEW.pause_id;
    END IF;

    SELECT * INTO pause_row FROM pauses WHERE id = target_pause_id;
    SELECT *
    INTO revision_row
    FROM pause_revisions
    WHERE id = pause_row.current_revision_id;

    IF revision_row.id IS NULL
        OR revision_row.pause_id IS DISTINCT FROM pause_row.id
        OR revision_row.revision_number IS DISTINCT FROM pause_row.revision
        OR revision_row.state IS DISTINCT FROM pause_row.state
        OR (
            TG_TABLE_NAME = 'pause_revisions'
            AND revision_row.id IS DISTINCT FROM NEW.id
        ) THEN
        RAISE EXCEPTION 'pause current revision is inconsistent'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

--
-- Name: validate_reconnect_slot_count(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.validate_reconnect_slot_count() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    target_pause_id UUID;
    target_participant_id UUID;
    used_slots SMALLINT;
    root_count INTEGER;
    distinct_root_count INTEGER;
    first_root_number INTEGER;
    last_root_number INTEGER;
BEGIN
    target_pause_id := NEW.pause_id;
    target_participant_id := NEW.participant_id;

    SELECT slots_used
    INTO used_slots
    FROM reconnect_slot_counters
    WHERE pause_id = target_pause_id
        AND participant_id = target_participant_id;

    SELECT
        COUNT(*),
        COUNT(DISTINCT interval_number),
        MIN(interval_number),
        MAX(interval_number)
    INTO
        root_count,
        distinct_root_count,
        first_root_number,
        last_root_number
    FROM reconnect_intervals
    WHERE pause_id = target_pause_id
        AND participant_id = target_participant_id
        AND continuation_number = 0;

    IF used_slots IS DISTINCT FROM root_count THEN
        RAISE EXCEPTION
            'reconnect slot count differs from retained root intervals'
            USING ERRCODE = 'check_violation';
    END IF;

    IF root_count > 0 AND (
        distinct_root_count IS DISTINCT FROM root_count
        OR first_root_number IS DISTINCT FROM 1
        OR last_root_number IS DISTINCT FROM root_count
    ) THEN
        RAISE EXCEPTION
            'reconnect root interval numbers must be contiguous'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

--
-- Name: validate_reconnect_terminal_presence(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.validate_reconnect_terminal_presence() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    live_state VARCHAR(16);
    live_epoch BIGINT;
BEGIN
    IF TG_TABLE_NAME = 'presence_states' THEN
        IF OLD.state = 'disconnected'
            AND NEW.state = 'connected'
            AND EXISTS (
                SELECT 1
                FROM reconnect_intervals AS reconnect_interval
                WHERE reconnect_interval.series_id = NEW.series_id
                    AND reconnect_interval.participant_id = NEW.participant_id
                    AND reconnect_interval.state = 'open'
            ) THEN
            RAISE EXCEPTION 'reconnect presence retains an open interval'
                USING ERRCODE = 'check_violation';
        END IF;

        RETURN NULL;
    END IF;

    IF NEW.state NOT IN ('reconnected', 'expired') THEN
        RETURN NULL;
    END IF;

    SELECT state, presence_epoch
    INTO live_state, live_epoch
    FROM presence_states
    WHERE series_id = NEW.series_id AND participant_id = NEW.participant_id;

    IF (
        NEW.state = 'reconnected'
        AND (
            live_state IS DISTINCT FROM 'connected'
            OR live_epoch IS DISTINCT FROM NEW.presence_epoch + 1
        )
    ) OR (
        NEW.state = 'expired'
        AND (
            live_state IS DISTINCT FROM 'disconnected'
            OR live_epoch IS DISTINCT FROM NEW.presence_epoch
        )
    ) THEN
        RAISE EXCEPTION 'reconnect terminal state differs from live presence CAS'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

--
-- Name: wave_member_normal_pause_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.wave_member_normal_pause_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    old_wave_id UUID;
    new_wave_id UUID;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        old_wave_id := OLD.wave_id;
    END IF;

    IF TG_OP <> 'DELETE' THEN
        new_wave_id := NEW.wave_id;
    END IF;

    PERFORM 1
    FROM waves
    WHERE id = ANY(
        ARRAY_REMOVE(ARRAY[old_wave_id, new_wave_id], NULL)
    )
    ORDER BY id
    FOR UPDATE;

    IF EXISTS (
        SELECT 1
        FROM pauses
        WHERE scope_kind = 'wave'
            AND parent_pause_id IS NULL
            AND wave_id = ANY(
                ARRAY_REMOVE(ARRAY[old_wave_id, new_wave_id], NULL)
            )
    ) THEN
        RAISE EXCEPTION
            'Wave membership is immutable after normal pause history'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

--
-- Name: tournament_execution_revision_snapshot(uuid, uuid); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.tournament_execution_revision_snapshot(target_tournament_id uuid, target_roster_id uuid) RETURNS jsonb
    LANGUAGE sql STABLE
    AS $$
WITH current_drafts AS (
    SELECT DISTINCT ON (draft.id)
        draft.id,
        revision.id AS revision_id,
        revision.revision,
        revision.state,
        revision.service_epoch
    FROM drafts AS draft
    JOIN series ON series.id = draft.series_id
        AND series.roster_id = draft.roster_id
    JOIN draft_revisions AS revision ON revision.draft_id = draft.id
    WHERE series.tournament_id = target_tournament_id
        AND draft.roster_id = target_roster_id
    ORDER BY draft.id, revision.revision DESC, revision.id DESC
), snapshot AS (
    SELECT jsonb_build_object(
        'waves', COALESCE((
            SELECT jsonb_agg(jsonb_build_array(wave.id, wave.revision_id, wave.revision, wave.state) ORDER BY wave.id)
            FROM waves AS wave
            WHERE wave.tournament_id = target_tournament_id AND wave.roster_id = target_roster_id
        ), '[]'::jsonb),
        'series', COALESCE((
            SELECT jsonb_agg(jsonb_build_array(series.id, series.revision, series.state, series.current_score_revision_id, series.current_result_revision_id) ORDER BY series.id)
            FROM series
            WHERE series.tournament_id = target_tournament_id AND series.roster_id = target_roster_id
        ), '[]'::jsonb),
        'games', COALESCE((
            SELECT jsonb_agg(jsonb_build_array(attempt.id, attempt.revision, attempt.state, attempt.result_revision_id) ORDER BY attempt.id)
            FROM game_attempts AS attempt
            JOIN series ON series.id = attempt.series_id AND series.roster_id = attempt.roster_id
            WHERE series.tournament_id = target_tournament_id AND attempt.roster_id = target_roster_id
        ), '[]'::jsonb),
        'drafts', COALESCE((
            SELECT jsonb_agg(jsonb_build_array(draft.id, draft.revision_id, draft.revision, draft.state, draft.service_epoch) ORDER BY draft.id)
            FROM current_drafts AS draft
        ), '[]'::jsonb),
        'ready_windows', COALESCE((
            SELECT jsonb_agg(jsonb_build_array(ready_window.id, ready_window.revision_id, ready_window.state) ORDER BY ready_window.id)
            FROM ready_windows AS ready_window
            JOIN waves AS wave ON wave.id = ready_window.wave_id AND wave.roster_id = ready_window.roster_id
            WHERE wave.tournament_id = target_tournament_id AND ready_window.roster_id = target_roster_id
        ), '[]'::jsonb),
        'readiness', COALESCE((
            SELECT jsonb_agg(jsonb_build_array(readiness.wave_id, readiness.participant_id, readiness.revision, readiness.ready) ORDER BY readiness.wave_id, readiness.participant_id)
            FROM wave_readiness AS readiness
            JOIN waves AS wave ON wave.id = readiness.wave_id AND wave.roster_id = readiness.roster_id
            WHERE wave.tournament_id = target_tournament_id AND readiness.roster_id = target_roster_id
        ), '[]'::jsonb),
        'assignments', COALESCE((
            SELECT jsonb_agg(jsonb_build_array(assignment.id, assignment.attempt_id, assignment.state) ORDER BY assignment.id)
            FROM assignments AS assignment
            JOIN series ON series.id = assignment.series_id AND series.roster_id = assignment.roster_id
            WHERE series.tournament_id = target_tournament_id AND assignment.roster_id = target_roster_id
        ), '[]'::jsonb),
        'child_pauses', COALESCE((
            SELECT jsonb_agg(jsonb_build_array(pause.id, pause.current_revision_id, pause.revision, pause.state) ORDER BY pause.id)
            FROM pauses AS pause
            WHERE pause.tournament_id = target_tournament_id AND pause.roster_id = target_roster_id
                AND pause.scope_kind <> 'tournament'
        ), '[]'::jsonb),
        'reconnect', COALESCE((
            SELECT jsonb_agg(jsonb_build_array(reconnect.id, reconnect.revision, reconnect.state) ORDER BY reconnect.id)
            FROM reconnect_intervals AS reconnect
            JOIN series ON series.id = reconnect.series_id AND series.roster_id = reconnect.roster_id
            WHERE series.tournament_id = target_tournament_id AND reconnect.roster_id = target_roster_id
        ), '[]'::jsonb),
        'golden', COALESCE((
            SELECT jsonb_agg(jsonb_build_array(attempt.id, attempt.attempt_number, attempt.state) ORDER BY attempt.id)
            FROM golden_attempts AS attempt
            WHERE attempt.tournament_id = target_tournament_id AND attempt.roster_id = target_roster_id
        ), '[]'::jsonb),
        'incomplete_count', (
            (SELECT COUNT(*) FROM waves AS wave WHERE wave.tournament_id = target_tournament_id AND wave.roster_id = target_roster_id AND wave.state NOT IN ('completed', 'ready_window_expired', 'superseded'))
            + (SELECT COUNT(*) FROM series WHERE tournament_id = target_tournament_id AND roster_id = target_roster_id AND state NOT IN ('completed', 'cancelled'))
            + (SELECT COUNT(*) FROM game_attempts AS attempt JOIN series ON series.id = attempt.series_id AND series.roster_id = attempt.roster_id WHERE series.tournament_id = target_tournament_id AND attempt.roster_id = target_roster_id AND attempt.state NOT IN ('completed', 'void', 'cancelled', 'superseded'))
            + (SELECT COUNT(*) FROM current_drafts WHERE state NOT IN ('completed', 'superseded'))
            + (SELECT COUNT(*) FROM ready_windows AS ready_window JOIN waves AS wave ON wave.id = ready_window.wave_id AND wave.roster_id = ready_window.roster_id WHERE wave.tournament_id = target_tournament_id AND ready_window.roster_id = target_roster_id AND ready_window.state = 'open')
            + (SELECT COUNT(*) FROM pauses AS pause WHERE pause.tournament_id = target_tournament_id AND pause.roster_id = target_roster_id AND pause.scope_kind <> 'tournament' AND pause.state = 'active')
            + (SELECT COUNT(*) FROM reconnect_intervals AS reconnect JOIN series ON series.id = reconnect.series_id AND series.roster_id = reconnect.roster_id WHERE series.tournament_id = target_tournament_id AND reconnect.roster_id = target_roster_id AND reconnect.state = 'open')
            + (SELECT COUNT(*) FROM golden_attempts AS attempt WHERE attempt.tournament_id = target_tournament_id AND attempt.roster_id = target_roster_id AND attempt.state NOT IN ('completed', 'cancelled', 'superseded'))
        ),
        'active_golden_count', (
            SELECT COUNT(*) FROM golden_attempts AS attempt
            WHERE attempt.tournament_id = target_tournament_id AND attempt.roster_id = target_roster_id
                AND attempt.state IN ('ready', 'active', 'technical_pause')
        )
    ) AS document
)
SELECT document FROM snapshot;
$$;

--
-- Name: pause_clocks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.pause_clocks (
    pause_id uuid NOT NULL,
    game_attempt_id uuid NOT NULL,
    original_deadline timestamp with time zone NOT NULL,
    frozen_at timestamp with time zone NOT NULL,
    frozen_remaining_ms bigint NOT NULL,
    resumed_at timestamp with time zone,
    resumed_deadline timestamp with time zone,
    revision bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT pause_clocks_frozen_check CHECK (((original_deadline > frozen_at) AND (frozen_remaining_ms > 0) AND (frozen_remaining_ms = (floor((EXTRACT(epoch FROM (original_deadline - frozen_at)) * (1000)::numeric)))::bigint))),
    CONSTRAINT pause_clocks_resume_check CHECK ((((resumed_at IS NULL) AND (resumed_deadline IS NULL)) OR ((resumed_at IS NOT NULL) AND (resumed_deadline = (resumed_at + ((frozen_remaining_ms)::double precision * '00:00:00.001'::interval)))))),
    CONSTRAINT pause_clocks_revision_check CHECK ((revision >= 1)),
    CONSTRAINT pause_clocks_timestamps_check CHECK (((created_at >= frozen_at) AND (updated_at >= created_at) AND ((resumed_at IS NULL) OR (resumed_at >= frozen_at))))
);

--
-- Name: pause_presence_snapshots; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.pause_presence_snapshots (
    pause_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    series_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    presence_state character varying(16) NOT NULL,
    presence_epoch bigint NOT NULL,
    presence_revision bigint NOT NULL,
    captured_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT pause_presence_snapshots_revision_check CHECK (((presence_epoch >= 1) AND (presence_revision >= 1))),
    CONSTRAINT pause_presence_snapshots_state_check CHECK (((presence_state)::text = ANY ((ARRAY['connected'::character varying, 'disconnected'::character varying])::text[]))),
    CONSTRAINT pause_presence_snapshots_timestamps_check CHECK ((captured_at <= created_at))
);

--
-- Name: pause_revisions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.pause_revisions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    pause_id uuid NOT NULL,
    previous_revision_id uuid,
    revision_number bigint NOT NULL,
    state character varying(16) NOT NULL,
    transition_reason text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT pause_revisions_number_check CHECK (((revision_number >= 1) AND (((revision_number = 1) AND (previous_revision_id IS NULL)) OR ((revision_number > 1) AND (previous_revision_id IS NOT NULL))))),
    CONSTRAINT pause_revisions_state_check CHECK (((((state)::text = 'active'::text) AND (transition_reason IS NULL)) OR (((state)::text = ANY ((ARRAY['resumed'::character varying, 'cancelled'::character varying])::text[])) AND (transition_reason = btrim(transition_reason)) AND (transition_reason <> ''::text))))
);

--
-- Name: pauses; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.pauses (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    scope_kind character varying(16) NOT NULL,
    scope_id uuid NOT NULL,
    wave_id uuid,
    series_id uuid,
    game_attempt_id uuid,
    parent_pause_id uuid,
    depth smallint DEFAULT 0 NOT NULL,
    reason character varying(32) NOT NULL,
    paused_from_state character varying(32) NOT NULL,
    state character varying(16) DEFAULT 'active'::character varying NOT NULL,
    current_revision_id uuid NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    started_at timestamp with time zone NOT NULL,
    resolved_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT pauses_depth_check CHECK (((depth >= 0) AND (depth <= 3))),
    CONSTRAINT pauses_origin_check CHECK ((((paused_from_state)::text = btrim((paused_from_state)::text)) AND ((paused_from_state)::text <> ''::text))),
    CONSTRAINT pauses_reason_check CHECK (((reason)::text = ANY ((ARRAY['operator'::character varying, 'disconnect'::character varying, 'platform'::character varying, 'execution_epoch'::character varying])::text[]))),
    CONSTRAINT pauses_revision_check CHECK ((revision >= 1)),
    CONSTRAINT pauses_scope_check CHECK (((((scope_kind)::text = 'tournament'::text) AND (scope_id = tournament_id) AND (wave_id IS NULL) AND (series_id IS NULL) AND (game_attempt_id IS NULL)) OR (((scope_kind)::text = 'wave'::text) AND (scope_id = wave_id) AND (wave_id IS NOT NULL) AND (series_id IS NULL) AND (game_attempt_id IS NULL)) OR (((scope_kind)::text = 'series'::text) AND (scope_id = series_id) AND (wave_id IS NULL) AND (series_id IS NOT NULL) AND (game_attempt_id IS NULL)) OR (((scope_kind)::text = 'game_attempt'::text) AND (scope_id = game_attempt_id) AND (wave_id IS NULL) AND (series_id IS NOT NULL) AND (game_attempt_id IS NOT NULL)))),
    CONSTRAINT pauses_state_check CHECK (((((state)::text = 'active'::text) AND (resolved_at IS NULL)) OR (((state)::text = ANY ((ARRAY['resumed'::character varying, 'cancelled'::character varying])::text[])) AND (resolved_at IS NOT NULL)))),
    CONSTRAINT pauses_timestamps_check CHECK (((started_at <= created_at) AND (updated_at >= created_at) AND ((resolved_at IS NULL) OR (resolved_at >= started_at))))
);

--
-- Name: presence_states; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.presence_states (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    series_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    state character varying(16) NOT NULL,
    presence_epoch bigint DEFAULT 1 NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    connected_at timestamp with time zone NOT NULL,
    disconnected_at timestamp with time zone,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT presence_states_revision_check CHECK (((presence_epoch >= 1) AND (revision >= 1))),
    CONSTRAINT presence_states_state_check CHECK (((((state)::text = 'connected'::text) AND (disconnected_at IS NULL)) OR (((state)::text = 'disconnected'::text) AND (disconnected_at IS NOT NULL) AND (disconnected_at >= connected_at)))),
    CONSTRAINT presence_states_timestamp_check CHECK (((updated_at >= connected_at) AND ((disconnected_at IS NULL) OR (updated_at >= disconnected_at))))
);

--
-- Name: reconnect_intervals; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.reconnect_intervals (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    pause_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    series_id uuid NOT NULL,
    game_attempt_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    presence_epoch bigint NOT NULL,
    interval_number smallint NOT NULL,
    state character varying(16) DEFAULT 'open'::character varying NOT NULL,
    opened_at timestamp with time zone NOT NULL,
    deadline_at timestamp with time zone NOT NULL,
    closed_at timestamp with time zone,
    revision bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    continuation_number integer DEFAULT 0 NOT NULL,
    continued_from_id uuid,
    suspended_by_pause_id uuid,
    CONSTRAINT reconnect_intervals_lineage_check CHECK (((continuation_number >= 0) AND (continued_from_id IS DISTINCT FROM id) AND (((continuation_number = 0) AND (continued_from_id IS NULL)) OR ((continuation_number > 0) AND (continued_from_id IS NOT NULL))))),
    CONSTRAINT reconnect_intervals_revision_check CHECK (((presence_epoch >= 1) AND (interval_number >= 1) AND (revision >= 1))),
    CONSTRAINT reconnect_intervals_state_check CHECK (((((state)::text = 'open'::text) AND (closed_at IS NULL)) OR (((state)::text = ANY ((ARRAY['reconnected'::character varying, 'expired'::character varying, 'cancelled'::character varying])::text[])) AND (closed_at IS NOT NULL)))),
    CONSTRAINT reconnect_intervals_suspension_check CHECK (((suspended_by_pause_id IS NULL) OR ((state)::text = 'cancelled'::text))),
    CONSTRAINT reconnect_intervals_terminal_time_check CHECK ((((state)::text = 'open'::text) OR ((closed_at <= updated_at) AND ((((state)::text = 'reconnected'::text) AND (closed_at <= deadline_at)) OR (((state)::text = 'expired'::text) AND (closed_at >= deadline_at)) OR ((state)::text = 'cancelled'::text))))),
    CONSTRAINT reconnect_intervals_timestamps_check CHECK (((deadline_at > opened_at) AND (created_at >= opened_at) AND (updated_at >= created_at) AND ((closed_at IS NULL) OR (closed_at >= opened_at))))
);

--
-- Name: COLUMN reconnect_intervals.continuation_number; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.reconnect_intervals.continuation_number IS 'Zero for a slot-charging root and positive for resumed segments of that root';

--
-- Name: COLUMN reconnect_intervals.continued_from_id; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.reconnect_intervals.continued_from_id IS 'Immediate predecessor segment; null only for a root segment';

--
-- Name: COLUMN reconnect_intervals.suspended_by_pause_id; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.reconnect_intervals.suspended_by_pause_id IS 'Normal Wave pause that atomically cancelled this segment';

--
-- Name: reconnect_slot_counters; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.reconnect_slot_counters (
    pause_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    slot_limit smallint NOT NULL,
    slots_used smallint DEFAULT 0 NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT reconnect_slot_counters_timestamps_check CHECK ((updated_at >= created_at)),
    CONSTRAINT reconnect_slot_counters_value_check CHECK ((((slot_limit >= 1) AND (slot_limit <= 10)) AND ((slots_used >= 0) AND (slots_used <= slot_limit)) AND (revision >= 1)))
);

--
-- Name: resume_decisions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.resume_decisions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    pause_id uuid NOT NULL,
    decision_number bigint NOT NULL,
    first_participant_id uuid NOT NULL,
    second_participant_id uuid NOT NULL,
    first_pre_pause_state character varying(16) NOT NULL,
    second_pre_pause_state character varying(16) NOT NULL,
    first_live_state character varying(16) NOT NULL,
    second_live_state character varying(16) NOT NULL,
    first_presence_epoch bigint NOT NULL,
    second_presence_epoch bigint NOT NULL,
    first_presence_revision bigint NOT NULL,
    second_presence_revision bigint NOT NULL,
    first_reconnect_interval_id uuid,
    second_reconnect_interval_id uuid,
    action character varying(16) NOT NULL,
    decided_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT resume_decisions_interval_check CHECK (((((first_live_state)::text = 'connected'::text) AND (first_reconnect_interval_id IS NULL)) OR (((first_live_state)::text = 'disconnected'::text) AND (first_reconnect_interval_id IS NOT NULL)))),
    CONSTRAINT resume_decisions_matrix_check CHECK (((((first_live_state)::text = 'connected'::text) AND ((second_live_state)::text = 'connected'::text) AND ((action)::text = 'resume'::text)) OR (((first_live_state)::text = 'disconnected'::text) AND ((second_live_state)::text = 'connected'::text) AND ((action)::text = 'wait_first'::text)) OR (((first_live_state)::text = 'connected'::text) AND ((second_live_state)::text = 'disconnected'::text) AND ((action)::text = 'wait_second'::text)) OR (((first_live_state)::text = 'disconnected'::text) AND ((second_live_state)::text = 'disconnected'::text) AND ((action)::text = 'wait_both'::text)))),
    CONSTRAINT resume_decisions_participants_check CHECK ((first_participant_id <> second_participant_id)),
    CONSTRAINT resume_decisions_revision_check CHECK (((decision_number >= 1) AND (first_presence_epoch >= 1) AND (second_presence_epoch >= 1) AND (first_presence_revision >= 1) AND (second_presence_revision >= 1))),
    CONSTRAINT resume_decisions_second_interval_check CHECK (((((second_live_state)::text = 'connected'::text) AND (second_reconnect_interval_id IS NULL)) OR (((second_live_state)::text = 'disconnected'::text) AND (second_reconnect_interval_id IS NOT NULL)))),
    CONSTRAINT resume_decisions_state_check CHECK ((((first_pre_pause_state)::text = ANY ((ARRAY['connected'::character varying, 'disconnected'::character varying])::text[])) AND ((second_pre_pause_state)::text = ANY ((ARRAY['connected'::character varying, 'disconnected'::character varying])::text[])) AND ((first_live_state)::text = ANY ((ARRAY['connected'::character varying, 'disconnected'::character varying])::text[])) AND ((second_live_state)::text = ANY ((ARRAY['connected'::character varying, 'disconnected'::character varying])::text[])))),
    CONSTRAINT resume_decisions_timestamps_check CHECK ((decided_at <= created_at))
);

--
-- Name: pause_clocks pause_clocks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pause_clocks
    ADD CONSTRAINT pause_clocks_pkey PRIMARY KEY (pause_id);

--
-- Name: pause_presence_snapshots pause_presence_snapshots_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pause_presence_snapshots
    ADD CONSTRAINT pause_presence_snapshots_identity_key UNIQUE (pause_id, participant_id, presence_epoch);

--
-- Name: pause_presence_snapshots pause_presence_snapshots_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pause_presence_snapshots
    ADD CONSTRAINT pause_presence_snapshots_pkey PRIMARY KEY (pause_id, participant_id);

--
-- Name: pause_revisions pause_revisions_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pause_revisions
    ADD CONSTRAINT pause_revisions_identity_key UNIQUE (id, pause_id);

--
-- Name: pause_revisions pause_revisions_number_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pause_revisions
    ADD CONSTRAINT pause_revisions_number_key UNIQUE (pause_id, revision_number);

--
-- Name: pause_revisions pause_revisions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pause_revisions
    ADD CONSTRAINT pause_revisions_pkey PRIMARY KEY (id);

--
-- Name: pauses pauses_current_revision_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pauses
    ADD CONSTRAINT pauses_current_revision_id_key UNIQUE (current_revision_id);

--
-- Name: pauses pauses_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pauses
    ADD CONSTRAINT pauses_identity_key UNIQUE (id, series_id, roster_id);

--
-- Name: pauses pauses_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pauses
    ADD CONSTRAINT pauses_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.tournament_lifecycle_commands
    ADD CONSTRAINT tournament_lifecycle_commands_pause_fk FOREIGN KEY (pause_id) REFERENCES public.pauses(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;

--
-- Name: presence_states presence_states_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.presence_states
    ADD CONSTRAINT presence_states_identity_key UNIQUE (id, series_id, roster_id, participant_id);

--
-- Name: presence_states presence_states_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.presence_states
    ADD CONSTRAINT presence_states_pkey PRIMARY KEY (id);

--
-- Name: presence_states presence_states_series_participant_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.presence_states
    ADD CONSTRAINT presence_states_series_participant_key UNIQUE (series_id, participant_id);

--
-- Name: reconnect_intervals reconnect_intervals_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reconnect_intervals
    ADD CONSTRAINT reconnect_intervals_identity_key UNIQUE (id, pause_id, participant_id);

--
-- Name: reconnect_intervals reconnect_intervals_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reconnect_intervals
    ADD CONSTRAINT reconnect_intervals_pkey PRIMARY KEY (id);

--
-- Name: reconnect_intervals reconnect_intervals_segment_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reconnect_intervals
    ADD CONSTRAINT reconnect_intervals_segment_key UNIQUE (pause_id, participant_id, interval_number, continuation_number);

--
-- Name: reconnect_slot_counters reconnect_slot_counters_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reconnect_slot_counters
    ADD CONSTRAINT reconnect_slot_counters_pkey PRIMARY KEY (pause_id, participant_id);

--
-- Name: resume_decisions resume_decisions_number_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.resume_decisions
    ADD CONSTRAINT resume_decisions_number_key UNIQUE (pause_id, decision_number);

--
-- Name: resume_decisions resume_decisions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.resume_decisions
    ADD CONSTRAINT resume_decisions_pkey PRIMARY KEY (id);

--
-- Name: pauses_one_active_scope_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX pauses_one_active_scope_idx ON public.pauses USING btree (scope_kind, scope_id) WHERE ((state)::text = 'active'::text);

--
-- Name: pauses_parent_state_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX pauses_parent_state_idx ON public.pauses USING btree (parent_pause_id, state, depth, created_at);

--
-- Name: reconnect_intervals_continued_from_key; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX reconnect_intervals_continued_from_key ON public.reconnect_intervals USING btree (continued_from_id) WHERE (continued_from_id IS NOT NULL);

--
-- Name: game_attempts_recovery_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX game_attempts_recovery_idx ON public.game_attempts USING btree (started_at, id) WHERE ((state)::text = 'active'::text);

--
-- Name: pause_clocks_game_resume_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX pause_clocks_game_resume_idx ON public.pause_clocks USING btree (game_attempt_id, resumed_at DESC, pause_id DESC) WHERE (resumed_deadline IS NOT NULL);

--
-- Name: ready_windows_recovery_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ready_windows_recovery_idx ON public.ready_windows USING btree (deadline, id) WHERE ((state)::text = 'open'::text);

--
-- Name: reconnect_intervals_deadline_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX reconnect_intervals_deadline_idx ON public.reconnect_intervals USING btree (deadline_at, id) WHERE ((state)::text = 'open'::text);

--
-- Name: reconnect_intervals_one_open_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX reconnect_intervals_one_open_idx ON public.reconnect_intervals USING btree (pause_id, participant_id) WHERE ((state)::text = 'open'::text);

--
-- Name: reconnect_intervals_root_presence_epoch_key; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX reconnect_intervals_root_presence_epoch_key ON public.reconnect_intervals USING btree (pause_id, participant_id, presence_epoch) WHERE (continuation_number = 0);

--
-- Name: game_attempts game_attempt_revision_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER game_attempt_revision_guard BEFORE UPDATE ON public.game_attempts FOR EACH ROW EXECUTE FUNCTION public.game_attempt_revision_guard();

--
-- Name: game_attempts game_attempts_pause_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER game_attempts_pause_consistency AFTER INSERT OR UPDATE ON public.game_attempts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_game_pause_state();

--
-- Name: pauses normal_wave_reconnect_suspension; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER normal_wave_reconnect_suspension AFTER INSERT ON public.pauses DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_normal_wave_reconnect_suspension();

--
-- Name: pauses pause_cancel_reconnect_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER pause_cancel_reconnect_guard BEFORE UPDATE ON public.pauses FOR EACH ROW WHEN ((((old.state)::text = 'active'::text) AND ((new.state)::text = 'cancelled'::text))) EXECUTE FUNCTION public.pause_cancel_reconnect_guard();

--
-- Name: pause_clocks pause_clock_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER pause_clock_guard BEFORE DELETE OR UPDATE ON public.pause_clocks FOR EACH ROW EXECUTE FUNCTION public.pause_clock_guard();

--
-- Name: pause_clocks pause_clocks_pause_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER pause_clocks_pause_consistency AFTER INSERT OR UPDATE ON public.pause_clocks DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_game_pause_clock();

--
-- Name: pauses pause_current_resume_cas_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER pause_current_resume_cas_guard BEFORE UPDATE ON public.pauses FOR EACH ROW WHEN ((((old.state)::text = 'active'::text) AND ((new.state)::text = 'resumed'::text))) EXECUTE FUNCTION public.pause_resume_current_evidence_guard();

--
-- Name: pauses pause_graph_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER pause_graph_guard BEFORE INSERT OR DELETE OR UPDATE ON public.pauses FOR EACH ROW EXECUTE FUNCTION public.pause_graph_guard();

--
-- Name: pause_presence_snapshots pause_presence_snapshot_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER pause_presence_snapshot_guard BEFORE INSERT ON public.pause_presence_snapshots FOR EACH ROW EXECUTE FUNCTION public.pause_presence_snapshot_guard();

--
-- Name: pause_presence_snapshots pause_presence_snapshots_append_only; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER pause_presence_snapshots_append_only BEFORE DELETE OR UPDATE ON public.pause_presence_snapshots FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

--
-- Name: pause_revisions pause_revision_insert_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER pause_revision_insert_guard BEFORE INSERT ON public.pause_revisions FOR EACH ROW EXECUTE FUNCTION public.pause_revision_insert_guard();

--
-- Name: pause_revisions pause_revisions_append_only; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER pause_revisions_append_only BEFORE DELETE OR UPDATE ON public.pause_revisions FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

--
-- Name: pause_revisions pause_revisions_pause_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER pause_revisions_pause_consistency AFTER INSERT ON public.pause_revisions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_pause_revision();

--
-- Name: pause_presence_snapshots pause_snapshots_pause_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER pause_snapshots_pause_consistency AFTER INSERT ON public.pause_presence_snapshots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_pause_presence_snapshot();

--
-- Name: pauses pause_terminal_descendant_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER pause_terminal_descendant_guard BEFORE UPDATE ON public.pauses FOR EACH ROW WHEN ((((old.state)::text = 'active'::text) AND ((new.state)::text = ANY ((ARRAY['resumed'::character varying, 'cancelled'::character varying])::text[])))) EXECUTE FUNCTION public.pause_terminal_descendant_guard();

--
-- Name: pauses pause_wave_reconnect_lock; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER pause_wave_reconnect_lock BEFORE INSERT ON public.pauses FOR EACH ROW EXECUTE FUNCTION public.normal_wave_reconnect_lock();

--
-- Name: pauses pauses_clock_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER pauses_clock_consistency AFTER INSERT OR UPDATE ON public.pauses DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_game_pause_clock();

--
-- Name: pauses pauses_game_state_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER pauses_game_state_consistency AFTER INSERT OR UPDATE ON public.pauses DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_game_pause_state();

--
-- Name: pauses pauses_presence_snapshot_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER pauses_presence_snapshot_consistency AFTER INSERT OR UPDATE ON public.pauses DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_pause_presence_snapshot();

--
-- Name: pauses pauses_revision_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER pauses_revision_consistency AFTER INSERT OR UPDATE ON public.pauses DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_pause_revision();

--
-- Name: presence_states presence_state_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER presence_state_guard BEFORE INSERT OR DELETE OR UPDATE ON public.presence_states FOR EACH ROW EXECUTE FUNCTION public.presence_state_guard();

--
-- Name: presence_states presence_states_interval_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER presence_states_interval_consistency AFTER UPDATE ON public.presence_states DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_reconnect_terminal_presence();

--
-- Name: reconnect_slot_counters reconnect_counters_interval_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER reconnect_counters_interval_consistency AFTER INSERT OR UPDATE ON public.reconnect_slot_counters DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_reconnect_slot_count();

--
-- Name: reconnect_intervals reconnect_interval_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER reconnect_interval_guard BEFORE INSERT OR DELETE OR UPDATE ON public.reconnect_intervals FOR EACH ROW EXECUTE FUNCTION public.reconnect_interval_guard();

--
-- Name: reconnect_intervals reconnect_interval_normal_wave_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER reconnect_interval_normal_wave_consistency AFTER INSERT ON public.reconnect_intervals DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_normal_wave_reconnect_suspension();

--
-- Name: reconnect_intervals reconnect_intervals_counter_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER reconnect_intervals_counter_consistency AFTER INSERT OR UPDATE ON public.reconnect_intervals DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_reconnect_slot_count();

--
-- Name: reconnect_intervals reconnect_intervals_presence_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER reconnect_intervals_presence_consistency AFTER UPDATE ON public.reconnect_intervals DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_reconnect_terminal_presence();

--
-- Name: reconnect_slot_counters reconnect_slot_counter_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER reconnect_slot_counter_guard BEFORE DELETE OR UPDATE ON public.reconnect_slot_counters FOR EACH ROW EXECUTE FUNCTION public.reconnect_slot_counter_guard();

--
-- Name: resume_decisions resume_decision_insert_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER resume_decision_insert_guard BEFORE INSERT ON public.resume_decisions FOR EACH ROW EXECUTE FUNCTION public.resume_decision_insert_guard();

--
-- Name: resume_decisions resume_decisions_append_only; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER resume_decisions_append_only BEFORE DELETE OR UPDATE ON public.resume_decisions FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

--
-- Name: wave_members wave_members_normal_pause_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER wave_members_normal_pause_guard BEFORE INSERT OR DELETE OR UPDATE ON public.wave_members FOR EACH ROW EXECUTE FUNCTION public.wave_member_normal_pause_guard();

--
-- Name: pause_clocks pause_clocks_attempt_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pause_clocks
    ADD CONSTRAINT pause_clocks_attempt_fk FOREIGN KEY (game_attempt_id) REFERENCES public.game_attempts(id) ON DELETE RESTRICT;

--
-- Name: pause_clocks pause_clocks_pause_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pause_clocks
    ADD CONSTRAINT pause_clocks_pause_id_fkey FOREIGN KEY (pause_id) REFERENCES public.pauses(id) ON DELETE RESTRICT;

--
-- Name: pause_presence_snapshots pause_presence_snapshots_live_presence_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pause_presence_snapshots
    ADD CONSTRAINT pause_presence_snapshots_live_presence_fk FOREIGN KEY (series_id, participant_id) REFERENCES public.presence_states(series_id, participant_id) ON DELETE RESTRICT;

--
-- Name: pause_presence_snapshots pause_presence_snapshots_participant_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pause_presence_snapshots
    ADD CONSTRAINT pause_presence_snapshots_participant_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

--
-- Name: pause_presence_snapshots pause_presence_snapshots_pause_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pause_presence_snapshots
    ADD CONSTRAINT pause_presence_snapshots_pause_id_fkey FOREIGN KEY (pause_id) REFERENCES public.pauses(id) ON DELETE RESTRICT;

--
-- Name: pause_revisions pause_revisions_pause_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pause_revisions
    ADD CONSTRAINT pause_revisions_pause_id_fkey FOREIGN KEY (pause_id) REFERENCES public.pauses(id) ON DELETE RESTRICT;

--
-- Name: pause_revisions pause_revisions_previous_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pause_revisions
    ADD CONSTRAINT pause_revisions_previous_fk FOREIGN KEY (previous_revision_id, pause_id) REFERENCES public.pause_revisions(id, pause_id) ON DELETE RESTRICT;

--
-- Name: pauses pauses_attempt_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pauses
    ADD CONSTRAINT pauses_attempt_fk FOREIGN KEY (game_attempt_id, series_id, roster_id) REFERENCES public.game_attempts(id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: pauses pauses_current_revision_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pauses
    ADD CONSTRAINT pauses_current_revision_fk FOREIGN KEY (current_revision_id, id) REFERENCES public.pause_revisions(id, pause_id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;

--
-- Name: pauses pauses_parent_pause_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pauses
    ADD CONSTRAINT pauses_parent_pause_id_fkey FOREIGN KEY (parent_pause_id) REFERENCES public.pauses(id) ON DELETE RESTRICT;

--
-- Name: pauses pauses_roster_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pauses
    ADD CONSTRAINT pauses_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

--
-- Name: pauses pauses_series_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pauses
    ADD CONSTRAINT pauses_series_fk FOREIGN KEY (series_id, roster_id) REFERENCES public.series(id, roster_id) ON DELETE RESTRICT;

--
-- Name: pauses pauses_wave_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pauses
    ADD CONSTRAINT pauses_wave_fk FOREIGN KEY (wave_id, roster_id) REFERENCES public.waves(id, roster_id) ON DELETE RESTRICT;

--
-- Name: presence_states presence_states_participant_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.presence_states
    ADD CONSTRAINT presence_states_participant_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

--
-- Name: presence_states presence_states_roster_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.presence_states
    ADD CONSTRAINT presence_states_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

--
-- Name: presence_states presence_states_series_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.presence_states
    ADD CONSTRAINT presence_states_series_fk FOREIGN KEY (series_id, roster_id) REFERENCES public.series(id, roster_id) ON DELETE RESTRICT;

--
-- Name: reconnect_intervals reconnect_intervals_attempt_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reconnect_intervals
    ADD CONSTRAINT reconnect_intervals_attempt_fk FOREIGN KEY (game_attempt_id, series_id, roster_id) REFERENCES public.game_attempts(id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: reconnect_intervals reconnect_intervals_continued_from_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reconnect_intervals
    ADD CONSTRAINT reconnect_intervals_continued_from_fk FOREIGN KEY (continued_from_id) REFERENCES public.reconnect_intervals(id) ON DELETE RESTRICT;

--
-- Name: reconnect_intervals reconnect_intervals_counter_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reconnect_intervals
    ADD CONSTRAINT reconnect_intervals_counter_fk FOREIGN KEY (pause_id, participant_id) REFERENCES public.reconnect_slot_counters(pause_id, participant_id) ON DELETE RESTRICT;

--
-- Name: reconnect_intervals reconnect_intervals_participant_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reconnect_intervals
    ADD CONSTRAINT reconnect_intervals_participant_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

--
-- Name: reconnect_intervals reconnect_intervals_pause_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reconnect_intervals
    ADD CONSTRAINT reconnect_intervals_pause_fk FOREIGN KEY (pause_id, series_id, roster_id) REFERENCES public.pauses(id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: reconnect_intervals reconnect_intervals_suspended_by_pause_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reconnect_intervals
    ADD CONSTRAINT reconnect_intervals_suspended_by_pause_fk FOREIGN KEY (suspended_by_pause_id) REFERENCES public.pauses(id) ON DELETE RESTRICT;

--
-- Name: reconnect_slot_counters reconnect_slot_counters_participant_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reconnect_slot_counters
    ADD CONSTRAINT reconnect_slot_counters_participant_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

--
-- Name: reconnect_slot_counters reconnect_slot_counters_pause_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reconnect_slot_counters
    ADD CONSTRAINT reconnect_slot_counters_pause_id_fkey FOREIGN KEY (pause_id) REFERENCES public.pauses(id) ON DELETE RESTRICT;

--
-- Name: reconnect_slot_counters reconnect_slot_counters_snapshot_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reconnect_slot_counters
    ADD CONSTRAINT reconnect_slot_counters_snapshot_fk FOREIGN KEY (pause_id, participant_id) REFERENCES public.pause_presence_snapshots(pause_id, participant_id) ON DELETE RESTRICT;

--
-- Name: resume_decisions resume_decisions_first_interval_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.resume_decisions
    ADD CONSTRAINT resume_decisions_first_interval_fk FOREIGN KEY (first_reconnect_interval_id, pause_id, first_participant_id) REFERENCES public.reconnect_intervals(id, pause_id, participant_id) ON DELETE RESTRICT;

--
-- Name: resume_decisions resume_decisions_pause_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.resume_decisions
    ADD CONSTRAINT resume_decisions_pause_id_fkey FOREIGN KEY (pause_id) REFERENCES public.pauses(id) ON DELETE RESTRICT;

--
-- Name: resume_decisions resume_decisions_second_interval_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.resume_decisions
    ADD CONSTRAINT resume_decisions_second_interval_fk FOREIGN KEY (second_reconnect_interval_id, pause_id, second_participant_id) REFERENCES public.reconnect_intervals(id, pause_id, participant_id) ON DELETE RESTRICT;

-- Durable idempotency and topology evidence for timer-driven transitions.
CREATE TABLE public.deadline_transition_receipts (
    id uuid NOT NULL,
    command_id uuid NOT NULL,
    transition_kind character varying(32) NOT NULL,
    deadline_id uuid NOT NULL,
    expected_deadline_revision bigint NOT NULL,
    expected_authority_revision bigint NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    wave_id uuid NOT NULL,
    series_id uuid,
    game_attempt_id uuid,
    pause_id uuid,
    participant_id uuid,
    ready_window_id uuid,
    result_commit_id uuid,
    normal_no_show_commit_ids uuid[],
    route_evidence_id uuid,
    resolved_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT deadline_transition_receipts_kind_check CHECK (((transition_kind)::text = ANY ((ARRAY['game_timeout_replay'::character varying, 'ready_window_no_show'::character varying, 'reconnect_interval_expired'::character varying, 'reconnect_forfeit'::character varying, 'reconnect_replay'::character varying])::text[]))),
    CONSTRAINT deadline_transition_receipts_revision_check CHECK ((expected_deadline_revision >= 1 AND expected_authority_revision >= 1)),
    CONSTRAINT deadline_transition_receipts_shape_check CHECK (((transition_kind = 'game_timeout_replay' AND series_id IS NOT NULL AND game_attempt_id IS NOT NULL AND pause_id IS NULL AND participant_id IS NULL AND ready_window_id IS NULL AND result_commit_id IS NOT NULL AND normal_no_show_commit_ids IS NULL AND route_evidence_id IS NOT NULL) OR (transition_kind = 'ready_window_no_show' AND series_id IS NULL AND game_attempt_id IS NULL AND pause_id IS NULL AND participant_id IS NULL AND ready_window_id IS NOT NULL AND ready_window_id = deadline_id AND result_commit_id IS NULL AND normal_no_show_commit_ids IS NOT NULL AND cardinality(normal_no_show_commit_ids) > 0 AND route_evidence_id IS NULL) OR (transition_kind = 'reconnect_interval_expired' AND series_id IS NOT NULL AND game_attempt_id IS NOT NULL AND pause_id IS NOT NULL AND participant_id IS NOT NULL AND ready_window_id IS NULL AND result_commit_id IS NULL AND normal_no_show_commit_ids IS NULL AND route_evidence_id IS NULL) OR (transition_kind = 'reconnect_forfeit' AND series_id IS NOT NULL AND game_attempt_id IS NOT NULL AND pause_id IS NOT NULL AND participant_id IS NOT NULL AND ready_window_id IS NULL AND result_commit_id IS NOT NULL AND normal_no_show_commit_ids IS NULL AND route_evidence_id IS NULL) OR (transition_kind = 'reconnect_replay' AND series_id IS NOT NULL AND game_attempt_id IS NOT NULL AND pause_id IS NOT NULL AND participant_id IS NOT NULL AND ready_window_id IS NULL AND result_commit_id IS NOT NULL AND normal_no_show_commit_ids IS NULL AND route_evidence_id IS NOT NULL))),
    CONSTRAINT deadline_transition_receipts_timestamps_check CHECK ((resolved_at <= created_at))
);

ALTER TABLE ONLY public.deadline_transition_receipts
    ADD CONSTRAINT deadline_transition_receipts_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.deadline_transition_receipts
    ADD CONSTRAINT deadline_transition_receipts_command_key UNIQUE (command_id);

ALTER TABLE ONLY public.deadline_transition_receipts
    ADD CONSTRAINT deadline_transition_receipts_deadline_key UNIQUE (deadline_id, expected_deadline_revision);

ALTER TABLE ONLY public.deadline_transition_receipts
    ADD CONSTRAINT deadline_transition_receipts_wave_fk FOREIGN KEY (wave_id, tournament_id, roster_id) REFERENCES public.waves(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.deadline_transition_receipts
    ADD CONSTRAINT deadline_transition_receipts_series_fk FOREIGN KEY (series_id, tournament_id, roster_id) REFERENCES public.series(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.deadline_transition_receipts
    ADD CONSTRAINT deadline_transition_receipts_game_fk FOREIGN KEY (game_attempt_id, series_id, roster_id) REFERENCES public.game_attempts(id, series_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.deadline_transition_receipts
    ADD CONSTRAINT deadline_transition_receipts_pause_fk FOREIGN KEY (pause_id, series_id, roster_id) REFERENCES public.pauses(id, series_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.deadline_transition_receipts
    ADD CONSTRAINT deadline_transition_receipts_participant_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.deadline_transition_receipts
    ADD CONSTRAINT deadline_transition_receipts_window_fk FOREIGN KEY (ready_window_id, wave_id, roster_id) REFERENCES public.ready_windows(id, wave_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.deadline_transition_receipts
    ADD CONSTRAINT deadline_transition_receipts_result_commit_fk FOREIGN KEY (result_commit_id) REFERENCES public.result_commits(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.deadline_transition_receipts
    ADD CONSTRAINT deadline_transition_receipts_route_fk FOREIGN KEY (route_evidence_id) REFERENCES public.wave_member_routes(id) ON DELETE RESTRICT;

CREATE FUNCTION public.deadline_transition_receipt_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    matching_no_show_commits INTEGER;
    distinct_no_show_commits INTEGER;
    stored_no_show_commits INTEGER;
    result_is_consistent BOOLEAN;
    route_is_consistent BOOLEAN;
    deadline_is_consistent BOOLEAN;
    terminal_is_consistent BOOLEAN;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'deadline transition receipts are append-only evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.transition_kind = 'ready_window_no_show' THEN
        SELECT COUNT(*), COUNT(DISTINCT requested.commit_id)
        INTO matching_no_show_commits, distinct_no_show_commits
        FROM unnest(NEW.normal_no_show_commit_ids) AS requested(commit_id)
        JOIN normal_no_show_commits AS no_show_commit
            ON no_show_commit.id = requested.commit_id
            AND no_show_commit.tournament_id = NEW.tournament_id
            AND no_show_commit.roster_id = NEW.roster_id
            AND no_show_commit.wave_id = NEW.wave_id
            AND no_show_commit.ready_window_id = NEW.ready_window_id
            AND no_show_commit.expected_wave_revision = NEW.expected_deadline_revision;

        SELECT COUNT(*)
        INTO stored_no_show_commits
        FROM normal_no_show_commits
        WHERE tournament_id = NEW.tournament_id
            AND roster_id = NEW.roster_id
            AND wave_id = NEW.wave_id
            AND ready_window_id = NEW.ready_window_id;

        SELECT ready_window.state = 'expired'
            AND wave.state = 'ready_window_expired'
        INTO deadline_is_consistent
        FROM ready_windows AS ready_window
        JOIN waves AS wave
            ON wave.id = ready_window.wave_id
            AND wave.roster_id = ready_window.roster_id
        WHERE ready_window.id = NEW.ready_window_id;

        IF matching_no_show_commits <> cardinality(NEW.normal_no_show_commit_ids)
            OR distinct_no_show_commits <> matching_no_show_commits
            OR stored_no_show_commits <> matching_no_show_commits
            OR deadline_is_consistent IS DISTINCT FROM TRUE THEN
            RAISE EXCEPTION 'ready-window receipt does not exhaustively bind its no-show commits'
                USING ERRCODE = 'check_violation';
        END IF;

        RETURN NEW;
    END IF;

    IF NEW.result_commit_id IS NOT NULL THEN
        SELECT result_commit.tournament_id = NEW.tournament_id
            AND result_commit.roster_id = NEW.roster_id
            AND result_commit.series_id = NEW.series_id
            AND result_commit.attempt_id = NEW.game_attempt_id
        INTO result_is_consistent
        FROM result_commits AS result_commit
        WHERE result_commit.id = NEW.result_commit_id;
    ELSE
        result_is_consistent := TRUE;
    END IF;

    IF NEW.route_evidence_id IS NOT NULL THEN
        SELECT route.tournament_id = NEW.tournament_id
            AND route.roster_id = NEW.roster_id
            AND route.wave_id = NEW.wave_id
            AND route.series_id = NEW.series_id
            AND route.game_attempt_id = NEW.game_attempt_id
        INTO route_is_consistent
        FROM wave_member_routes AS route
        WHERE route.id = NEW.route_evidence_id;
    ELSE
        route_is_consistent := TRUE;
    END IF;

    IF NEW.transition_kind = 'game_timeout_replay' THEN
        SELECT game_attempt.state = 'void'
            AND game_attempt.result_reason = 'no_solve'
            AND game_attempt.revision = NEW.expected_deadline_revision + 1
            AND series.state = 'replay_required'
        INTO deadline_is_consistent
        FROM game_attempts AS game_attempt
        JOIN series
            ON series.id = game_attempt.series_id
            AND series.roster_id = game_attempt.roster_id
        WHERE game_attempt.id = NEW.deadline_id
            AND game_attempt.id = NEW.game_attempt_id;
    ELSE
        SELECT reconnect_interval.state = 'expired'
            AND reconnect_interval.revision = NEW.expected_deadline_revision + 1
            AND reconnect_interval.closed_at = NEW.resolved_at
            AND reconnect_interval.participant_id = NEW.participant_id
            AND reconnect_interval.pause_id = NEW.pause_id
            AND reconnect_interval.game_attempt_id = NEW.game_attempt_id
        INTO deadline_is_consistent
        FROM reconnect_intervals AS reconnect_interval
        WHERE reconnect_interval.id = NEW.deadline_id;

        IF NEW.transition_kind = 'reconnect_interval_expired' THEN
            SELECT pause.state = 'active'
                AND game_attempt.state = 'paused'
            INTO terminal_is_consistent
            FROM pauses AS pause
            JOIN game_attempts AS game_attempt
                ON game_attempt.id = pause.game_attempt_id
                AND game_attempt.series_id = pause.series_id
                AND game_attempt.roster_id = pause.roster_id
            WHERE pause.id = NEW.pause_id;
        ELSIF NEW.transition_kind = 'reconnect_forfeit' THEN
            SELECT pause.state = 'cancelled'
                AND pause.resolved_at = NEW.resolved_at
                AND game_attempt.state = 'completed'
                AND game_attempt.result_reason = 'operator_forfeit'
            INTO terminal_is_consistent
            FROM pauses AS pause
            JOIN game_attempts AS game_attempt
                ON game_attempt.id = pause.game_attempt_id
                AND game_attempt.series_id = pause.series_id
                AND game_attempt.roster_id = pause.roster_id
            WHERE pause.id = NEW.pause_id;
        ELSE
            SELECT pause.state = 'cancelled'
                AND pause.resolved_at = NEW.resolved_at
                AND game_attempt.state = 'void'
                AND game_attempt.result_reason = 'disconnect'
                AND series.state = 'replay_required'
            INTO terminal_is_consistent
            FROM pauses AS pause
            JOIN game_attempts AS game_attempt
                ON game_attempt.id = pause.game_attempt_id
                AND game_attempt.series_id = pause.series_id
                AND game_attempt.roster_id = pause.roster_id
            JOIN series
                ON series.id = game_attempt.series_id
                AND series.roster_id = game_attempt.roster_id
            WHERE pause.id = NEW.pause_id;
        END IF;
    END IF;

    IF result_is_consistent IS DISTINCT FROM TRUE
        OR route_is_consistent IS DISTINCT FROM TRUE
        OR deadline_is_consistent IS DISTINCT FROM TRUE
        OR (
            NEW.transition_kind <> 'game_timeout_replay'
            AND terminal_is_consistent IS DISTINCT FROM TRUE
        ) THEN
        RAISE EXCEPTION 'deadline receipt does not match committed terminal state'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER deadline_transition_receipts_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.deadline_transition_receipts
FOR EACH ROW EXECUTE FUNCTION public.deadline_transition_receipt_guard();

CREATE FUNCTION public.normal_no_show_receipt_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM deadline_transition_receipts AS receipt
        WHERE receipt.transition_kind = 'ready_window_no_show'
            AND receipt.ready_window_id = NEW.ready_window_id
            AND NOT NEW.id = ANY(receipt.normal_no_show_commit_ids)
    ) THEN
        RAISE EXCEPTION 'resolved ready window cannot gain another no-show commit'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER normal_no_show_commits_receipt_guard
BEFORE INSERT ON public.normal_no_show_commits
FOR EACH ROW EXECUTE FUNCTION public.normal_no_show_receipt_guard();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS public.ready_windows_recovery_idx;
DROP INDEX IF EXISTS public.game_attempts_recovery_idx;

DROP TRIGGER IF EXISTS wave_members_normal_pause_guard ON public.wave_members;
DROP TRIGGER IF EXISTS game_attempts_pause_consistency ON public.game_attempts;
DROP TRIGGER IF EXISTS game_attempt_revision_guard ON public.game_attempts;
DROP TRIGGER IF EXISTS normal_no_show_commits_receipt_guard ON public.normal_no_show_commits;

ALTER TABLE ONLY public.tournament_lifecycle_commands
    DROP CONSTRAINT IF EXISTS tournament_lifecycle_commands_pause_fk;

DROP TABLE IF EXISTS
    public.deadline_transition_receipts,
    public.resume_decisions,
    public.pause_clocks,
    public.reconnect_intervals,
    public.reconnect_slot_counters,
    public.pause_presence_snapshots,
    public.pause_revisions,
    public.pauses,
    public.presence_states;

DROP FUNCTION IF EXISTS
    public.normal_no_show_receipt_guard(),
    public.deadline_transition_receipt_guard(),
    public.tournament_execution_revision_snapshot(uuid, uuid),
    public.wave_member_normal_pause_guard(),
    public.validate_reconnect_terminal_presence(),
    public.validate_reconnect_slot_count(),
    public.validate_pause_revision(),
    public.validate_pause_presence_snapshot(),
    public.validate_normal_wave_reconnect_suspension(),
    public.validate_game_pause_state(),
    public.validate_game_pause_clock(),
    public.resume_decision_insert_guard(),
    public.reconnect_slot_counter_guard(),
    public.reconnect_interval_guard(),
    public.presence_state_guard(),
    public.pause_terminal_descendant_guard(),
    public.pause_revision_insert_guard(),
    public.pause_resume_current_evidence_guard(),
    public.pause_presence_snapshot_guard(),
    public.pause_graph_guard(),
    public.pause_clock_guard(),
    public.pause_cancel_reconnect_guard(),
    public.normal_wave_reconnect_lock(),
    public.game_attempt_revision_guard();

-- +goose StatementEnd
