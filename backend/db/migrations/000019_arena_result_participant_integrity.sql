-- +goose Up

-- Result evidence is already durable and may have active synthetic writers.
-- Keep the current participant boundary stable while validating retained rows
-- and replacing the insert guards.
LOCK TABLE
    arena_series,
    arena_game_attempts,
    arena_submission_events,
    arena_result_events
IN SHARE ROW EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM arena_submission_events AS submission
        JOIN arena_series AS series
            ON series.id = submission.series_id
            AND series.roster_id = submission.roster_id
        WHERE submission.participant_id NOT IN (
            series.first_participant_id,
            series.second_participant_id
        )
    ) OR EXISTS (
        SELECT 1
        FROM arena_result_events AS result
        JOIN arena_series AS series
            ON series.id = result.series_id
            AND series.roster_id = result.roster_id
        WHERE result.winner_id IS NOT NULL
            AND result.winner_id NOT IN (
                series.first_participant_id,
                series.second_participant_id
            )
    ) OR EXISTS (
        SELECT 1
        FROM arena_result_events AS result
        JOIN arena_submission_events AS submission
            ON submission.id = result.submission_event_id
        WHERE result.result_reason = 'solved'
            AND result.winner_id IS DISTINCT FROM submission.participant_id
    ) THEN
        RAISE EXCEPTION 'Arena result participant evidence violates the Series boundary'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;
-- +goose StatementEnd

-- Series participants and format define every assignment, submission, score,
-- and result lineage. They remain immutable while lifecycle and score fields
-- continue to advance through their existing CAS guards.
-- +goose StatementBegin
CREATE FUNCTION arena_series_result_identity_guard() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.first_participant_id IS DISTINCT FROM OLD.first_participant_id
        OR NEW.second_participant_id IS DISTINCT FROM OLD.second_participant_id
        OR NEW.format IS DISTINCT FROM OLD.format
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Arena Series result identity is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_series_result_identity_guard
BEFORE UPDATE ON arena_series
FOR EACH ROW EXECUTE FUNCTION arena_series_result_identity_guard();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION arena_submission_event_insert_guard() RETURNS TRIGGER AS $$
DECLARE
    previous_sequence BIGINT;
    series_first UUID;
    series_second UUID;
BEGIN
    PERFORM 1
    FROM arena_game_attempts
    WHERE id = NEW.attempt_id
    FOR UPDATE;

    SELECT first_participant_id, second_participant_id
    INTO series_first, series_second
    FROM arena_series
    WHERE id = NEW.series_id AND roster_id = NEW.roster_id
    FOR KEY SHARE;

    IF NEW.participant_id NOT IN (series_first, series_second) THEN
        RAISE EXCEPTION 'Arena submission participant is outside the Series'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT MAX(server_sequence)
    INTO previous_sequence
    FROM arena_submission_events
    WHERE attempt_id = NEW.attempt_id;

    IF NEW.server_sequence <> COALESCE(previous_sequence, 0) + 1 THEN
        RAISE EXCEPTION 'Arena submission sequence must be contiguous'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION arena_result_event_insert_guard() RETURNS TRIGGER AS $$
DECLARE
    previous_sequence BIGINT;
    submission_status VARCHAR(16);
    submission_participant UUID;
    series_first UUID;
    series_second UUID;
BEGIN
    PERFORM 1
    FROM arena_game_attempts
    WHERE id = NEW.attempt_id
    FOR UPDATE;

    SELECT first_participant_id, second_participant_id
    INTO series_first, series_second
    FROM arena_series
    WHERE id = NEW.series_id AND roster_id = NEW.roster_id
    FOR KEY SHARE;

    IF NEW.winner_id IS NOT NULL
        AND NEW.winner_id NOT IN (series_first, series_second) THEN
        RAISE EXCEPTION 'Arena result winner is outside the Series'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT MAX(server_sequence)
    INTO previous_sequence
    FROM arena_result_events
    WHERE attempt_id = NEW.attempt_id;

    IF NEW.server_sequence <> COALESCE(previous_sequence, 0) + 1 THEN
        RAISE EXCEPTION 'Arena result event sequence must be contiguous'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.submission_event_id IS NOT NULL THEN
        SELECT status, participant_id
        INTO submission_status, submission_participant
        FROM arena_submission_events
        WHERE id = NEW.submission_event_id;

        IF submission_status <> 'accepted' THEN
            RAISE EXCEPTION 'Arena result requires an accepted submission event'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.result_reason = 'solved'
            AND NEW.winner_id IS DISTINCT FROM submission_participant THEN
            RAISE EXCEPTION 'Arena solved result winner must match the submission participant'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down

-- Downgrading reopens the participant-boundary gap. Production recovery must
-- preserve the corrected guards and use roll-forward or a verified restore.
LOCK TABLE
    arena_series,
    arena_game_attempts,
    arena_submission_events,
    arena_result_events
IN SHARE ROW EXCLUSIVE MODE;

DROP TRIGGER IF EXISTS arena_series_result_identity_guard ON arena_series;
DROP FUNCTION IF EXISTS arena_series_result_identity_guard();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION arena_submission_event_insert_guard() RETURNS TRIGGER AS $$
DECLARE
    previous_sequence BIGINT;
BEGIN
    PERFORM 1
    FROM arena_game_attempts
    WHERE id = NEW.attempt_id
    FOR UPDATE;

    SELECT MAX(server_sequence)
    INTO previous_sequence
    FROM arena_submission_events
    WHERE attempt_id = NEW.attempt_id;

    IF NEW.server_sequence <> COALESCE(previous_sequence, 0) + 1 THEN
        RAISE EXCEPTION 'Arena submission sequence must be contiguous'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION arena_result_event_insert_guard() RETURNS TRIGGER AS $$
DECLARE
    previous_sequence BIGINT;
    submission_status VARCHAR(16);
BEGIN
    PERFORM 1
    FROM arena_game_attempts
    WHERE id = NEW.attempt_id
    FOR UPDATE;

    SELECT MAX(server_sequence)
    INTO previous_sequence
    FROM arena_result_events
    WHERE attempt_id = NEW.attempt_id;

    IF NEW.server_sequence <> COALESCE(previous_sequence, 0) + 1 THEN
        RAISE EXCEPTION 'Arena result event sequence must be contiguous'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.submission_event_id IS NOT NULL THEN
        SELECT status
        INTO submission_status
        FROM arena_submission_events
        WHERE id = NEW.submission_event_id;

        IF submission_status <> 'accepted' THEN
            RAISE EXCEPTION 'Arena result requires an accepted submission event'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
