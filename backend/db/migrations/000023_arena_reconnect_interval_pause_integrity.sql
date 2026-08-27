-- +goose Up

-- Reconnect evidence may already have synthetic writers. Freeze the pause,
-- presence, counter, and interval surfaces while retained ownership is checked
-- and the interval guard is replaced atomically.
LOCK TABLE
    arena_pauses,
    arena_presence_states,
    arena_reconnect_slot_counters,
    arena_reconnect_intervals
IN SHARE ROW EXCLUSIVE MODE;

-- Closed intervals may belong to a resolved pause, but every interval must
-- retain the owning Game identity and no open interval may outlive its pause.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM arena_reconnect_intervals AS reconnect_interval
        JOIN arena_pauses AS pause
            ON pause.id = reconnect_interval.pause_id
            AND pause.series_id = reconnect_interval.series_id
            AND pause.roster_id = reconnect_interval.roster_id
        WHERE pause.scope_kind <> 'game_attempt'
            OR pause.game_attempt_id
                IS DISTINCT FROM reconnect_interval.game_attempt_id
            OR reconnect_interval.opened_at < pause.started_at
            OR (
                reconnect_interval.state = 'open'
                AND pause.state <> 'active'
            )
    ) THEN
        RAISE EXCEPTION 'Arena reconnect interval violates its owning Game pause'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;
-- +goose StatementEnd

-- Lock the owning pause before live presence and the slot counter. A concurrent
-- pause resolution therefore either observes the new open interval or commits
-- first and causes the insert to fail against terminal pause state.
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

-- +goose Down

-- Downgrading lets new intervals cross a Game-pause boundary or start after
-- pause resolution. Production recovery must preserve the corrected guard and
-- use roll-forward or a verified restore.
LOCK TABLE
    arena_pauses,
    arena_presence_states,
    arena_reconnect_slot_counters,
    arena_reconnect_intervals
IN SHARE ROW EXCLUSIVE MODE;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION arena_reconnect_interval_guard() RETURNS TRIGGER AS $$
DECLARE
    live_state VARCHAR(16);
    live_epoch BIGINT;
    counter_slots SMALLINT;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Arena reconnect intervals are durable history'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_OP = 'INSERT' THEN
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
