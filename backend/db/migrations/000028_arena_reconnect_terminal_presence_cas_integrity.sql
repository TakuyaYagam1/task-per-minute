-- +goose Up

-- Reconnect intervals and live presence are durable CAS evidence. Freeze both
-- surfaces while retained open intervals are checked and terminal guards are
-- installed atomically.
LOCK TABLE
    arena_presence_states,
    arena_reconnect_intervals
IN SHARE ROW EXCLUSIVE MODE;

-- An open interval must still describe the current disconnected presence
-- epoch. Closed historical intervals cannot be compared with current presence
-- after later reconnect cycles, so only retained live evidence is checked.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM arena_reconnect_intervals AS reconnect_interval
        LEFT JOIN arena_presence_states AS presence
            ON presence.series_id = reconnect_interval.series_id
            AND presence.participant_id = reconnect_interval.participant_id
        WHERE reconnect_interval.state = 'open'
            AND (
                presence.id IS NULL
                OR presence.state <> 'disconnected'
                OR presence.presence_epoch
                    IS DISTINCT FROM reconnect_interval.presence_epoch
            )
    ) THEN
        RAISE EXCEPTION 'Arena open reconnect interval has stale presence evidence'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;
-- +goose StatementEnd

-- Terminal reconnect transitions lock live presence after the interval row.
-- The deferred validator then observes the final state of the surrounding
-- interval -> presence transaction without reversing that lock order.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION arena_reconnect_interval_guard() RETURNS TRIGGER AS $$
DECLARE
    pause_state VARCHAR(16);
    pause_scope_kind VARCHAR(16);
    pause_game_attempt_id UUID;
    pause_started_at TIMESTAMPTZ;
    live_state VARCHAR(16);
    live_epoch BIGINT;
    counter_slots SMALLINT;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Arena reconnect intervals are durable history'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_OP = 'INSERT' THEN
        SELECT state, scope_kind, game_attempt_id, started_at
        INTO pause_state, pause_scope_kind, pause_game_attempt_id, pause_started_at
        FROM arena_pauses
        WHERE id = NEW.pause_id
            AND series_id = NEW.series_id
            AND roster_id = NEW.roster_id
        FOR UPDATE;

        IF pause_state IS DISTINCT FROM 'active'
            OR pause_scope_kind IS DISTINCT FROM 'game_attempt'
            OR pause_game_attempt_id IS DISTINCT FROM NEW.game_attempt_id
            OR NEW.opened_at < pause_started_at THEN
            RAISE EXCEPTION 'Arena reconnect interval requires its active Game pause'
                USING ERRCODE = 'check_violation';
        END IF;

        SELECT state, presence_epoch
        INTO live_state, live_epoch
        FROM arena_presence_states
        WHERE series_id = NEW.series_id AND participant_id = NEW.participant_id
        FOR UPDATE;

        SELECT slots_used
        INTO counter_slots
        FROM arena_reconnect_slot_counters
        WHERE pause_id = NEW.pause_id AND participant_id = NEW.participant_id
        FOR UPDATE;

        IF live_state <> 'disconnected'
            OR live_epoch IS DISTINCT FROM NEW.presence_epoch
            OR NEW.interval_number <> counter_slots + 1 THEN
            RAISE EXCEPTION 'Arena reconnect interval must consume the next live slot'
                USING ERRCODE = 'check_violation';
        END IF;

        RETURN NEW;
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.pause_id IS DISTINCT FROM OLD.pause_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.game_attempt_id IS DISTINCT FROM OLD.game_attempt_id
        OR NEW.participant_id IS DISTINCT FROM OLD.participant_id
        OR NEW.presence_epoch IS DISTINCT FROM OLD.presence_epoch
        OR NEW.interval_number IS DISTINCT FROM OLD.interval_number
        OR NEW.opened_at IS DISTINCT FROM OLD.opened_at
        OR NEW.deadline_at IS DISTINCT FROM OLD.deadline_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR OLD.state <> 'open'
        OR NEW.state NOT IN ('reconnected', 'expired', 'cancelled')
        OR NEW.revision <> OLD.revision + 1
        OR NEW.updated_at <= OLD.updated_at THEN
        RAISE EXCEPTION 'invalid Arena reconnect interval CAS transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state IN ('reconnected', 'expired') THEN
        SELECT state, presence_epoch
        INTO live_state, live_epoch
        FROM arena_presence_states
        WHERE series_id = NEW.series_id AND participant_id = NEW.participant_id
        FOR UPDATE;

        IF live_state IS DISTINCT FROM 'disconnected'
            OR live_epoch IS DISTINCT FROM NEW.presence_epoch THEN
            RAISE EXCEPTION 'Arena reconnect terminal transition has stale presence CAS'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION arena_validate_reconnect_terminal_presence() RETURNS TRIGGER AS $$
DECLARE
    live_state VARCHAR(16);
    live_epoch BIGINT;
BEGIN
    IF TG_TABLE_NAME = 'arena_presence_states' THEN
        IF OLD.state = 'disconnected'
            AND NEW.state = 'connected'
            AND EXISTS (
                SELECT 1
                FROM arena_reconnect_intervals AS reconnect_interval
                WHERE reconnect_interval.series_id = NEW.series_id
                    AND reconnect_interval.participant_id = NEW.participant_id
                    AND reconnect_interval.state = 'open'
            ) THEN
            RAISE EXCEPTION 'Arena reconnect presence retains an open interval'
                USING ERRCODE = 'check_violation';
        END IF;

        RETURN NULL;
    END IF;

    IF NEW.state NOT IN ('reconnected', 'expired') THEN
        RETURN NULL;
    END IF;

    SELECT state, presence_epoch
    INTO live_state, live_epoch
    FROM arena_presence_states
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
        RAISE EXCEPTION 'Arena reconnect terminal state differs from live presence CAS'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER arena_reconnect_intervals_presence_consistency
AFTER UPDATE ON arena_reconnect_intervals
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_reconnect_terminal_presence();

CREATE CONSTRAINT TRIGGER arena_presence_states_interval_consistency
AFTER UPDATE ON arena_presence_states
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_reconnect_terminal_presence();

-- +goose Down

-- Downgrading reopens contradictory terminal reconnect evidence. Production
-- recovery must preserve the corrected CAS and use roll-forward or a verified
-- restore.
LOCK TABLE
    arena_presence_states,
    arena_reconnect_intervals
IN SHARE ROW EXCLUSIVE MODE;

DROP TRIGGER IF EXISTS arena_presence_states_interval_consistency
    ON arena_presence_states;
DROP TRIGGER IF EXISTS arena_reconnect_intervals_presence_consistency
    ON arena_reconnect_intervals;
DROP FUNCTION IF EXISTS arena_validate_reconnect_terminal_presence();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION arena_reconnect_interval_guard() RETURNS TRIGGER AS $$
DECLARE
    pause_state VARCHAR(16);
    pause_scope_kind VARCHAR(16);
    pause_game_attempt_id UUID;
    pause_started_at TIMESTAMPTZ;
    live_state VARCHAR(16);
    live_epoch BIGINT;
    counter_slots SMALLINT;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Arena reconnect intervals are durable history'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_OP = 'INSERT' THEN
        SELECT state, scope_kind, game_attempt_id, started_at
        INTO pause_state, pause_scope_kind, pause_game_attempt_id, pause_started_at
        FROM arena_pauses
        WHERE id = NEW.pause_id
            AND series_id = NEW.series_id
            AND roster_id = NEW.roster_id
        FOR UPDATE;

        IF pause_state IS DISTINCT FROM 'active'
            OR pause_scope_kind IS DISTINCT FROM 'game_attempt'
            OR pause_game_attempt_id IS DISTINCT FROM NEW.game_attempt_id
            OR NEW.opened_at < pause_started_at THEN
            RAISE EXCEPTION 'Arena reconnect interval requires its active Game pause'
                USING ERRCODE = 'check_violation';
        END IF;

        SELECT state, presence_epoch
        INTO live_state, live_epoch
        FROM arena_presence_states
        WHERE series_id = NEW.series_id AND participant_id = NEW.participant_id
        FOR UPDATE;

        SELECT slots_used
        INTO counter_slots
        FROM arena_reconnect_slot_counters
        WHERE pause_id = NEW.pause_id AND participant_id = NEW.participant_id
        FOR UPDATE;

        IF live_state <> 'disconnected'
            OR live_epoch IS DISTINCT FROM NEW.presence_epoch
            OR NEW.interval_number <> counter_slots + 1 THEN
            RAISE EXCEPTION 'Arena reconnect interval must consume the next live slot'
                USING ERRCODE = 'check_violation';
        END IF;

        RETURN NEW;
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.pause_id IS DISTINCT FROM OLD.pause_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.game_attempt_id IS DISTINCT FROM OLD.game_attempt_id
        OR NEW.participant_id IS DISTINCT FROM OLD.participant_id
        OR NEW.presence_epoch IS DISTINCT FROM OLD.presence_epoch
        OR NEW.interval_number IS DISTINCT FROM OLD.interval_number
        OR NEW.opened_at IS DISTINCT FROM OLD.opened_at
        OR NEW.deadline_at IS DISTINCT FROM OLD.deadline_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR OLD.state <> 'open'
        OR NEW.state NOT IN ('reconnected', 'expired', 'cancelled')
        OR NEW.revision <> OLD.revision + 1
        OR NEW.updated_at <= OLD.updated_at THEN
        RAISE EXCEPTION 'invalid Arena reconnect interval CAS transition'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
