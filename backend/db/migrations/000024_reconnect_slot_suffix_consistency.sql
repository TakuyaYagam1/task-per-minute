-- +goose Up
-- +goose StatementBegin

-- A synthetic pause created after a completed reconnect retains the next
-- counter value but only owns the newly opened root suffix. Validate that
-- suffix instead of requiring every historical root in the prior pause to be
-- copied into the new pause.
CREATE OR REPLACE FUNCTION public.validate_reconnect_slot_count() RETURNS trigger
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

    IF used_slots IS NULL
        OR (used_slots = 0 AND root_count <> 0)
        OR (used_slots > 0 AND root_count = 0) THEN
        RAISE EXCEPTION
            'reconnect slot count differs from retained root intervals'
            USING ERRCODE = 'check_violation';
    END IF;

    IF used_slots > 0 AND (
        distinct_root_count IS DISTINCT FROM root_count
        OR first_root_number IS DISTINCT FROM used_slots - root_count + 1
        OR last_root_number IS DISTINCT FROM used_slots
    ) THEN
        RAISE EXCEPTION
            'reconnect root interval numbers must be contiguous'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE OR REPLACE FUNCTION public.validate_reconnect_slot_count() RETURNS trigger
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

-- +goose StatementEnd
