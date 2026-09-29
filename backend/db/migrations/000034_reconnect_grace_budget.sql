-- +goose Up
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

CREATE OR REPLACE FUNCTION public.reconnect_slot_counter_guard() RETURNS trigger
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
        OR NEW.slots_used <> OLD.slots_used + 1
        OR NEW.revision <> OLD.revision + 1
        OR NEW.updated_at <= OLD.updated_at
        OR NOT (
            (NEW.slot_limit = OLD.slot_limit
                AND OLD.slot_limit IN (2, 10)
                AND OLD.slots_used < OLD.slot_limit)
            OR (NEW.slot_limit = 10 AND OLD.slots_used = OLD.slot_limit AND OLD.slot_limit = 2)
        ) THEN
        RAISE EXCEPTION 'invalid reconnect slot CAS transition'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.reconnect_slot_counters WHERE slot_limit > 2) THEN
        RAISE EXCEPTION 'cannot roll back while reconnect counters use the expanded grace budget';
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION public.reconnect_slot_counter_guard() RETURNS trigger
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

-- +goose StatementEnd
