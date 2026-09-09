-- +goose Up
-- +goose StatementBegin

-- Initial result domain schema.
SET LOCAL check_function_bodies = false;

--
-- Name: append_only_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.append_only_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION '% is append-only evidence', TG_TABLE_NAME
        USING ERRCODE = 'check_violation';
END;
$$;

--
-- Name: audit_payload_has_flag(jsonb); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.audit_payload_has_flag(value jsonb) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE
    AS $$
DECLARE
    item JSONB;
    item_key TEXT;
BEGIN
    IF jsonb_typeof(value) = 'object' THEN
        FOR item_key, item IN SELECT * FROM jsonb_each(value)
        LOOP
            IF LOWER(item_key) IN ('flag', 'submitted_flag', 'expected_flag')
                OR audit_payload_has_flag(item) THEN
                RETURN TRUE;
            END IF;
        END LOOP;
    ELSIF jsonb_typeof(value) = 'array' THEN
        FOR item IN SELECT * FROM jsonb_array_elements(value)
        LOOP
            IF audit_payload_has_flag(item) THEN
                RETURN TRUE;
            END IF;
        END LOOP;
    END IF;

    RETURN FALSE;
END;
$$;

--
-- Name: official_result_head_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.official_result_head_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    current_number BIGINT;
    previous_revision UUID;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'official-result heads are durable CAS state'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT revision_number, previous_revision_id
    INTO current_number, previous_revision
    FROM official_result_revisions
    WHERE id = NEW.current_revision_id;

    IF TG_OP = 'INSERT' THEN
        IF NEW.revision <> 1 OR current_number <> 1 THEN
            RAISE EXCEPTION 'official-result head must start at revision one'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSE
        IF NEW.entity_kind IS DISTINCT FROM OLD.entity_kind
            OR NEW.entity_id IS DISTINCT FROM OLD.entity_id
            OR NEW.series_id IS DISTINCT FROM OLD.series_id
            OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
            OR NEW.game_attempt_id IS DISTINCT FROM OLD.game_attempt_id
            OR NEW.revision <> OLD.revision + 1
            OR current_number <> NEW.revision
            OR previous_revision IS DISTINCT FROM OLD.current_revision_id
            OR NEW.updated_at <= OLD.updated_at THEN
            RAISE EXCEPTION 'invalid official-result head CAS transition'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: official_result_revision_insert_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.official_result_revision_insert_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    previous_number BIGINT;
    event_state VARCHAR(16);
    event_reason VARCHAR(40);
    event_winner UUID;
BEGIN
    IF NEW.entity_kind = 'game_attempt' THEN
        PERFORM 1
        FROM game_attempts
        WHERE id = NEW.game_attempt_id
        FOR UPDATE;
    ELSE
        PERFORM 1
        FROM series
        WHERE id = NEW.series_id
        FOR UPDATE;
    END IF;

    IF EXISTS (
        SELECT 1
        FROM result_commits
        WHERE result_event_id = NEW.result_event_id

        UNION ALL

        SELECT 1
        FROM normal_no_show_commits
        WHERE result_event_id = NEW.result_event_id

        UNION ALL

        SELECT 1
        FROM operator_forfeit_commits
        WHERE result_event_id = NEW.result_event_id
    ) THEN
        RAISE EXCEPTION 'committed result event cannot gain another revision'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.previous_revision_id IS NOT NULL THEN
        SELECT revision_number
        INTO previous_number
        FROM official_result_revisions
        WHERE id = NEW.previous_revision_id;

        IF previous_number IS NULL OR NEW.revision_number <> previous_number + 1 THEN
            RAISE EXCEPTION 'official-result revisions must be contiguous'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.revision_number <> 1 THEN
        RAISE EXCEPTION 'official-result lineage must start at revision one'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT result_state, result_reason, winner_id
    INTO event_state, event_reason, event_winner
    FROM result_events
    WHERE id = NEW.result_event_id;

    IF NEW.entity_kind = 'game_attempt'
        AND (
            NEW.result_state IS DISTINCT FROM event_state
            OR NEW.result_reason IS DISTINCT FROM event_reason
            OR NEW.winner_id IS DISTINCT FROM event_winner
        ) THEN
        RAISE EXCEPTION 'Game result revision must retain server event evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM projection_revisions
        WHERE id = NEW.source_projection_revision_id
            AND tournament_id = NEW.tournament_id
            AND roster_id = NEW.roster_id
            AND revision_number = NEW.source_projection_revision
    ) THEN
        RAISE EXCEPTION 'official result revision has no exact source projection'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;
-- Name: result_commit_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.result_commit_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    linkage_is_consistent BOOLEAN;
    official_revision_count INTEGER;
    score_revision_count INTEGER;
BEGIN
    SELECT (
        game_revision.entity_kind = 'game_attempt'
        AND game_revision.entity_id = NEW.attempt_id
        AND game_revision.result_event_id = NEW.result_event_id
        AND score_revision.result_event_id = NEW.result_event_id
        AND audit_event.tournament_id = NEW.tournament_id
        AND outbox_event.tournament_id = NEW.tournament_id
        AND outbox_source.tournament_id = NEW.tournament_id
        AND outbox_source.roster_id = NEW.roster_id
        AND outbox_source.series_id = NEW.series_id
        AND outbox_source.result_event_id = NEW.result_event_id
        AND outbox_source.projection_evidence_id = NEW.projection_evidence_id
        AND projection.tournament_id = NEW.tournament_id
        AND projection.result_event_id = NEW.result_event_id
        AND (
            NEW.series_result_revision_id IS NULL
            OR (
                series_revision.entity_kind = 'series'
                AND series_revision.entity_id = NEW.series_id
                AND series_revision.result_event_id = NEW.result_event_id
            )
        )
    )
    INTO linkage_is_consistent
    FROM official_result_revisions AS game_revision
    JOIN series_score_revisions AS score_revision
        ON score_revision.id = NEW.series_score_revision_id
    JOIN audit_events AS audit_event
        ON audit_event.id = NEW.audit_event_id
    JOIN outbox_events AS outbox_event
        ON outbox_event.id = NEW.outbox_event_id
    JOIN outbox_result_sources AS outbox_source
        ON outbox_source.outbox_event_id = outbox_event.id
    JOIN result_projection_evidence AS projection
        ON projection.id = NEW.projection_evidence_id
    LEFT JOIN official_result_revisions AS series_revision
        ON series_revision.id = NEW.series_result_revision_id
    WHERE game_revision.id = NEW.game_result_revision_id;

    IF linkage_is_consistent IS DISTINCT FROM TRUE THEN
        RAISE EXCEPTION 'result commit evidence is not one atomic lineage'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*)
    INTO official_revision_count
    FROM official_result_revisions
    WHERE result_event_id = NEW.result_event_id;

    SELECT COUNT(*)
    INTO score_revision_count
    FROM series_score_revisions
    WHERE result_event_id = NEW.result_event_id;

    IF official_revision_count <> 1 + (
        CASE
            WHEN NEW.series_result_revision_id IS NOT NULL THEN 1
            ELSE 0
        END
    )
        OR score_revision_count <> 1 THEN
        RAISE EXCEPTION 'result commit must exhaustively link event revisions'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: result_event_insert_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.result_event_insert_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    attempt_row game_attempts%ROWTYPE;
    submission_status VARCHAR(16);
    submission_participant UUID;
    series_first UUID;
    series_second UUID;
BEGIN
    SELECT *
    INTO attempt_row
    FROM game_attempts
    WHERE id = NEW.attempt_id
    FOR UPDATE;

    SELECT first_participant_id, second_participant_id
    INTO series_first, series_second
    FROM series
    WHERE id = NEW.series_id AND roster_id = NEW.roster_id
    FOR KEY SHARE;

    IF NEW.winner_id IS NOT NULL
        AND NEW.winner_id NOT IN (series_first, series_second) THEN
        RAISE EXCEPTION 'result winner is outside the Series'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.server_sequence <> attempt_row.result_event_sequence THEN
        RAISE EXCEPTION 'result event sequence must equal the durable attempt cursor'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.submission_event_id IS NOT NULL THEN
        SELECT status, participant_id
        INTO submission_status, submission_participant
        FROM submission_events
        WHERE id = NEW.submission_event_id;

        IF submission_status <> 'accepted' THEN
            RAISE EXCEPTION 'result requires an accepted submission event'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.result_reason = 'solved'
            AND NEW.winner_id IS DISTINCT FROM submission_participant THEN
            RAISE EXCEPTION 'solved result winner must match the submission participant'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: series_result_identity_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.series_result_identity_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.first_participant_id IS DISTINCT FROM OLD.first_participant_id
        OR NEW.second_participant_id IS DISTINCT FROM OLD.second_participant_id
        OR NEW.format IS DISTINCT FROM OLD.format
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Series result identity is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: series_score_head_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.series_score_head_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    current_number BIGINT;
    previous_revision UUID;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Series-score heads are durable CAS state'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT revision_number, previous_revision_id
    INTO current_number, previous_revision
    FROM series_score_revisions
    WHERE id = NEW.current_revision_id;

    IF TG_OP = 'INSERT' THEN
        IF NEW.revision <> 1 OR current_number <> 1 THEN
            RAISE EXCEPTION 'Series-score head must start at revision one'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSE
        IF NEW.series_id IS DISTINCT FROM OLD.series_id
            OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
            OR NEW.revision <> OLD.revision + 1
            OR current_number <> NEW.revision
            OR previous_revision IS DISTINCT FROM OLD.current_revision_id
            OR NEW.updated_at <= OLD.updated_at THEN
            RAISE EXCEPTION 'invalid Series-score head CAS transition'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: series_score_revision_insert_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.series_score_revision_insert_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    previous_number BIGINT;
    series_format VARCHAR(8);
BEGIN
    SELECT format
    INTO series_format
    FROM series
    WHERE id = NEW.series_id
    FOR UPDATE;

    IF NEW.result_event_id IS NOT NULL AND EXISTS (
        SELECT 1
        FROM result_commits
        WHERE result_event_id = NEW.result_event_id

        UNION ALL

        SELECT 1
        FROM normal_no_show_commits
        WHERE result_event_id = NEW.result_event_id

        UNION ALL

        SELECT 1
        FROM operator_forfeit_commits
        WHERE result_event_id = NEW.result_event_id
    ) THEN
        RAISE EXCEPTION 'committed result event cannot gain another score revision'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.previous_revision_id IS NOT NULL THEN
        SELECT revision_number
        INTO previous_number
        FROM series_score_revisions
        WHERE id = NEW.previous_revision_id;

        IF previous_number IS NULL OR NEW.revision_number <> previous_number + 1 THEN
            RAISE EXCEPTION 'Series-score revisions must be contiguous'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.revision_number <> 1 THEN
        RAISE EXCEPTION 'Series-score lineage must start at revision one'
            USING ERRCODE = 'check_violation';
    END IF;

    IF (series_format = 'bo1' AND (
        NEW.first_participant_wins > 1
        OR NEW.second_participant_wins > 1
    )) THEN
        RAISE EXCEPTION 'BO1 score revision exceeds one win'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

-- Every score head is backed by immutable terminal evidence. The score row
-- is inserted before its evidence children, so this runs as a deferred
-- constraint trigger on the row and on each child rather than relying on a
-- client-supplied aggregate.
CREATE FUNCTION public.validate_series_score_revision_evidence() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    target_score_revision_id UUID;
    score_row series_score_revisions%ROWTYPE;
    series_first_participant_id UUID;
    series_second_participant_id UUID;
    source_exists BOOLEAN;
    attempt_count INTEGER;
    valid_attempt_count INTEGER;
    first_wins INTEGER;
    second_wins INTEGER;
    contiguous_positions BOOLEAN;
    adjudication_count INTEGER;
    normal_no_show_matches BOOLEAN;
    result_commit_matches BOOLEAN;
    pre_start_forfeit_matches BOOLEAN;
BEGIN
    IF TG_TABLE_NAME = 'series_score_revisions' THEN
        target_score_revision_id := NEW.id;
    ELSE
        target_score_revision_id := NEW.score_revision_id;
    END IF;

    SELECT *
    INTO score_row
    FROM series_score_revisions
    WHERE id = target_score_revision_id
    FOR KEY SHARE;

    IF score_row.id IS NULL THEN
        RAISE EXCEPTION 'score evidence has no score revision'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT series.first_participant_id, series.second_participant_id
    INTO series_first_participant_id, series_second_participant_id
    FROM series AS series
    WHERE series.id = score_row.series_id
        AND series.tournament_id = score_row.tournament_id
        AND series.roster_id = score_row.roster_id
    FOR KEY SHARE;

    SELECT EXISTS (
        SELECT 1
        FROM projection_revisions
        WHERE id = score_row.source_projection_revision_id
            AND tournament_id = score_row.tournament_id
            AND roster_id = score_row.roster_id
            AND revision_number = score_row.source_projection_revision
    )
    INTO source_exists;

    SELECT COUNT(*),
        COUNT(*) FILTER (
            WHERE attempt.tournament_id = score_row.tournament_id
                AND attempt.roster_id = score_row.roster_id
                AND attempt.series_id = score_row.series_id
                AND slot.series_id = score_row.series_id
                AND slot.roster_id = score_row.roster_id
                AND slot.slot_number = attempt.slot_position
                AND game.series_id = score_row.series_id
                AND game.roster_id = score_row.roster_id
                AND game.slot_id = attempt.slot_id
                AND game.attempt_number = attempt.attempt_number
                AND result_revision.entity_kind = 'game_attempt'
                AND result_revision.entity_id = attempt.game_attempt_id
                AND result_revision.game_attempt_id = attempt.game_attempt_id
                AND result_revision.series_id = score_row.series_id
                AND result_revision.roster_id = score_row.roster_id
                AND result_revision.result_event_id = attempt.result_event_id
                AND result_revision.result_state = attempt.result_state
                AND result_revision.result_reason = attempt.result_reason
                AND result_revision.winner_id IS NOT DISTINCT FROM attempt.winner_id
                AND result_event.id = attempt.result_event_id
                AND result_event.attempt_id = attempt.game_attempt_id
                AND result_event.series_id = score_row.series_id
                AND result_event.roster_id = score_row.roster_id
                AND result_event.occurred_at = attempt.occurred_at
        ),
        COALESCE(SUM(CASE
            WHEN attempt.result_state = 'completed'
                AND attempt.winner_id = series_first_participant_id THEN 1
            ELSE 0
        END), 0),
        COALESCE(SUM(CASE
            WHEN attempt.result_state = 'completed'
                AND attempt.winner_id = series_second_participant_id THEN 1
            ELSE 0
        END), 0),
        CASE
            WHEN COUNT(*) = 0 THEN TRUE
            ELSE MIN(attempt.position) = 1
                AND MAX(attempt.position) = COUNT(*)
        END
    INTO attempt_count,
        valid_attempt_count,
        first_wins,
        second_wins,
        contiguous_positions
    FROM series_score_revision_attempts AS attempt
    JOIN game_slots AS slot
        ON slot.id = attempt.slot_id
    JOIN game_attempts AS game
        ON game.id = attempt.game_attempt_id
    JOIN official_result_revisions AS result_revision
        ON result_revision.id = attempt.game_result_revision_id
    JOIN result_events AS result_event
        ON result_event.id = attempt.result_event_id
    WHERE attempt.score_revision_id = target_score_revision_id;

    SELECT COUNT(*)
    INTO adjudication_count
    FROM series_score_revision_adjudications
    WHERE score_revision_id = target_score_revision_id;

    SELECT EXISTS (
        SELECT 1
        FROM normal_no_show_commits AS commit_row
        WHERE commit_row.series_score_revision_id = target_score_revision_id
            AND commit_row.tournament_id = score_row.tournament_id
            AND commit_row.roster_id = score_row.roster_id
            AND commit_row.series_id = score_row.series_id
            AND commit_row.result_event_id = score_row.result_event_id
            AND NOT EXISTS (
                SELECT 1
                FROM series_score_revision_attempts AS attempt
                WHERE attempt.score_revision_id = target_score_revision_id
                    AND NOT EXISTS (
                        SELECT 1
                        FROM normal_no_show_commit_games AS commit_game
                        WHERE commit_game.commit_id = commit_row.id
                            AND commit_game.game_attempt_id = attempt.game_attempt_id
                            AND commit_game.game_result_revision_id = attempt.game_result_revision_id
                    )
            )
            AND NOT EXISTS (
                SELECT 1
                FROM normal_no_show_commit_games AS commit_game
                WHERE commit_game.commit_id = commit_row.id
                    AND NOT EXISTS (
                        SELECT 1
                        FROM series_score_revision_attempts AS attempt
                        WHERE attempt.score_revision_id = target_score_revision_id
                            AND attempt.game_attempt_id = commit_game.game_attempt_id
                            AND attempt.game_result_revision_id = commit_game.game_result_revision_id
                    )
            )
    )
    INTO normal_no_show_matches;

    SELECT EXISTS (
        SELECT 1
        FROM result_commits AS commit_row
        WHERE commit_row.series_score_revision_id = target_score_revision_id
            AND commit_row.tournament_id = score_row.tournament_id
            AND commit_row.roster_id = score_row.roster_id
            AND commit_row.series_id = score_row.series_id
            AND commit_row.result_event_id = score_row.result_event_id
    )
    INTO result_commit_matches;

    SELECT EXISTS (
        SELECT 1
        FROM series_score_revision_adjudications AS adjudication
        JOIN operator_forfeit_commits AS commit_row
            ON commit_row.id = adjudication.operator_forfeit_commit_id
        WHERE adjudication.score_revision_id = target_score_revision_id
            AND adjudication.tournament_id = score_row.tournament_id
            AND adjudication.roster_id = score_row.roster_id
            AND adjudication.series_id = score_row.series_id
            AND adjudication.command_id = score_row.command_id
            AND adjudication.actor_id = score_row.actor_id
            AND adjudication.source_projection_revision_id = score_row.source_projection_revision_id
            AND adjudication.source_projection_revision = score_row.source_projection_revision
            AND commit_row.series_score_revision_id = target_score_revision_id
            AND commit_row.command_id = score_row.command_id
            AND commit_row.actor_id = score_row.actor_id
            AND commit_row.result_event_id = score_row.result_event_id
            AND commit_row.anchor_attempt_id = adjudication.anchor_attempt_id
            AND commit_row.forfeiting_participant_id = adjudication.forfeiting_participant_id
            AND commit_row.source_projection_revision_id = score_row.source_projection_revision_id
            AND commit_row.source_projection_revision = score_row.source_projection_revision
    )
    INTO pre_start_forfeit_matches;

    IF source_exists IS DISTINCT FROM TRUE
        OR attempt_count <> valid_attempt_count
        OR contiguous_positions IS DISTINCT FROM TRUE THEN
        RAISE EXCEPTION 'score revision evidence is not exact normalized authority'
            USING ERRCODE = 'check_violation';
    END IF;

    CASE score_row.operation
        WHEN 'initialize' THEN
            IF attempt_count <> 0 OR adjudication_count <> 0
                OR first_wins <> score_row.first_participant_wins
                OR second_wins <> score_row.second_participant_wins THEN
                RAISE EXCEPTION 'initial score revision cannot contain terminal evidence'
                    USING ERRCODE = 'check_violation';
            END IF;
        WHEN 'append_attempt', 'replace_result' THEN
            IF attempt_count = 0
                OR adjudication_count <> 0
                OR first_wins <> score_row.first_participant_wins
                OR second_wins <> score_row.second_participant_wins
                OR score_row.command_attempt_id IS NULL
                OR NOT result_commit_matches
                OR NOT EXISTS (
                    SELECT 1
                    FROM series_score_revision_attempts AS attempt
                    WHERE attempt.score_revision_id = target_score_revision_id
                        AND attempt.game_attempt_id = score_row.command_attempt_id
                        AND attempt.result_event_id = score_row.result_event_id
                ) THEN
                RAISE EXCEPTION 'result score revision lacks its command attempt evidence'
                    USING ERRCODE = 'check_violation';
            END IF;
        WHEN 'no_show' THEN
            IF attempt_count = 0 OR adjudication_count <> 0
                OR NOT normal_no_show_matches THEN
                RAISE EXCEPTION 'no-show score revision lacks exact commit evidence'
                    USING ERRCODE = 'check_violation';
            END IF;
        WHEN 'pre_start_forfeit' THEN
            IF attempt_count <> 0 OR adjudication_count <> 1
                OR score_row.command_attempt_id IS NOT NULL
                OR score_row.actor_kind <> 'operator'
                OR NOT pre_start_forfeit_matches THEN
                RAISE EXCEPTION 'pre-start forfeit score revision lacks exact adjudication'
                    USING ERRCODE = 'check_violation';
            END IF;
        ELSE
            RAISE EXCEPTION 'unknown score revision operation'
                USING ERRCODE = 'check_violation';
    END CASE;

    RETURN NULL;
END;
$$;

--
-- Name: submission_event_insert_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.submission_event_insert_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    attempt_row game_attempts%ROWTYPE;
    series_first UUID;
    series_second UUID;
BEGIN
    SELECT *
    INTO attempt_row
    FROM game_attempts
    WHERE id = NEW.attempt_id
    FOR UPDATE;

    SELECT first_participant_id, second_participant_id
    INTO series_first, series_second
    FROM series
    WHERE id = NEW.series_id AND roster_id = NEW.roster_id
    FOR KEY SHARE;

    IF NEW.participant_id NOT IN (series_first, series_second) THEN
        RAISE EXCEPTION 'submission participant is outside the Series'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.server_sequence <> attempt_row.submission_event_sequence THEN
        RAISE EXCEPTION 'submission sequence must equal the durable attempt cursor'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: validate_game_result_head(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.validate_game_result_head() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    target_attempt_id UUID;
    attempt_row game_attempts%ROWTYPE;
    head_revision_id UUID;
    revision_row official_result_revisions%ROWTYPE;
BEGIN
    IF TG_TABLE_NAME = 'game_attempts' THEN
        target_attempt_id := NEW.id;
    ELSIF NEW.entity_kind = 'game_attempt' THEN
        target_attempt_id := NEW.entity_id;
    ELSE
        RETURN NULL;
    END IF;

    SELECT *
    INTO attempt_row
    FROM game_attempts
    WHERE id = target_attempt_id;

    SELECT current_revision_id
    INTO head_revision_id
    FROM official_result_heads
    WHERE entity_kind = 'game_attempt' AND entity_id = target_attempt_id;

    IF attempt_row.state IN ('completed', 'void', 'cancelled', 'superseded') THEN
        IF head_revision_id IS NULL
            OR attempt_row.result_revision_id IS DISTINCT FROM head_revision_id THEN
            RAISE EXCEPTION 'terminal Game requires exactly one current result head'
                USING ERRCODE = 'check_violation';
        END IF;

        SELECT *
        INTO revision_row
        FROM official_result_revisions
        WHERE id = head_revision_id;

        IF revision_row.series_id IS DISTINCT FROM attempt_row.series_id
            OR revision_row.roster_id IS DISTINCT FROM attempt_row.roster_id
            OR revision_row.result_state IS DISTINCT FROM attempt_row.state
            OR revision_row.result_reason IS DISTINCT FROM attempt_row.result_reason
            OR revision_row.winner_id IS DISTINCT FROM attempt_row.winner_id THEN
            RAISE EXCEPTION 'Game terminal state differs from current result revision'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF head_revision_id IS NOT NULL OR attempt_row.result_revision_id IS NOT NULL THEN
        RAISE EXCEPTION 'non-terminal Game cannot have a current result head'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

--
-- Name: validate_result_commit_current_heads(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.validate_result_commit_current_heads() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    attempt_revision_id UUID;
    game_head_revision_id UUID;
    series_score_revision_id UUID;
    score_head_revision_id UUID;
    series_result_revision_id UUID;
    result_head_revision_id UUID;
BEGIN
    SELECT result_revision_id
    INTO attempt_revision_id
    FROM game_attempts
    WHERE id = NEW.attempt_id;

    SELECT current_revision_id
    INTO game_head_revision_id
    FROM official_result_heads
    WHERE entity_kind = 'game_attempt'
        AND entity_id = NEW.attempt_id;

    SELECT current_score_revision_id, current_result_revision_id
    INTO series_score_revision_id, series_result_revision_id
    FROM series
    WHERE id = NEW.series_id;

    SELECT current_revision_id
    INTO score_head_revision_id
    FROM series_score_heads
    WHERE series_id = NEW.series_id;

    SELECT current_revision_id
    INTO result_head_revision_id
    FROM official_result_heads
    WHERE entity_kind = 'series'
        AND entity_id = NEW.series_id;

    IF attempt_revision_id IS DISTINCT FROM NEW.game_result_revision_id
        OR game_head_revision_id IS DISTINCT FROM NEW.game_result_revision_id
        OR series_score_revision_id IS DISTINCT FROM NEW.series_score_revision_id
        OR score_head_revision_id IS DISTINCT FROM NEW.series_score_revision_id
        OR series_result_revision_id
            IS DISTINCT FROM NEW.series_result_revision_id
        OR result_head_revision_id
            IS DISTINCT FROM NEW.series_result_revision_id THEN
        RAISE EXCEPTION 'result commit revisions must be current heads'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

--
-- Name: validate_result_event_commit(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.validate_result_event_commit() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    target_result_event_id UUID;
    commit_count INTEGER;
BEGIN
    IF TG_TABLE_NAME = 'result_events' THEN
        target_result_event_id := NEW.id;
    ELSE
        target_result_event_id := NEW.result_event_id;
    END IF;

    SELECT (
        SELECT COUNT(*)
        FROM result_commits
        WHERE result_event_id = target_result_event_id
    ) + (
        SELECT COUNT(*)
        FROM normal_no_show_commits
        WHERE result_event_id = target_result_event_id
    ) + (
        SELECT COUNT(*)
        FROM operator_forfeit_commits
        WHERE result_event_id = target_result_event_id
    )
    INTO commit_count;

    IF commit_count <> 1 THEN
        RAISE EXCEPTION 'result event requires exactly one atomic evidence commit'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

--
-- Name: validate_series_revision_heads(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.validate_series_revision_heads() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    target_series_id UUID;
    series_row series%ROWTYPE;
    current_score_revision_id UUID;
    result_revision_id UUID;
    score_row series_score_revisions%ROWTYPE;
    result_row official_result_revisions%ROWTYPE;
BEGIN
    IF TG_TABLE_NAME = 'series' THEN
        target_series_id := NEW.id;
    ELSIF TG_TABLE_NAME = 'series_score_heads' THEN
        target_series_id := NEW.series_id;
    ELSIF NEW.entity_kind = 'series' THEN
        target_series_id := NEW.entity_id;
    ELSE
        RETURN NULL;
    END IF;

    SELECT *
    INTO series_row
    FROM series
    WHERE id = target_series_id;

    SELECT current_revision_id
    INTO current_score_revision_id
    FROM series_score_heads
    WHERE series_id = target_series_id;

    SELECT current_revision_id
    INTO result_revision_id
    FROM official_result_heads
    WHERE entity_kind = 'series' AND entity_id = target_series_id;

    IF series_row.state = 'planned' THEN
        IF current_score_revision_id IS NULL
            OR series_row.current_score_revision_id IS DISTINCT FROM current_score_revision_id
            OR result_revision_id IS NOT NULL
            OR series_row.current_result_revision_id IS NOT NULL THEN
            RAISE EXCEPTION 'planned Series requires exactly one genesis score head and no result head'
                USING ERRCODE = 'check_violation';
        END IF;

        SELECT *
        INTO score_row
        FROM series_score_revisions
        WHERE id = current_score_revision_id;

        IF score_row.id IS NULL
            OR score_row.series_id IS DISTINCT FROM series_row.id
            OR score_row.roster_id IS DISTINCT FROM series_row.roster_id
            OR score_row.revision_number <> 1
            OR score_row.operation <> 'initialize'
            OR score_row.result_event_id IS NOT NULL
            OR score_row.previous_revision_id IS NOT NULL
            OR score_row.command_attempt_id IS NOT NULL
            OR score_row.first_participant_wins <> 0
            OR score_row.second_participant_wins <> 0
            OR EXISTS (
                SELECT 1
                FROM series_score_revision_attempts AS attempt
                WHERE attempt.score_revision_id = current_score_revision_id
            )
            OR EXISTS (
                SELECT 1
                FROM series_score_revision_adjudications AS adjudication
                WHERE adjudication.score_revision_id = current_score_revision_id
            ) THEN
            RAISE EXCEPTION 'planned Series score head must be an immutable zero-score genesis revision'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NULL;
    END IF;

    IF current_score_revision_id IS NULL
        OR series_row.current_score_revision_id IS DISTINCT FROM current_score_revision_id THEN
        RAISE EXCEPTION 'non-planned Series requires exactly one current score head'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT *
    INTO score_row
    FROM series_score_revisions
    WHERE id = current_score_revision_id;

    IF score_row.roster_id IS DISTINCT FROM series_row.roster_id
        OR score_row.first_participant_wins IS DISTINCT FROM series_row.first_participant_wins
        OR score_row.second_participant_wins IS DISTINCT FROM series_row.second_participant_wins THEN
        RAISE EXCEPTION 'Series score differs from current score revision'
            USING ERRCODE = 'check_violation';
    END IF;

    IF series_row.state IN ('completed', 'cancelled') THEN
        IF result_revision_id IS NULL
            OR series_row.current_result_revision_id IS DISTINCT FROM result_revision_id THEN
            RAISE EXCEPTION 'terminal Series requires exactly one current result head'
                USING ERRCODE = 'check_violation';
        END IF;

        SELECT *
        INTO result_row
        FROM official_result_revisions
        WHERE id = result_revision_id;

        IF result_row.roster_id IS DISTINCT FROM series_row.roster_id
            OR result_row.result_state IS DISTINCT FROM series_row.state
            OR result_row.winner_id IS DISTINCT FROM series_row.winner_id THEN
            RAISE EXCEPTION 'Series terminal state differs from current result revision'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF result_revision_id IS NOT NULL OR series_row.current_result_revision_id IS NOT NULL THEN
        RAISE EXCEPTION 'non-terminal Series cannot have a current result head'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

--
-- Name: validate_tournament_cancellation_evidence(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.validate_tournament_cancellation_evidence() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    evidence_is_consistent BOOLEAN;
BEGIN
    SELECT (
        tournament.state = 'cancelled'
        AND tournament.revision = NEW.resulting_revision
        AND tournament.paused_from_state IS NULL
        AND tournament.finished_at IS NOT DISTINCT FROM NEW.cancelled_at
        AND tournament.updated_at IS NOT DISTINCT FROM NEW.cancelled_at
        AND audit_event.actor_kind = 'operator'
        AND audit_event.actor_id IS NOT DISTINCT FROM NEW.actor_id
        AND audit_event.action = 'tournament.cancelled'
        AND audit_event.occurred_at IS NOT DISTINCT FROM NEW.cancelled_at
        AND outbox_event.terminal
        AND outbox_event.topic = 'tournament.cancelled'
        AND outbox_event.payload ->> 'state' = 'cancelled'
        AND outbox_event.created_at IS NOT DISTINCT FROM NEW.cancelled_at
        AND outbox_source.cancellation_command_id = NEW.command_id
    )
    INTO evidence_is_consistent
    FROM tournaments AS tournament
    JOIN audit_events AS audit_event
        ON audit_event.id = NEW.audit_event_id
        AND audit_event.tournament_id = NEW.tournament_id
    JOIN outbox_events AS outbox_event
        ON outbox_event.id = NEW.outbox_event_id
        AND outbox_event.tournament_id = NEW.tournament_id
    JOIN outbox_tournament_cancellation_sources AS outbox_source
        ON outbox_source.outbox_event_id = outbox_event.id
    WHERE tournament.id = NEW.tournament_id;

    IF evidence_is_consistent IS DISTINCT FROM TRUE THEN
        RAISE EXCEPTION 'Tournament cancellation evidence is inconsistent'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

--
-- Name: audit_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.audit_events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    series_id uuid,
    result_event_id uuid,
    actor_kind character varying(16) NOT NULL,
    actor_id uuid,
    action character varying(64) NOT NULL,
    payload jsonb NOT NULL,
    occurred_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT audit_events_action_check CHECK ((((action)::text = btrim((action)::text)) AND ((action)::text <> ''::text))),
    CONSTRAINT audit_events_actor_check CHECK (((((actor_kind)::text = 'server'::text) AND (actor_id IS NULL)) OR (((actor_kind)::text = 'operator'::text) AND (actor_id IS NOT NULL)))),
    CONSTRAINT audit_events_payload_check CHECK (((jsonb_typeof(payload) = 'object'::text) AND (payload <> '{}'::jsonb) AND (NOT public.audit_payload_has_flag(payload)))),
    CONSTRAINT audit_events_source_check CHECK (((((action)::text = 'tournament.cancelled'::text) AND (series_id IS NULL) AND (result_event_id IS NULL)) OR (((action)::text <> 'tournament.cancelled'::text) AND (series_id IS NOT NULL) AND (result_event_id IS NOT NULL)))),
    CONSTRAINT audit_events_timestamps_check CHECK ((occurred_at <= created_at))
);

--
-- Name: official_result_heads; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.official_result_heads (
    entity_kind character varying(16) NOT NULL,
    entity_id uuid NOT NULL,
    series_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    game_attempt_id uuid,
    current_revision_id uuid NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT official_result_heads_entity_check CHECK (((((entity_kind)::text = 'game_attempt'::text) AND (game_attempt_id IS NOT NULL) AND (entity_id = game_attempt_id)) OR (((entity_kind)::text = 'series'::text) AND (game_attempt_id IS NULL) AND (entity_id = series_id)))),
    CONSTRAINT official_result_heads_revision_check CHECK ((revision >= 1))
);

--
-- Name: official_result_revisions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.official_result_revisions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    entity_kind character varying(16) NOT NULL,
    entity_id uuid NOT NULL,
    series_id uuid NOT NULL,
    game_attempt_id uuid,
    result_event_id uuid NOT NULL,
    previous_revision_id uuid,
    revision_number bigint NOT NULL,
    command_id uuid NOT NULL,
    actor_kind character varying(16) NOT NULL,
    actor_id uuid,
    source_projection_revision_id uuid NOT NULL,
    source_projection_revision bigint NOT NULL,
    result_state character varying(16) NOT NULL,
    result_reason character varying(40) NOT NULL,
    winner_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT official_result_revisions_actor_check CHECK (
        (actor_kind = 'server' AND actor_id IS NULL)
        OR (actor_kind = 'operator' AND actor_id IS NOT NULL)
    ),
    CONSTRAINT official_result_revisions_entity_check CHECK (((((entity_kind)::text = 'game_attempt'::text) AND (game_attempt_id IS NOT NULL) AND (entity_id = game_attempt_id)) OR (((entity_kind)::text = 'series'::text) AND (game_attempt_id IS NULL) AND (entity_id = series_id)))),
    CONSTRAINT official_result_revisions_number_check CHECK (((revision_number >= 1) AND (source_projection_revision >= 1) AND (((revision_number = 1) AND (previous_revision_id IS NULL)) OR ((revision_number > 1) AND (previous_revision_id IS NOT NULL))))),
    CONSTRAINT official_result_revisions_result_check CHECK (((((entity_kind)::text = 'game_attempt'::text) AND ((result_state)::text = ANY ((ARRAY['completed'::character varying, 'void'::character varying, 'cancelled'::character varying, 'superseded'::character varying])::text[]))) OR (((entity_kind)::text = 'series'::text) AND ((result_state)::text = ANY ((ARRAY['completed'::character varying, 'cancelled'::character varying])::text[])) AND ((result_reason)::text = ANY ((ARRAY['score_complete'::character varying, 'operator_correction'::character varying, 'series_cancelled'::character varying, 'tournament_cancelled'::character varying])::text[])))))
);

--
-- Name: result_commits; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.result_commits (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    series_id uuid NOT NULL,
    attempt_id uuid NOT NULL,
    result_event_id uuid NOT NULL,
    game_result_revision_id uuid NOT NULL,
    series_score_revision_id uuid NOT NULL,
    series_result_revision_id uuid,
    audit_event_id uuid NOT NULL,
    outbox_event_id uuid NOT NULL,
    projection_evidence_id uuid NOT NULL,
    idempotency_key uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

--
-- Name: result_events; Type: TABLE; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_attempts
    ADD COLUMN result_event_sequence bigint DEFAULT 0 NOT NULL,
    ADD COLUMN submission_event_sequence bigint DEFAULT 0 NOT NULL,
    ADD CONSTRAINT game_attempts_result_event_sequence_check CHECK (result_event_sequence >= 0),
    ADD CONSTRAINT game_attempts_submission_event_sequence_check CHECK (submission_event_sequence >= 0);

CREATE TABLE public.result_events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    series_id uuid NOT NULL,
    attempt_id uuid NOT NULL,
    submission_event_id uuid,
    server_sequence bigint NOT NULL,
    idempotency_key uuid NOT NULL,
    result_state character varying(16) NOT NULL,
    result_reason character varying(40) NOT NULL,
    winner_id uuid,
    occurred_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT result_events_result_check CHECK (((((result_state)::text = 'completed'::text) AND ((result_reason)::text = ANY ((ARRAY['solved'::character varying, 'surrender'::character varying, 'operator_forfeit'::character varying])::text[])) AND (winner_id IS NOT NULL) AND (((result_reason)::text <> 'solved'::text) OR (submission_event_id IS NOT NULL))) OR (((result_state)::text = 'void'::text) AND ((result_reason)::text = ANY ((ARRAY['no_solve'::character varying, 'task_failure'::character varying, 'common_platform_failure'::character varying, 'disconnect'::character varying, 'execution_epoch_break'::character varying])::text[])) AND (winner_id IS NULL)) OR (((result_state)::text = 'cancelled'::text) AND ((result_reason)::text = ANY ((ARRAY['no_show'::character varying, 'series_cancelled'::character varying, 'tournament_cancelled'::character varying])::text[])) AND (winner_id IS NULL)) OR (((result_state)::text = 'superseded'::text) AND ((result_reason)::text = 'derived_revision_superseded'::text) AND (winner_id IS NULL)))),
    CONSTRAINT result_events_sequence_check CHECK ((server_sequence >= 1)),
    CONSTRAINT result_events_timestamps_check CHECK ((occurred_at <= created_at))
);

--
-- Name: result_projection_evidence; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.result_projection_evidence (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    series_id uuid NOT NULL,
    result_event_id uuid NOT NULL,
    artifact_kinds jsonb NOT NULL,
    payload_digest bytea NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT result_projection_evidence_artifacts_check CHECK (((jsonb_typeof(artifact_kinds) = 'array'::text) AND (jsonb_array_length(artifact_kinds) > 0))),
    CONSTRAINT result_projection_evidence_digest_check CHECK (((octet_length(payload_digest) = 32) AND (payload_digest <> decode(repeat('00'::text, 32), 'hex'::text))))
);

--
-- Name: series_score_heads; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.series_score_heads (
    series_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    current_revision_id uuid NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT series_score_heads_revision_check CHECK ((revision >= 1))
);

--
-- Name: series_score_revisions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.series_score_revisions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    series_id uuid NOT NULL,
    result_event_id uuid,
    previous_revision_id uuid,
    revision_number bigint NOT NULL,
    operation character varying(32) NOT NULL,
    command_id uuid NOT NULL,
    actor_kind character varying(16) NOT NULL,
    actor_id uuid,
    command_attempt_id uuid,
    source_projection_revision_id uuid NOT NULL,
    source_projection_revision bigint NOT NULL,
    first_participant_wins smallint NOT NULL,
    second_participant_wins smallint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT series_score_revisions_actor_check CHECK (
        (actor_kind = 'server' AND actor_id IS NULL)
        OR (actor_kind = 'operator' AND actor_id IS NOT NULL)
    ),
    CONSTRAINT series_score_revisions_number_check CHECK (
        revision_number >= 1
        AND source_projection_revision >= 1
        AND (
            revision_number = 1
            AND previous_revision_id IS NULL
            AND result_event_id IS NULL
            AND operation = 'initialize'
            AND command_attempt_id IS NULL
            AND first_participant_wins = 0
            AND second_participant_wins = 0
            OR revision_number > 1
            AND previous_revision_id IS NOT NULL
            AND result_event_id IS NOT NULL
            AND operation IN ('append_attempt', 'replace_result', 'no_show', 'pre_start_forfeit')
        )
    ),
    CONSTRAINT series_score_revisions_operation_check CHECK (
        operation IN ('initialize', 'append_attempt', 'replace_result', 'no_show', 'pre_start_forfeit')
    ),
    CONSTRAINT series_score_revisions_score_check CHECK ((((first_participant_wins >= 0) AND (first_participant_wins <= 2)) AND ((second_participant_wins >= 0) AND (second_participant_wins <= 2)) AND (NOT ((first_participant_wins = 2) AND (second_participant_wins = 2)))))
);

CREATE TABLE public.series_score_revision_attempts (
    score_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    series_id uuid NOT NULL,
    position smallint NOT NULL,
    slot_id uuid NOT NULL,
    slot_position smallint NOT NULL,
    game_attempt_id uuid NOT NULL,
    attempt_number integer NOT NULL,
    game_result_revision_id uuid NOT NULL,
    result_event_id uuid NOT NULL,
    result_state character varying(16) NOT NULL,
    result_reason character varying(40) NOT NULL,
    winner_id uuid,
    occurred_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT series_score_revision_attempts_position_check CHECK (
        position >= 1 AND slot_position >= 1 AND attempt_number >= 1
    )
);

--
-- Name: submission_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.submission_events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    series_id uuid NOT NULL,
    attempt_id uuid NOT NULL,
    assignment_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    server_sequence bigint NOT NULL,
    idempotency_key uuid NOT NULL,
    status character varying(16) NOT NULL,
    decision_reason text,
    payload_digest bytea NOT NULL,
    intent_digest bytea NOT NULL,
    submitted_at timestamp with time zone NOT NULL,
    received_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT submission_events_decision_check CHECK (((((status)::text = ANY ((ARRAY['received'::character varying, 'accepted'::character varying])::text[])) AND (decision_reason IS NULL)) OR (((status)::text = 'rejected'::text) AND (decision_reason = btrim(decision_reason)) AND (decision_reason <> ''::text)))),
    CONSTRAINT submission_events_digest_check CHECK (((octet_length(payload_digest) = 32) AND (payload_digest <> decode(repeat('00'::text, 32), 'hex'::text)))),
    CONSTRAINT submission_events_intent_digest_check CHECK (((octet_length(intent_digest) = 32) AND (intent_digest <> decode(repeat('00'::text, 32), 'hex'::text)))),
    CONSTRAINT submission_events_sequence_check CHECK ((server_sequence >= 1)),
    CONSTRAINT submission_events_status_check CHECK (((status)::text = ANY ((ARRAY['received'::character varying, 'accepted'::character varying, 'rejected'::character varying])::text[]))),
    CONSTRAINT submission_events_timestamps_check CHECK (((submitted_at <= received_at) AND (received_at <= created_at)))
);

--
-- Name: audit_events audit_events_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_events
    ADD CONSTRAINT audit_events_identity_key UNIQUE (id, result_event_id);

--
-- Name: audit_events audit_events_tournament_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_events
    ADD CONSTRAINT audit_events_tournament_identity_key UNIQUE (id, tournament_id);

--
-- Name: audit_events audit_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_events
    ADD CONSTRAINT audit_events_pkey PRIMARY KEY (id);

--
-- Name: audit_events audit_events_result_event_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_events
    ADD CONSTRAINT audit_events_result_event_id_key UNIQUE (result_event_id);

--
-- Name: official_result_heads official_result_heads_current_revision_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.official_result_heads
    ADD CONSTRAINT official_result_heads_current_revision_id_key UNIQUE (current_revision_id);

--
-- Name: official_result_heads official_result_heads_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.official_result_heads
    ADD CONSTRAINT official_result_heads_pkey PRIMARY KEY (entity_kind, entity_id);

--
-- Name: official_result_revisions official_result_revisions_entity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.official_result_revisions
    ADD CONSTRAINT official_result_revisions_entity_key UNIQUE (id, entity_kind, entity_id);

--
-- Name: official_result_revisions official_result_revisions_head_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.official_result_revisions
    ADD CONSTRAINT official_result_revisions_head_identity_key UNIQUE (id, entity_kind, entity_id, series_id, roster_id);

--
-- Name: official_result_revisions official_result_revisions_number_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.official_result_revisions
    ADD CONSTRAINT official_result_revisions_number_key UNIQUE (entity_kind, entity_id, revision_number);

--
-- Name: official_result_revisions official_result_revisions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.official_result_revisions
    ADD CONSTRAINT official_result_revisions_pkey PRIMARY KEY (id);

--
-- Name: official_result_revisions official_result_revisions_series_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.official_result_revisions
    ADD CONSTRAINT official_result_revisions_series_key UNIQUE (id, series_id, roster_id);

--
-- Name: result_commits result_commits_audit_event_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_commits
    ADD CONSTRAINT result_commits_audit_event_id_key UNIQUE (audit_event_id);

--
-- Name: result_commits result_commits_game_result_revision_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_commits
    ADD CONSTRAINT result_commits_game_result_revision_id_key UNIQUE (game_result_revision_id);

--
-- Name: result_commits result_commits_idempotency_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_commits
    ADD CONSTRAINT result_commits_idempotency_key_key UNIQUE (idempotency_key);

--
-- Name: result_commits result_commits_outbox_event_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_commits
    ADD CONSTRAINT result_commits_outbox_event_id_key UNIQUE (outbox_event_id);

--
-- Name: result_commits result_commits_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_commits
    ADD CONSTRAINT result_commits_pkey PRIMARY KEY (id);

--
-- Name: result_commits result_commits_projection_evidence_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_commits
    ADD CONSTRAINT result_commits_projection_evidence_id_key UNIQUE (projection_evidence_id);

--
-- Name: result_commits result_commits_result_event_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_commits
    ADD CONSTRAINT result_commits_result_event_id_key UNIQUE (result_event_id);

--
-- Name: result_commits result_commits_series_result_revision_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_commits
    ADD CONSTRAINT result_commits_series_result_revision_id_key UNIQUE (series_result_revision_id);

--
-- Name: result_commits result_commits_series_score_revision_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_commits
    ADD CONSTRAINT result_commits_series_score_revision_id_key UNIQUE (series_score_revision_id);

--
-- Name: result_events result_events_idempotency_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_events
    ADD CONSTRAINT result_events_idempotency_key_key UNIQUE (idempotency_key);

--
-- Name: result_events result_events_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_events
    ADD CONSTRAINT result_events_identity_key UNIQUE (id, attempt_id, series_id, roster_id);

--
-- Name: result_events result_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_events
    ADD CONSTRAINT result_events_pkey PRIMARY KEY (id);

--
-- Name: result_events result_events_sequence_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_events
    ADD CONSTRAINT result_events_sequence_key UNIQUE (attempt_id, server_sequence);

--
-- Name: result_events result_events_series_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_events
    ADD CONSTRAINT result_events_series_identity_key UNIQUE (id, series_id, roster_id);

--
-- Name: result_events result_events_submission_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_events
    ADD CONSTRAINT result_events_submission_key UNIQUE (submission_event_id);

--
-- Name: result_projection_evidence result_projection_evidence_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_projection_evidence
    ADD CONSTRAINT result_projection_evidence_identity_key UNIQUE (id, result_event_id);

--
-- Name: result_projection_evidence result_projection_evidence_scope_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_projection_evidence
    ADD CONSTRAINT result_projection_evidence_scope_key UNIQUE (id, tournament_id, roster_id);

--
-- Name: result_projection_evidence result_projection_evidence_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_projection_evidence
    ADD CONSTRAINT result_projection_evidence_pkey PRIMARY KEY (id);

--
-- Name: result_projection_evidence result_projection_evidence_result_event_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_projection_evidence
    ADD CONSTRAINT result_projection_evidence_result_event_id_key UNIQUE (result_event_id);

--
-- Name: series_score_heads series_score_heads_current_revision_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.series_score_heads
    ADD CONSTRAINT series_score_heads_current_revision_id_key UNIQUE (current_revision_id);

--
-- Name: series_score_heads series_score_heads_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.series_score_heads
    ADD CONSTRAINT series_score_heads_pkey PRIMARY KEY (series_id);

--
-- Name: series_score_revisions series_score_revisions_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.series_score_revisions
    ADD CONSTRAINT series_score_revisions_identity_key UNIQUE (id, series_id, roster_id);

--
-- Name: series_score_revisions series_score_revisions_number_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.series_score_revisions
    ADD CONSTRAINT series_score_revisions_number_key UNIQUE (series_id, revision_number);

--
-- Name: series_score_revisions series_score_revisions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.series_score_revisions
    ADD CONSTRAINT series_score_revisions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.series_score_revision_attempts
    ADD CONSTRAINT series_score_revision_attempts_pkey PRIMARY KEY (score_revision_id, position);

ALTER TABLE ONLY public.series_score_revision_attempts
    ADD CONSTRAINT series_score_revision_attempts_attempt_key
    UNIQUE (score_revision_id, game_attempt_id);

ALTER TABLE ONLY public.series_score_revision_attempts
    ADD CONSTRAINT series_score_revision_attempts_slot_key
    UNIQUE (score_revision_id, slot_id, attempt_number);

--
-- Name: submission_events submission_events_idempotency_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.submission_events
    ADD CONSTRAINT submission_events_idempotency_key_key UNIQUE (idempotency_key);

--
-- Name: submission_events submission_events_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.submission_events
    ADD CONSTRAINT submission_events_identity_key UNIQUE (id, attempt_id, series_id, roster_id);

--
-- Name: submission_events submission_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.submission_events
    ADD CONSTRAINT submission_events_pkey PRIMARY KEY (id);

--
-- Name: submission_events submission_events_sequence_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.submission_events
    ADD CONSTRAINT submission_events_sequence_key UNIQUE (attempt_id, server_sequence);

--
-- Name: audit_events audit_events_append_only; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER audit_events_append_only BEFORE DELETE OR UPDATE ON public.audit_events FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

--
-- Name: game_attempts game_attempts_result_head_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER game_attempts_result_head_consistency AFTER INSERT OR UPDATE ON public.game_attempts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_game_result_head();

--
-- Name: official_result_heads game_result_heads_attempt_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER game_result_heads_attempt_consistency AFTER INSERT OR UPDATE ON public.official_result_heads DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_game_result_head();

--
-- Name: official_result_heads official_result_head_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER official_result_head_guard BEFORE INSERT OR DELETE OR UPDATE ON public.official_result_heads FOR EACH ROW EXECUTE FUNCTION public.official_result_head_guard();

--
-- Name: official_result_revisions official_result_revision_insert_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER official_result_revision_insert_guard BEFORE INSERT ON public.official_result_revisions FOR EACH ROW EXECUTE FUNCTION public.official_result_revision_insert_guard();

--
-- Name: official_result_revisions official_result_revisions_append_only; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER official_result_revisions_append_only BEFORE DELETE OR UPDATE ON public.official_result_revisions FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

--
-- Name: tournament_cancellations tournament_cancellation_evidence_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER tournament_cancellation_evidence_consistency
AFTER INSERT ON public.tournament_cancellations
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_tournament_cancellation_evidence();

--
-- Name: result_commits result_commit_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER result_commit_guard BEFORE INSERT ON public.result_commits FOR EACH ROW EXECUTE FUNCTION public.result_commit_guard();

--
-- Name: result_commits result_commits_append_only; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER result_commits_append_only BEFORE DELETE OR UPDATE ON public.result_commits FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

--
-- Name: result_commits result_commits_current_heads_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER result_commits_current_heads_consistency AFTER INSERT ON public.result_commits DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_result_commit_current_heads();

--
-- Name: result_commits result_commits_event_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER result_commits_event_consistency AFTER INSERT ON public.result_commits DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_result_event_commit();

--
-- Name: result_events result_event_insert_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER result_event_insert_guard BEFORE INSERT ON public.result_events FOR EACH ROW EXECUTE FUNCTION public.result_event_insert_guard();

--
-- Name: result_events result_events_append_only; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER result_events_append_only BEFORE DELETE OR UPDATE ON public.result_events FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

--
-- Name: result_events result_events_commit_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER result_events_commit_consistency AFTER INSERT ON public.result_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_result_event_commit();

--
-- Name: result_projection_evidence result_projection_evidence_append_only; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER result_projection_evidence_append_only BEFORE DELETE OR UPDATE ON public.result_projection_evidence FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

--
-- Name: official_result_heads series_result_heads_series_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER series_result_heads_series_consistency AFTER INSERT OR UPDATE ON public.official_result_heads DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_series_revision_heads();

--
-- Name: series series_result_identity_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER series_result_identity_guard BEFORE UPDATE ON public.series FOR EACH ROW EXECUTE FUNCTION public.series_result_identity_guard();

--
-- Name: series series_revision_head_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER series_revision_head_consistency AFTER INSERT OR UPDATE ON public.series DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_series_revision_heads();

--
-- Name: series_score_heads series_score_head_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER series_score_head_guard BEFORE INSERT OR DELETE OR UPDATE ON public.series_score_heads FOR EACH ROW EXECUTE FUNCTION public.series_score_head_guard();

--
-- Name: series_score_heads series_score_heads_series_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER series_score_heads_series_consistency AFTER INSERT OR UPDATE ON public.series_score_heads DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.validate_series_revision_heads();

--
-- Name: series_score_revisions series_score_revision_insert_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER series_score_revision_insert_guard BEFORE INSERT ON public.series_score_revisions FOR EACH ROW EXECUTE FUNCTION public.series_score_revision_insert_guard();

--
-- Name: series_score_revisions series_score_revisions_append_only; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER series_score_revisions_append_only BEFORE DELETE OR UPDATE ON public.series_score_revisions FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE TRIGGER series_score_revision_attempts_append_only
BEFORE DELETE OR UPDATE ON public.series_score_revision_attempts
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

--
-- Name: submission_events submission_event_insert_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER submission_event_insert_guard BEFORE INSERT ON public.submission_events FOR EACH ROW EXECUTE FUNCTION public.submission_event_insert_guard();

--
-- Name: submission_events submission_events_append_only; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER submission_events_append_only BEFORE DELETE OR UPDATE ON public.submission_events FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

--
-- Name: audit_events audit_events_event_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_events
    ADD CONSTRAINT audit_events_event_fk FOREIGN KEY (result_event_id, series_id, roster_id) REFERENCES public.result_events(id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: audit_events audit_events_roster_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_events
    ADD CONSTRAINT audit_events_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

--
-- Name: tournament_cancellations tournament_cancellations_audit_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_cancellations
    ADD CONSTRAINT tournament_cancellations_audit_fk FOREIGN KEY (audit_event_id, tournament_id) REFERENCES public.audit_events(id, tournament_id) ON DELETE RESTRICT;

--
-- Name: game_attempts game_attempts_result_revision_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_attempts
    ADD CONSTRAINT game_attempts_result_revision_fk FOREIGN KEY (result_revision_id) REFERENCES public.official_result_revisions(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;

--
-- Name: official_result_heads official_result_heads_revision_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.official_result_heads
    ADD CONSTRAINT official_result_heads_revision_fk FOREIGN KEY (current_revision_id, entity_kind, entity_id, series_id, roster_id) REFERENCES public.official_result_revisions(id, entity_kind, entity_id, series_id, roster_id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;

--
-- Name: official_result_revisions official_result_revisions_attempt_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.official_result_revisions
    ADD CONSTRAINT official_result_revisions_attempt_fk FOREIGN KEY (game_attempt_id, series_id, roster_id) REFERENCES public.game_attempts(id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: official_result_revisions official_result_revisions_event_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.official_result_revisions
    ADD CONSTRAINT official_result_revisions_event_fk FOREIGN KEY (result_event_id, series_id, roster_id) REFERENCES public.result_events(id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: official_result_revisions official_result_revisions_previous_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.official_result_revisions
    ADD CONSTRAINT official_result_revisions_previous_fk FOREIGN KEY (previous_revision_id, entity_kind, entity_id) REFERENCES public.official_result_revisions(id, entity_kind, entity_id) ON DELETE RESTRICT;

--
-- Name: official_result_revisions official_result_revisions_roster_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.official_result_revisions
    ADD CONSTRAINT official_result_revisions_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

--
-- Name: official_result_revisions official_result_revisions_series_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.official_result_revisions
    ADD CONSTRAINT official_result_revisions_series_fk FOREIGN KEY (series_id, roster_id) REFERENCES public.series(id, roster_id) ON DELETE RESTRICT;

--
-- Name: result_commits result_commits_audit_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_commits
    ADD CONSTRAINT result_commits_audit_fk FOREIGN KEY (audit_event_id, result_event_id) REFERENCES public.audit_events(id, result_event_id) ON DELETE RESTRICT;

--
-- Name: result_commits result_commits_event_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_commits
    ADD CONSTRAINT result_commits_event_fk FOREIGN KEY (result_event_id, attempt_id, series_id, roster_id) REFERENCES public.result_events(id, attempt_id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: result_commits result_commits_game_revision_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_commits
    ADD CONSTRAINT result_commits_game_revision_fk FOREIGN KEY (game_result_revision_id, series_id, roster_id) REFERENCES public.official_result_revisions(id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: result_commits result_commits_projection_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_commits
    ADD CONSTRAINT result_commits_projection_fk FOREIGN KEY (projection_evidence_id, result_event_id) REFERENCES public.result_projection_evidence(id, result_event_id) ON DELETE RESTRICT;

--
-- Name: result_commits result_commits_score_revision_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_commits
    ADD CONSTRAINT result_commits_score_revision_fk FOREIGN KEY (series_score_revision_id, series_id, roster_id) REFERENCES public.series_score_revisions(id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: result_commits result_commits_series_revision_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_commits
    ADD CONSTRAINT result_commits_series_revision_fk FOREIGN KEY (series_result_revision_id, series_id, roster_id) REFERENCES public.official_result_revisions(id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: result_events result_events_attempt_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_events
    ADD CONSTRAINT result_events_attempt_fk FOREIGN KEY (attempt_id, series_id, roster_id) REFERENCES public.game_attempts(id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: result_events result_events_roster_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_events
    ADD CONSTRAINT result_events_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

--
-- Name: result_events result_events_submission_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_events
    ADD CONSTRAINT result_events_submission_fk FOREIGN KEY (submission_event_id, attempt_id, series_id, roster_id) REFERENCES public.submission_events(id, attempt_id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: result_events result_events_winner_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_events
    ADD CONSTRAINT result_events_winner_fk FOREIGN KEY (roster_id, winner_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

--
-- Name: result_projection_evidence result_projection_evidence_event_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_projection_evidence
    ADD CONSTRAINT result_projection_evidence_event_fk FOREIGN KEY (result_event_id, series_id, roster_id) REFERENCES public.result_events(id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: result_projection_evidence result_projection_evidence_roster_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_projection_evidence
    ADD CONSTRAINT result_projection_evidence_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

--
-- Name: series series_result_revision_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.series
    ADD CONSTRAINT series_result_revision_fk FOREIGN KEY (current_result_revision_id) REFERENCES public.official_result_revisions(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;

--
-- Name: series_score_heads series_score_heads_revision_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.series_score_heads
    ADD CONSTRAINT series_score_heads_revision_fk FOREIGN KEY (current_revision_id, series_id, roster_id) REFERENCES public.series_score_revisions(id, series_id, roster_id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;

--
-- Name: series series_score_revision_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.series
    ADD CONSTRAINT series_score_revision_fk FOREIGN KEY (current_score_revision_id) REFERENCES public.series_score_revisions(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;

--
-- Name: series_score_revisions series_score_revisions_event_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.series_score_revisions
    ADD CONSTRAINT series_score_revisions_event_fk FOREIGN KEY (result_event_id, series_id, roster_id) REFERENCES public.result_events(id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: series_score_revisions series_score_revisions_previous_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.series_score_revisions
    ADD CONSTRAINT series_score_revisions_previous_fk FOREIGN KEY (previous_revision_id, series_id, roster_id) REFERENCES public.series_score_revisions(id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: series_score_revisions series_score_revisions_roster_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.series_score_revisions
    ADD CONSTRAINT series_score_revisions_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

--
-- Name: series_score_revisions series_score_revisions_series_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.series_score_revisions
    ADD CONSTRAINT series_score_revisions_series_fk FOREIGN KEY (series_id, roster_id) REFERENCES public.series(id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.series_score_revision_attempts
    ADD CONSTRAINT series_score_revision_attempts_score_fk
    FOREIGN KEY (score_revision_id, series_id, roster_id)
    REFERENCES public.series_score_revisions(id, series_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.series_score_revision_attempts
    ADD CONSTRAINT series_score_revision_attempts_attempt_fk
    FOREIGN KEY (game_attempt_id, series_id, roster_id)
    REFERENCES public.game_attempts(id, series_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.series_score_revision_attempts
    ADD CONSTRAINT series_score_revision_attempts_slot_fk
    FOREIGN KEY (slot_id, series_id, roster_id)
    REFERENCES public.game_slots(id, series_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.series_score_revision_attempts
    ADD CONSTRAINT series_score_revision_attempts_result_revision_fk
    FOREIGN KEY (game_result_revision_id, series_id, roster_id)
    REFERENCES public.official_result_revisions(id, series_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.series_score_revision_attempts
    ADD CONSTRAINT series_score_revision_attempts_result_event_fk
    FOREIGN KEY (result_event_id, game_attempt_id, series_id, roster_id)
    REFERENCES public.result_events(id, attempt_id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: submission_events submission_events_assignment_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.submission_events
    ADD CONSTRAINT submission_events_assignment_fk FOREIGN KEY (assignment_id, attempt_id, roster_id) REFERENCES public.assignments(id, attempt_id, roster_id) ON DELETE RESTRICT;

--
-- Name: submission_events submission_events_attempt_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.submission_events
    ADD CONSTRAINT submission_events_attempt_fk FOREIGN KEY (attempt_id, series_id, roster_id) REFERENCES public.game_attempts(id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: submission_events submission_events_participant_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.submission_events
    ADD CONSTRAINT submission_events_participant_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

--
-- Name: submission_events submission_events_roster_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.submission_events
    ADD CONSTRAINT submission_events_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

-- A normal no-show can cancel more than one unstarted Game while producing a
-- single Series score and result revision. It therefore has its own exhaustive
-- commit instead of weakening the one-Game invariant of result_commits.
CREATE TABLE public.normal_no_show_commits (
    id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    wave_id uuid NOT NULL,
    ready_window_id uuid NOT NULL,
    ready_window_revision_id uuid NOT NULL,
    series_id uuid NOT NULL,
    result_event_id uuid NOT NULL,
    series_score_revision_id uuid NOT NULL,
    series_result_revision_id uuid NOT NULL,
    audit_event_id uuid NOT NULL,
    outbox_event_id uuid NOT NULL,
    projection_evidence_id uuid NOT NULL,
    command_id uuid NOT NULL,
    expected_authority_revision bigint NOT NULL,
    expected_wave_revision bigint NOT NULL,
    action character varying(16) NOT NULL,
    resolved_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT normal_no_show_commits_action_check CHECK (((action)::text = ANY ((ARRAY['reopen_wave'::character varying, 'pause_wave'::character varying])::text[]))),
    CONSTRAINT normal_no_show_commits_revision_check CHECK ((expected_authority_revision >= 1 AND expected_wave_revision >= 1)),
    CONSTRAINT normal_no_show_commits_timestamps_check CHECK ((resolved_at <= created_at))
);

CREATE TABLE public.normal_no_show_commit_games (
    commit_id uuid NOT NULL,
    game_attempt_id uuid NOT NULL,
    game_result_revision_id uuid NOT NULL,
    position smallint NOT NULL,
    CONSTRAINT normal_no_show_commit_games_position_check CHECK ((position >= 1))
);

ALTER TABLE ONLY public.normal_no_show_commits
    ADD CONSTRAINT normal_no_show_commits_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.normal_no_show_commits
    ADD CONSTRAINT normal_no_show_commits_command_key UNIQUE (command_id);

ALTER TABLE ONLY public.normal_no_show_commits
    ADD CONSTRAINT normal_no_show_commits_window_series_key UNIQUE (ready_window_id, series_id);

ALTER TABLE ONLY public.normal_no_show_commits
    ADD CONSTRAINT normal_no_show_commits_result_event_key UNIQUE (result_event_id);

ALTER TABLE ONLY public.normal_no_show_commits
    ADD CONSTRAINT normal_no_show_commits_score_revision_key UNIQUE (series_score_revision_id);

ALTER TABLE ONLY public.normal_no_show_commits
    ADD CONSTRAINT normal_no_show_commits_series_revision_key UNIQUE (series_result_revision_id);

ALTER TABLE ONLY public.normal_no_show_commits
    ADD CONSTRAINT normal_no_show_commits_audit_key UNIQUE (audit_event_id);

ALTER TABLE ONLY public.normal_no_show_commits
    ADD CONSTRAINT normal_no_show_commits_outbox_key UNIQUE (outbox_event_id);

ALTER TABLE ONLY public.normal_no_show_commits
    ADD CONSTRAINT normal_no_show_commits_projection_key UNIQUE (projection_evidence_id);

ALTER TABLE ONLY public.normal_no_show_commit_games
    ADD CONSTRAINT normal_no_show_commit_games_pkey PRIMARY KEY (commit_id, game_attempt_id);

ALTER TABLE ONLY public.normal_no_show_commit_games
    ADD CONSTRAINT normal_no_show_commit_games_position_key UNIQUE (commit_id, position);

ALTER TABLE ONLY public.normal_no_show_commit_games
    ADD CONSTRAINT normal_no_show_commit_games_revision_key UNIQUE (game_result_revision_id);

ALTER TABLE ONLY public.normal_no_show_commits
    ADD CONSTRAINT normal_no_show_commits_wave_fk FOREIGN KEY (wave_id, tournament_id, roster_id) REFERENCES public.waves(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.normal_no_show_commits
    ADD CONSTRAINT normal_no_show_commits_window_fk FOREIGN KEY (ready_window_id, wave_id, roster_id) REFERENCES public.ready_windows(id, wave_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.normal_no_show_commits
    ADD CONSTRAINT normal_no_show_commits_window_revision_fk FOREIGN KEY (ready_window_revision_id) REFERENCES public.ready_windows(revision_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.normal_no_show_commits
    ADD CONSTRAINT normal_no_show_commits_series_fk FOREIGN KEY (series_id, tournament_id, roster_id) REFERENCES public.series(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.normal_no_show_commits
    ADD CONSTRAINT normal_no_show_commits_event_fk FOREIGN KEY (result_event_id, series_id, roster_id) REFERENCES public.result_events(id, series_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.normal_no_show_commits
    ADD CONSTRAINT normal_no_show_commits_score_fk FOREIGN KEY (series_score_revision_id, series_id, roster_id) REFERENCES public.series_score_revisions(id, series_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.normal_no_show_commits
    ADD CONSTRAINT normal_no_show_commits_series_revision_fk FOREIGN KEY (series_result_revision_id, series_id, roster_id) REFERENCES public.official_result_revisions(id, series_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.normal_no_show_commits
    ADD CONSTRAINT normal_no_show_commits_audit_fk FOREIGN KEY (audit_event_id, result_event_id) REFERENCES public.audit_events(id, result_event_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.normal_no_show_commits
    ADD CONSTRAINT normal_no_show_commits_projection_fk FOREIGN KEY (projection_evidence_id, result_event_id) REFERENCES public.result_projection_evidence(id, result_event_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.normal_no_show_commit_games
    ADD CONSTRAINT normal_no_show_commit_games_commit_fk FOREIGN KEY (commit_id) REFERENCES public.normal_no_show_commits(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.normal_no_show_commit_games
    ADD CONSTRAINT normal_no_show_commit_games_attempt_fk FOREIGN KEY (game_attempt_id) REFERENCES public.game_attempts(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.normal_no_show_commit_games
    ADD CONSTRAINT normal_no_show_commit_games_revision_fk FOREIGN KEY (game_result_revision_id) REFERENCES public.official_result_revisions(id) ON DELETE RESTRICT;

CREATE FUNCTION public.validate_normal_no_show_commit() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    target_commit_id UUID;
    commit_row normal_no_show_commits%ROWTYPE;
    game_count INTEGER;
    valid_game_count INTEGER;
    official_revision_count INTEGER;
    score_revision_count INTEGER;
    header_is_consistent BOOLEAN;
BEGIN
    IF TG_TABLE_NAME = 'normal_no_show_commit_games' THEN
        target_commit_id := NEW.commit_id;
    ELSE
        target_commit_id := NEW.id;
    END IF;

    SELECT *
    INTO commit_row
    FROM normal_no_show_commits
    WHERE id = target_commit_id;

    SELECT
        COUNT(*),
        COUNT(*) FILTER (
            WHERE game_revision.entity_kind = 'game_attempt'
                AND game_revision.entity_id = commit_game.game_attempt_id
                AND game_revision.game_attempt_id = commit_game.game_attempt_id
                AND game_revision.series_id = commit_row.series_id
                AND game_revision.roster_id = commit_row.roster_id
                AND game_revision.result_event_id = commit_row.result_event_id
                AND game_revision.result_state = 'cancelled'
                AND game_revision.result_reason = 'series_cancelled'
                AND game_revision.winner_id IS NULL
                AND game_attempt.result_revision_id = commit_game.game_result_revision_id
                AND game_attempt.state = 'cancelled'
                AND game_attempt.result_reason = 'series_cancelled'
                AND game_attempt.winner_id IS NULL
                AND game_head.current_revision_id = commit_game.game_result_revision_id
        )
    INTO game_count, valid_game_count
    FROM normal_no_show_commit_games AS commit_game
    JOIN official_result_revisions AS game_revision
        ON game_revision.id = commit_game.game_result_revision_id
    JOIN game_attempts AS game_attempt
        ON game_attempt.id = commit_game.game_attempt_id
    JOIN official_result_heads AS game_head
        ON game_head.entity_kind = 'game_attempt'
        AND game_head.entity_id = commit_game.game_attempt_id
    WHERE commit_game.commit_id = target_commit_id;

    SELECT (
        result_event.tournament_id = commit_row.tournament_id
        AND result_event.roster_id = commit_row.roster_id
        AND result_event.series_id = commit_row.series_id
        AND result_event.result_state = 'cancelled'
        AND result_event.result_reason = 'series_cancelled'
        AND result_event.winner_id IS NULL
        AND EXISTS (
            SELECT 1
            FROM normal_no_show_commit_games AS anchor_game
            WHERE anchor_game.commit_id = target_commit_id
                AND anchor_game.game_attempt_id = result_event.attempt_id
        )
        AND score_revision.result_event_id = commit_row.result_event_id
        AND series_revision.entity_kind = 'series'
        AND series_revision.entity_id = commit_row.series_id
        AND series_revision.result_event_id = commit_row.result_event_id
        AND series_revision.result_state = series.state
        AND series_revision.winner_id IS NOT DISTINCT FROM series.winner_id
        AND series.current_score_revision_id = commit_row.series_score_revision_id
        AND score_head.current_revision_id = commit_row.series_score_revision_id
        AND series.current_result_revision_id = commit_row.series_result_revision_id
        AND series_head.current_revision_id = commit_row.series_result_revision_id
        AND wave.state = 'ready_window_expired'
        AND ready_window.state = 'expired'
        AND ready_window.revision_id = commit_row.ready_window_revision_id
        AND audit_event.tournament_id = commit_row.tournament_id
        AND outbox_event.tournament_id = commit_row.tournament_id
        AND outbox_source.tournament_id = commit_row.tournament_id
        AND outbox_source.roster_id = commit_row.roster_id
        AND outbox_source.series_id = commit_row.series_id
        AND outbox_source.result_event_id = commit_row.result_event_id
        AND outbox_source.projection_evidence_id = commit_row.projection_evidence_id
        AND projection.tournament_id = commit_row.tournament_id
        AND (
            (
                commit_row.action = 'reopen_wave'
                AND series.state = 'completed'
                AND series.winner_id IS NOT NULL
                AND series_revision.result_reason = 'score_complete'
            )
            OR (
                commit_row.action = 'pause_wave'
                AND series.state = 'cancelled'
                AND series.winner_id IS NULL
                AND series_revision.result_reason = 'series_cancelled'
            )
        )
    )
    INTO header_is_consistent
    FROM result_events AS result_event
    JOIN series_score_revisions AS score_revision
        ON score_revision.id = commit_row.series_score_revision_id
    JOIN official_result_revisions AS series_revision
        ON series_revision.id = commit_row.series_result_revision_id
    JOIN series
        ON series.id = commit_row.series_id
    JOIN series_score_heads AS score_head
        ON score_head.series_id = commit_row.series_id
    JOIN official_result_heads AS series_head
        ON series_head.entity_kind = 'series'
        AND series_head.entity_id = commit_row.series_id
    JOIN waves AS wave
        ON wave.id = commit_row.wave_id
    JOIN ready_windows AS ready_window
        ON ready_window.id = commit_row.ready_window_id
    JOIN audit_events AS audit_event
        ON audit_event.id = commit_row.audit_event_id
    JOIN outbox_events AS outbox_event
        ON outbox_event.id = commit_row.outbox_event_id
    JOIN outbox_result_sources AS outbox_source
        ON outbox_source.outbox_event_id = outbox_event.id
    JOIN result_projection_evidence AS projection
        ON projection.id = commit_row.projection_evidence_id
    WHERE result_event.id = commit_row.result_event_id;

    SELECT COUNT(*)
    INTO official_revision_count
    FROM official_result_revisions
    WHERE result_event_id = commit_row.result_event_id;

    SELECT COUNT(*)
    INTO score_revision_count
    FROM series_score_revisions
    WHERE result_event_id = commit_row.result_event_id;

    IF game_count < 1
        OR valid_game_count <> game_count
        OR official_revision_count <> game_count + 1
        OR score_revision_count <> 1
        OR header_is_consistent IS DISTINCT FROM TRUE THEN
        RAISE EXCEPTION 'normal no-show commit is not one exhaustive atomic lineage'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

CREATE TRIGGER normal_no_show_commits_append_only
BEFORE DELETE OR UPDATE ON public.normal_no_show_commits
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE TRIGGER normal_no_show_commit_games_append_only
BEFORE DELETE OR UPDATE ON public.normal_no_show_commit_games
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE CONSTRAINT TRIGGER normal_no_show_commits_consistency
AFTER INSERT ON public.normal_no_show_commits
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_normal_no_show_commit();

CREATE CONSTRAINT TRIGGER normal_no_show_commit_games_consistency
AFTER INSERT ON public.normal_no_show_commit_games
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_normal_no_show_commit();

CREATE CONSTRAINT TRIGGER normal_no_show_commits_event_consistency
AFTER INSERT ON public.normal_no_show_commits
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_result_event_commit();

-- A pre-start operator forfeit completes a Series without inventing a Game
-- result. Its result event is anchored to the planned Game only for ordering;
-- the exhaustive commit proves that the Game and its result head stay intact.
CREATE TABLE public.operator_forfeit_commits (
    id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    series_id uuid NOT NULL,
    anchor_attempt_id uuid NOT NULL,
    result_event_id uuid NOT NULL,
    series_score_revision_id uuid NOT NULL,
    series_result_revision_id uuid NOT NULL,
    audit_event_id uuid NOT NULL,
    outbox_event_id uuid NOT NULL,
    projection_evidence_id uuid NOT NULL,
    command_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    forfeiting_participant_id uuid NOT NULL,
    expected_authority_revision bigint NOT NULL,
    source_projection_revision_id uuid NOT NULL,
    source_projection_revision bigint NOT NULL,
    rule_id character varying(64) NOT NULL,
    reason character varying(256) NOT NULL,
    evidence_ids uuid[] NOT NULL,
    resolved_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT operator_forfeit_commits_evidence_check CHECK (
        cardinality(evidence_ids) BETWEEN 1 AND 16
        AND array_position(evidence_ids, NULL) IS NULL
    ),
    CONSTRAINT operator_forfeit_commits_reason_check CHECK (
        reason = btrim(reason) AND reason <> ''
    ),
    CONSTRAINT operator_forfeit_commits_revision_check CHECK (
        expected_authority_revision >= 1
        AND source_projection_revision >= 1
    ),
    CONSTRAINT operator_forfeit_commits_rule_check CHECK (
        rule_id ~ '^[A-Za-z0-9._:-]{1,64}$'
    ),
    CONSTRAINT operator_forfeit_commits_timestamps_check CHECK (
        resolved_at <= created_at
    )
);

ALTER TABLE ONLY public.operator_forfeit_commits
    ADD CONSTRAINT operator_forfeit_commits_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.operator_forfeit_commits
    ADD CONSTRAINT operator_forfeit_commits_command_key UNIQUE (command_id);

ALTER TABLE ONLY public.operator_forfeit_commits
    ADD CONSTRAINT operator_forfeit_commits_event_key UNIQUE (result_event_id);

ALTER TABLE ONLY public.operator_forfeit_commits
    ADD CONSTRAINT operator_forfeit_commits_score_revision_key UNIQUE (series_score_revision_id);

ALTER TABLE ONLY public.operator_forfeit_commits
    ADD CONSTRAINT operator_forfeit_commits_series_revision_key UNIQUE (series_result_revision_id);

ALTER TABLE ONLY public.operator_forfeit_commits
    ADD CONSTRAINT operator_forfeit_commits_audit_key UNIQUE (audit_event_id);

ALTER TABLE ONLY public.operator_forfeit_commits
    ADD CONSTRAINT operator_forfeit_commits_outbox_key UNIQUE (outbox_event_id);

ALTER TABLE ONLY public.operator_forfeit_commits
    ADD CONSTRAINT operator_forfeit_commits_projection_key UNIQUE (projection_evidence_id);

ALTER TABLE ONLY public.operator_forfeit_commits
    ADD CONSTRAINT operator_forfeit_commits_series_fk
    FOREIGN KEY (series_id, tournament_id, roster_id)
    REFERENCES public.series(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operator_forfeit_commits
    ADD CONSTRAINT operator_forfeit_commits_attempt_fk
    FOREIGN KEY (anchor_attempt_id, series_id, roster_id)
    REFERENCES public.game_attempts(id, series_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operator_forfeit_commits
    ADD CONSTRAINT operator_forfeit_commits_event_fk
    FOREIGN KEY (result_event_id, anchor_attempt_id, series_id, roster_id)
    REFERENCES public.result_events(id, attempt_id, series_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operator_forfeit_commits
    ADD CONSTRAINT operator_forfeit_commits_score_fk
    FOREIGN KEY (series_score_revision_id, series_id, roster_id)
    REFERENCES public.series_score_revisions(id, series_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operator_forfeit_commits
    ADD CONSTRAINT operator_forfeit_commits_series_revision_fk
    FOREIGN KEY (series_result_revision_id, series_id, roster_id)
    REFERENCES public.official_result_revisions(id, series_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operator_forfeit_commits
    ADD CONSTRAINT operator_forfeit_commits_audit_fk
    FOREIGN KEY (audit_event_id, result_event_id)
    REFERENCES public.audit_events(id, result_event_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operator_forfeit_commits
    ADD CONSTRAINT operator_forfeit_commits_projection_fk
    FOREIGN KEY (projection_evidence_id, result_event_id)
    REFERENCES public.result_projection_evidence(id, result_event_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operator_forfeit_commits
    ADD CONSTRAINT operator_forfeit_commits_participant_fk
    FOREIGN KEY (roster_id, forfeiting_participant_id)
    REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

CREATE TABLE public.series_score_revision_adjudications (
    score_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    series_id uuid NOT NULL,
    operator_forfeit_commit_id uuid NOT NULL,
    command_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    anchor_attempt_id uuid NOT NULL,
    forfeiting_participant_id uuid NOT NULL,
    winner_id uuid NOT NULL,
    source_projection_revision_id uuid NOT NULL,
    source_projection_revision bigint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT series_score_revision_adjudications_source_check CHECK (
        source_projection_revision >= 1
    )
);

ALTER TABLE ONLY public.series_score_revision_adjudications
    ADD CONSTRAINT series_score_revision_adjudications_pkey PRIMARY KEY (score_revision_id);

ALTER TABLE ONLY public.series_score_revision_adjudications
    ADD CONSTRAINT series_score_revision_adjudications_commit_key
    UNIQUE (operator_forfeit_commit_id);

ALTER TABLE ONLY public.series_score_revision_adjudications
    ADD CONSTRAINT series_score_revision_adjudications_score_fk
    FOREIGN KEY (score_revision_id, series_id, roster_id)
    REFERENCES public.series_score_revisions(id, series_id, roster_id) ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.series_score_revision_adjudications
    ADD CONSTRAINT series_score_revision_adjudications_commit_fk
    FOREIGN KEY (operator_forfeit_commit_id)
    REFERENCES public.operator_forfeit_commits(id) ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.series_score_revision_adjudications
    ADD CONSTRAINT series_score_revision_adjudications_attempt_fk
    FOREIGN KEY (anchor_attempt_id, series_id, roster_id)
    REFERENCES public.game_attempts(id, series_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.series_score_revision_adjudications
    ADD CONSTRAINT series_score_revision_adjudications_forfeiting_participant_fk
    FOREIGN KEY (roster_id, forfeiting_participant_id)
    REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.series_score_revision_adjudications
    ADD CONSTRAINT series_score_revision_adjudications_winner_fk
    FOREIGN KEY (roster_id, winner_id)
    REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

CREATE TRIGGER series_score_revision_adjudications_append_only
BEFORE DELETE OR UPDATE ON public.series_score_revision_adjudications
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE CONSTRAINT TRIGGER series_score_revision_evidence_consistency
AFTER INSERT ON public.series_score_revisions
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_series_score_revision_evidence();

CREATE CONSTRAINT TRIGGER series_score_revision_attempt_evidence_consistency
AFTER INSERT ON public.series_score_revision_attempts
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_series_score_revision_evidence();

CREATE CONSTRAINT TRIGGER series_score_revision_adjudication_evidence_consistency
AFTER INSERT ON public.series_score_revision_adjudications
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_series_score_revision_evidence();

CREATE FUNCTION public.validate_operator_forfeit_commit() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    official_revision_count INTEGER;
    score_revision_count INTEGER;
    evidence_count INTEGER;
    linkage_is_consistent BOOLEAN;
BEGIN
    SELECT COUNT(DISTINCT evidence_id)
    INTO evidence_count
    FROM unnest(NEW.evidence_ids) AS evidence_id;

    SELECT (
        result_event.tournament_id = NEW.tournament_id
        AND result_event.roster_id = NEW.roster_id
        AND result_event.series_id = NEW.series_id
        AND result_event.attempt_id = NEW.anchor_attempt_id
        AND result_event.result_state = 'completed'
        AND result_event.result_reason = 'operator_forfeit'
        AND result_event.winner_id = series.winner_id
        AND anchor_attempt.state IN ('planned', 'ready')
        AND anchor_attempt.result_revision_id IS NULL
        AND score_revision.result_event_id = NEW.result_event_id
        AND series_revision.entity_kind = 'series'
        AND series_revision.entity_id = NEW.series_id
        AND series_revision.result_event_id = NEW.result_event_id
        AND series_revision.result_state = 'completed'
        AND series_revision.result_reason = 'score_complete'
        AND series_revision.winner_id = series.winner_id
        AND series.state = 'completed'
        AND series.winner_id IS NOT NULL
        AND series.winner_id <> NEW.forfeiting_participant_id
        AND NEW.forfeiting_participant_id IN (
            series.first_participant_id,
            series.second_participant_id
        )
        AND series.current_score_revision_id = NEW.series_score_revision_id
        AND score_head.current_revision_id = NEW.series_score_revision_id
        AND series.current_result_revision_id = NEW.series_result_revision_id
        AND series_head.current_revision_id = NEW.series_result_revision_id
        AND audit_event.tournament_id = NEW.tournament_id
        AND audit_event.roster_id = NEW.roster_id
        AND audit_event.series_id = NEW.series_id
        AND audit_event.result_event_id = NEW.result_event_id
        AND audit_event.actor_kind = 'operator'
        AND audit_event.actor_id = NEW.actor_id
        AND audit_event.action = 'tournament.result.committed'
        AND outbox_event.tournament_id = NEW.tournament_id
        AND outbox_event.roster_id = NEW.roster_id
        AND outbox_source.tournament_id = NEW.tournament_id
        AND outbox_source.roster_id = NEW.roster_id
        AND outbox_source.series_id = NEW.series_id
        AND outbox_source.result_event_id = NEW.result_event_id
        AND outbox_source.projection_evidence_id = NEW.projection_evidence_id
        AND projection.tournament_id = NEW.tournament_id
        AND projection.roster_id = NEW.roster_id
        AND projection.series_id = NEW.series_id
        AND projection.result_event_id = NEW.result_event_id
    )
    INTO linkage_is_consistent
    FROM result_events AS result_event
    JOIN game_attempts AS anchor_attempt
        ON anchor_attempt.id = NEW.anchor_attempt_id
    JOIN series
        ON series.id = NEW.series_id
    JOIN series_score_revisions AS score_revision
        ON score_revision.id = NEW.series_score_revision_id
    JOIN official_result_revisions AS series_revision
        ON series_revision.id = NEW.series_result_revision_id
    JOIN series_score_heads AS score_head
        ON score_head.series_id = NEW.series_id
    JOIN official_result_heads AS series_head
        ON series_head.entity_kind = 'series'
        AND series_head.entity_id = NEW.series_id
    JOIN audit_events AS audit_event
        ON audit_event.id = NEW.audit_event_id
    JOIN outbox_events AS outbox_event
        ON outbox_event.id = NEW.outbox_event_id
    JOIN outbox_result_sources AS outbox_source
        ON outbox_source.outbox_event_id = outbox_event.id
    JOIN result_projection_evidence AS projection
        ON projection.id = NEW.projection_evidence_id
    WHERE result_event.id = NEW.result_event_id;

    SELECT COUNT(*)
    INTO official_revision_count
    FROM official_result_revisions
    WHERE result_event_id = NEW.result_event_id;

    SELECT COUNT(*)
    INTO score_revision_count
    FROM series_score_revisions
    WHERE result_event_id = NEW.result_event_id;

    IF evidence_count <> cardinality(NEW.evidence_ids)
        OR official_revision_count <> 1
        OR score_revision_count <> 1
        OR linkage_is_consistent IS DISTINCT FROM TRUE THEN
        RAISE EXCEPTION 'operator forfeit commit is not one exhaustive atomic lineage'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

CREATE TRIGGER operator_forfeit_commits_append_only
BEFORE DELETE OR UPDATE ON public.operator_forfeit_commits
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE CONSTRAINT TRIGGER operator_forfeit_commits_consistency
AFTER INSERT ON public.operator_forfeit_commits
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_operator_forfeit_commit();

CREATE CONSTRAINT TRIGGER operator_forfeit_commits_event_consistency
AFTER INSERT ON public.operator_forfeit_commits
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_result_event_commit();

-- One command ledger spans both operator result routes. The polymorphic commit
-- reference is verified by a deferred trigger so command scope cannot be
-- replayed against another tournament, Series, action, or actor.
CREATE TABLE public.operator_result_commands (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    series_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    action character varying(16) NOT NULL,
    expected_authority_revision bigint NOT NULL,
    request_digest bytea NOT NULL,
    request_document jsonb NOT NULL,
    commit_id uuid NOT NULL,
    result_event_id uuid NOT NULL,
    executed_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT operator_result_commands_action_check CHECK (
        action IN ('no_show', 'forfeit')
    ),
    CONSTRAINT operator_result_commands_digest_check CHECK (
        octet_length(request_digest) = 32
        AND request_digest <> decode(repeat('00', 32), 'hex')
    ),
    CONSTRAINT operator_result_commands_document_check CHECK (
        jsonb_typeof(request_document) = 'object'
        AND request_document <> '{}'::jsonb
        AND NOT public.audit_payload_has_flag(request_document)
    ),
    CONSTRAINT operator_result_commands_revision_check CHECK (
        expected_authority_revision >= 1
    ),
    CONSTRAINT operator_result_commands_timestamps_check CHECK (
        executed_at <= created_at
    )
);

ALTER TABLE ONLY public.operator_result_commands
    ADD CONSTRAINT operator_result_commands_pkey PRIMARY KEY (command_id);

ALTER TABLE ONLY public.operator_result_commands
    ADD CONSTRAINT operator_result_commands_commit_key UNIQUE (commit_id);

ALTER TABLE ONLY public.operator_result_commands
    ADD CONSTRAINT operator_result_commands_event_key UNIQUE (result_event_id);

ALTER TABLE ONLY public.operator_result_commands
    ADD CONSTRAINT operator_result_commands_series_fk
    FOREIGN KEY (series_id, tournament_id, roster_id)
    REFERENCES public.series(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operator_result_commands
    ADD CONSTRAINT operator_result_commands_event_fk
    FOREIGN KEY (result_event_id, series_id, roster_id)
    REFERENCES public.result_events(id, series_id, roster_id) ON DELETE RESTRICT;

CREATE FUNCTION public.validate_operator_result_command() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    command_is_consistent BOOLEAN;
BEGIN
    IF NEW.action = 'no_show' THEN
        SELECT (
            commit_row.tournament_id = NEW.tournament_id
            AND commit_row.roster_id = NEW.roster_id
            AND commit_row.series_id = NEW.series_id
            AND commit_row.command_id = NEW.command_id
            AND commit_row.expected_authority_revision = NEW.expected_authority_revision
            AND commit_row.result_event_id = NEW.result_event_id
            AND audit_event.actor_kind = 'operator'
            AND audit_event.actor_id = NEW.actor_id
        )
        INTO command_is_consistent
        FROM normal_no_show_commits AS commit_row
        JOIN audit_events AS audit_event
            ON audit_event.id = commit_row.audit_event_id
        WHERE commit_row.id = NEW.commit_id;
    ELSE
        SELECT bool_or(is_consistent)
        INTO command_is_consistent
        FROM (
            SELECT (
                commit_row.tournament_id = NEW.tournament_id
                AND commit_row.roster_id = NEW.roster_id
                AND commit_row.series_id = NEW.series_id
                AND commit_row.idempotency_key = NEW.command_id
                AND commit_row.result_event_id = NEW.result_event_id
                AND result_event.result_reason = 'operator_forfeit'
                AND audit_event.actor_kind = 'operator'
                AND audit_event.actor_id = NEW.actor_id
            ) AS is_consistent
            FROM result_commits AS commit_row
            JOIN result_events AS result_event
                ON result_event.id = commit_row.result_event_id
            JOIN audit_events AS audit_event
                ON audit_event.id = commit_row.audit_event_id
            WHERE commit_row.id = NEW.commit_id

            UNION ALL

            SELECT (
                commit_row.tournament_id = NEW.tournament_id
                AND commit_row.roster_id = NEW.roster_id
                AND commit_row.series_id = NEW.series_id
                AND commit_row.command_id = NEW.command_id
                AND commit_row.expected_authority_revision = NEW.expected_authority_revision
                AND commit_row.result_event_id = NEW.result_event_id
                AND commit_row.actor_id = NEW.actor_id
            ) AS is_consistent
            FROM operator_forfeit_commits AS commit_row
            WHERE commit_row.id = NEW.commit_id
        ) AS candidate;
    END IF;

    IF command_is_consistent IS DISTINCT FROM TRUE THEN
        RAISE EXCEPTION 'operator result command scope does not match its commit'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

CREATE TRIGGER operator_result_commands_append_only
BEFORE DELETE OR UPDATE ON public.operator_result_commands
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE CONSTRAINT TRIGGER operator_result_commands_consistency
AFTER INSERT ON public.operator_result_commands
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_operator_result_command();

-- Immutable normalized Swiss point evidence. Public projection JSON is a
-- consumer of this ledger, never its source of truth. Each completed Series
-- contributes one row per participant; a bye contributes one row.
CREATE TABLE public.swiss_point_ledger_entries (
    id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    round_id uuid NOT NULL,
    round_number smallint NOT NULL,
    source_kind character varying(16) NOT NULL,
    source_series_id uuid,
    series_result_revision_id uuid,
    bye_revision_id uuid,
    result_label character varying(16),
    participant_id uuid NOT NULL,
    opponent_id uuid,
    points smallint NOT NULL,
    effective_time_ns bigint NOT NULL,
    accepted_solve_time_ns bigint,
    stable_seed integer NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT swiss_point_ledger_entries_pkey PRIMARY KEY (id),
    CONSTRAINT swiss_point_ledger_entries_source_check CHECK (
        (source_kind = 'series'
            AND source_series_id IS NOT NULL
            AND series_result_revision_id IS NOT NULL
            AND bye_revision_id IS NULL
            AND result_label IN ('played', 'no_show', 'void')
            AND opponent_id IS NOT NULL
            AND participant_id <> opponent_id
            AND points IN (0, 1))
        OR (source_kind = 'bye'
            AND source_series_id IS NULL
            AND series_result_revision_id IS NULL
            AND bye_revision_id IS NOT NULL
            AND result_label IS NULL
            AND opponent_id IS NULL
            AND points = 1
            AND effective_time_ns = 0
            AND accepted_solve_time_ns IS NULL)
    ),
    CONSTRAINT swiss_point_ledger_entries_time_check CHECK (
        effective_time_ns >= 0
        AND (accepted_solve_time_ns IS NULL
            OR (accepted_solve_time_ns >= 0 AND accepted_solve_time_ns <= effective_time_ns))
    ),
    CONSTRAINT swiss_point_ledger_entries_identity_check CHECK (
        round_number >= 1
        AND stable_seed >= 1
    )
);

CREATE UNIQUE INDEX swiss_point_ledger_series_participant_key
ON public.swiss_point_ledger_entries (roster_id, series_result_revision_id, participant_id)
WHERE series_result_revision_id IS NOT NULL;

CREATE UNIQUE INDEX swiss_point_ledger_bye_participant_key
ON public.swiss_point_ledger_entries (roster_id, bye_revision_id, participant_id)
WHERE bye_revision_id IS NOT NULL;

ALTER TABLE ONLY public.swiss_point_ledger_entries
    ADD CONSTRAINT swiss_point_ledger_round_fk
    FOREIGN KEY (round_id, roster_id)
    REFERENCES public.swiss_rounds(id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.swiss_point_ledger_entries
    ADD CONSTRAINT swiss_point_ledger_participant_fk
    FOREIGN KEY (roster_id, participant_id)
    REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.swiss_point_ledger_entries
    ADD CONSTRAINT swiss_point_ledger_opponent_fk
    FOREIGN KEY (roster_id, opponent_id)
    REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.swiss_point_ledger_entries
    ADD CONSTRAINT swiss_point_ledger_series_fk
    FOREIGN KEY (source_series_id, tournament_id, roster_id)
    REFERENCES public.series(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.swiss_point_ledger_entries
    ADD CONSTRAINT swiss_point_ledger_result_fk
    FOREIGN KEY (series_result_revision_id, source_series_id, roster_id)
    REFERENCES public.official_result_revisions(id, series_id, roster_id) ON DELETE RESTRICT;

CREATE FUNCTION public.validate_swiss_point_ledger_entry() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    stored_round_number SMALLINT;
    stored_seed INTEGER;
    result_is_current BOOLEAN;
    bye_is_linked BOOLEAN;
BEGIN
    SELECT round_number INTO stored_round_number
    FROM swiss_rounds
    WHERE id = NEW.round_id
        AND roster_id = NEW.roster_id;

    SELECT seed INTO stored_seed
    FROM participants
    WHERE id = NEW.participant_id
        AND roster_id = NEW.roster_id;

    IF stored_round_number IS NULL
        OR stored_round_number <> NEW.round_number
        OR stored_seed IS NULL
        OR stored_seed <> NEW.stable_seed THEN
        RAISE EXCEPTION 'Swiss point ledger identity is not current normalized authority'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.source_kind = 'series' THEN
        SELECT (
            series.current_result_revision_id = NEW.series_result_revision_id
            AND series.state IN ('completed', 'cancelled')
            AND revision.entity_kind = 'series'
            AND revision.entity_id = series.id
            AND (
                (
                    series.state = 'completed'
                    AND revision.result_state = 'completed'
                    AND NEW.result_label IN ('played', 'no_show')
                    AND (
                        (NEW.participant_id = series.first_participant_id
                            AND NEW.opponent_id = series.second_participant_id
                            AND NEW.points = CASE WHEN series.winner_id = series.first_participant_id THEN 1 ELSE 0 END)
                        OR (NEW.participant_id = series.second_participant_id
                            AND NEW.opponent_id = series.first_participant_id
                            AND NEW.points = CASE WHEN series.winner_id = series.second_participant_id THEN 1 ELSE 0 END)
                    )
                )
                OR (
                    series.state = 'cancelled'
                    AND revision.result_state = 'cancelled'
                    AND NEW.result_label = 'void'
                    AND NEW.points = 0
                    AND (
                        (NEW.participant_id = series.first_participant_id
                            AND NEW.opponent_id = series.second_participant_id)
                        OR (NEW.participant_id = series.second_participant_id
                            AND NEW.opponent_id = series.first_participant_id)
                    )
                )
            )
            AND EXISTS (
                SELECT 1
                FROM swiss_wave_links AS link
                JOIN wave_series AS membership
                    ON membership.wave_id = link.wave_id
                    AND membership.tournament_id = link.tournament_id
                    AND membership.roster_id = link.roster_id
                WHERE link.round_id = NEW.round_id
                    AND link.tournament_id = NEW.tournament_id
                    AND link.roster_id = NEW.roster_id
                    AND membership.series_id = NEW.source_series_id
            )
        ) INTO result_is_current
        FROM series
        JOIN official_result_revisions AS revision
            ON revision.id = NEW.series_result_revision_id
        WHERE series.id = NEW.source_series_id
            AND series.tournament_id = NEW.tournament_id
            AND series.roster_id = NEW.roster_id;

        IF result_is_current IS DISTINCT FROM TRUE THEN
            RAISE EXCEPTION 'Swiss point ledger Series source is not the current terminal authority'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSE
        SELECT EXISTS (
            SELECT 1
            FROM swiss_wave_links AS link
            WHERE link.round_id = NEW.round_id
                AND link.tournament_id = NEW.tournament_id
                AND link.roster_id = NEW.roster_id
                AND link.bye_participant_id = NEW.participant_id
                AND link.bye_revision_id = NEW.bye_revision_id
        ) INTO bye_is_linked;

        IF bye_is_linked IS DISTINCT FROM TRUE THEN
            RAISE EXCEPTION 'Swiss point ledger bye source is not the current normalized authority'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER swiss_point_ledger_entries_append_only
BEFORE DELETE OR UPDATE ON public.swiss_point_ledger_entries
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE TRIGGER swiss_point_ledger_entries_guard
BEFORE INSERT ON public.swiss_point_ledger_entries
FOR EACH ROW EXECUTE FUNCTION public.validate_swiss_point_ledger_entry();

-- One immutable correction receipt binds the operator command to the complete
-- application plan and to the exact result and projection commits produced by
-- that plan. Projection foreign keys are added by the projection migration.
CREATE TABLE public.result_correction_commits (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    series_id uuid NOT NULL,
    game_attempt_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    source_projection_revision_id uuid NOT NULL,
    source_projection_revision bigint NOT NULL,
    resulting_projection_revision_id uuid NOT NULL,
    resulting_projection_revision bigint NOT NULL,
    request_digest bytea NOT NULL,
    plan_digest bytea NOT NULL,
    validation_digest bytea NOT NULL,
    plan_document jsonb NOT NULL,
    evidence_document jsonb NOT NULL,
    result_commit_id uuid NOT NULL,
    executed_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT result_correction_commits_digest_check CHECK (
        octet_length(request_digest) = 32
        AND request_digest <> decode(repeat('00', 32), 'hex')
        AND octet_length(plan_digest) = 32
        AND plan_digest <> decode(repeat('00', 32), 'hex')
        AND octet_length(validation_digest) = 32
        AND validation_digest <> decode(repeat('00', 32), 'hex')
    ),
    CONSTRAINT result_correction_commits_document_check CHECK (
        jsonb_typeof(plan_document) = 'object'
        AND plan_document <> '{}'::jsonb
        AND jsonb_typeof(evidence_document) = 'object'
        AND evidence_document <> '{}'::jsonb
        AND NOT public.audit_payload_has_flag(plan_document)
        AND NOT public.audit_payload_has_flag(evidence_document)
    ),
    CONSTRAINT result_correction_commits_projection_check CHECK (
        source_projection_revision >= 1
        AND resulting_projection_revision = source_projection_revision + 1
        AND source_projection_revision_id <> resulting_projection_revision_id
    ),
    CONSTRAINT result_correction_commits_timestamps_check CHECK (
        executed_at <= created_at
    )
);

ALTER TABLE ONLY public.result_correction_commits
    ADD CONSTRAINT result_correction_commits_pkey PRIMARY KEY (command_id);

ALTER TABLE ONLY public.result_correction_commits
    ADD CONSTRAINT result_correction_commits_result_key UNIQUE (result_commit_id);

ALTER TABLE ONLY public.result_correction_commits
    ADD CONSTRAINT result_correction_commits_series_fk
    FOREIGN KEY (series_id, tournament_id, roster_id)
    REFERENCES public.series(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.result_correction_commits
    ADD CONSTRAINT result_correction_commits_game_fk
    FOREIGN KEY (game_attempt_id)
    REFERENCES public.game_attempts(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.result_correction_commits
    ADD CONSTRAINT result_correction_commits_result_fk
    FOREIGN KEY (result_commit_id)
    REFERENCES public.result_commits(id) ON DELETE RESTRICT;

CREATE FUNCTION public.validate_result_correction_commit() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    commit_is_consistent BOOLEAN;
BEGIN
    SELECT (
        result_commit.tournament_id = NEW.tournament_id
        AND result_commit.roster_id = NEW.roster_id
        AND result_commit.series_id = NEW.series_id
        AND result_commit.attempt_id = NEW.game_attempt_id
        AND result_commit.idempotency_key = NEW.command_id
        AND audit_event.actor_kind = 'operator'
        AND audit_event.actor_id = NEW.actor_id
        AND NEW.plan_document ->> 'schema' = 'result-correction-plan-v1'
        AND NEW.evidence_document ->> 'schema' = 'result-correction-evidence-v1'
        AND NEW.evidence_document ->> 'command_id' = NEW.command_id::TEXT
        AND NEW.evidence_document ->> 'tournament_id' = NEW.tournament_id::TEXT
        AND NEW.evidence_document ->> 'series_id' = NEW.series_id::TEXT
        AND NEW.evidence_document ->> 'game_id' = NEW.game_attempt_id::TEXT
        AND NEW.evidence_document ->> 'operator_id' = NEW.actor_id::TEXT
        AND NEW.evidence_document ->> 'validation_digest' = encode(NEW.validation_digest, 'hex')
    )
    INTO commit_is_consistent
    FROM result_commits AS result_commit
    JOIN audit_events AS audit_event
        ON audit_event.id = result_commit.audit_event_id
    WHERE result_commit.id = NEW.result_commit_id;

    IF commit_is_consistent IS DISTINCT FROM TRUE THEN
        RAISE EXCEPTION 'result correction command does not match its atomic commit evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

CREATE TRIGGER result_correction_commits_append_only
BEFORE DELETE OR UPDATE ON public.result_correction_commits
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE CONSTRAINT TRIGGER result_correction_commits_consistency
AFTER INSERT ON public.result_correction_commits
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_result_correction_commit();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS series_revision_head_consistency ON public.series;
DROP TRIGGER IF EXISTS series_result_identity_guard ON public.series;
DROP TRIGGER IF EXISTS game_attempts_result_head_consistency ON public.game_attempts;
DROP TRIGGER IF EXISTS tournament_cancellation_evidence_consistency ON public.tournament_cancellations;

ALTER TABLE ONLY public.tournament_cancellations
    DROP CONSTRAINT IF EXISTS tournament_cancellations_audit_fk;
ALTER TABLE ONLY public.tournament_cancellations
    DROP CONSTRAINT IF EXISTS tournament_cancellations_outbox_fk;

ALTER TABLE ONLY public.series
    DROP CONSTRAINT IF EXISTS series_score_revision_fk;
ALTER TABLE ONLY public.series
    DROP CONSTRAINT IF EXISTS series_result_revision_fk;
ALTER TABLE ONLY public.game_attempts
    DROP CONSTRAINT IF EXISTS game_attempts_result_revision_fk;

ALTER TABLE ONLY public.game_attempts
    DROP CONSTRAINT IF EXISTS game_attempts_result_event_sequence_check,
    DROP CONSTRAINT IF EXISTS game_attempts_submission_event_sequence_check,
    DROP COLUMN IF EXISTS result_event_sequence,
    DROP COLUMN IF EXISTS submission_event_sequence;

DROP TABLE IF EXISTS
    public.swiss_point_ledger_entries,
    public.result_correction_commits,
    public.operator_result_commands,
    public.series_score_revision_adjudications,
    public.operator_forfeit_commits,
    public.normal_no_show_commit_games,
    public.normal_no_show_commits,
    public.result_commits,
    public.result_projection_evidence,
    public.audit_events,
    public.series_score_heads,
    public.series_score_revision_attempts,
    public.series_score_revisions,
    public.official_result_heads,
    public.official_result_revisions,
    public.result_events,
    public.submission_events;

DROP FUNCTION IF EXISTS
    public.validate_tournament_cancellation_evidence(),
    public.validate_result_correction_commit(),
    public.validate_swiss_point_ledger_entry(),
    public.validate_operator_result_command(),
    public.validate_operator_forfeit_commit(),
    public.validate_normal_no_show_commit(),
    public.validate_series_score_revision_evidence(),
    public.validate_series_revision_heads(),
    public.validate_result_event_commit(),
    public.validate_result_commit_current_heads(),
    public.validate_game_result_head(),
    public.submission_event_insert_guard(),
    public.series_score_revision_insert_guard(),
    public.series_score_head_guard(),
    public.series_result_identity_guard(),
    public.result_event_insert_guard(),
    public.result_commit_guard(),
    public.official_result_revision_insert_guard(),
    public.official_result_head_guard(),
    public.audit_payload_has_flag(jsonb),
    public.append_only_guard();

-- +goose StatementEnd
