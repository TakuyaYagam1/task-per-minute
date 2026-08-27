-- +goose Up

-- Replace the reconnect guards without racing live Arena writes. The current
-- application does not consume these tables yet, but the locks keep migration
-- behavior deterministic if an operator has started synthetic validation.
LOCK TABLE
    arena_pauses,
    arena_presence_states,
    arena_reconnect_intervals,
    arena_resume_decisions
IN SHARE ROW EXCLUSIVE MODE;

-- A reconnect interval is a CAS decision over the live presence epoch. Lock
-- the row until the surrounding interval and slot-counter transaction commits.
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

-- Resume decisions lock reconnect intervals before presence rows. This matches
-- the existing reconnect transaction order (interval, then presence) and
-- prevents a decision from committing evidence read before either CAS update.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION arena_resume_decision_insert_guard() RETURNS TRIGGER AS $$
DECLARE
    pause_row arena_pauses%ROWTYPE;
    series_first UUID;
    series_second UUID;
    first_snapshot_state VARCHAR(16);
    second_snapshot_state VARCHAR(16);
    first_live arena_presence_states%ROWTYPE;
    second_live arena_presence_states%ROWTYPE;
    first_interval_state VARCHAR(16);
    second_interval_state VARCHAR(16);
    previous_number BIGINT;
BEGIN
    SELECT *
    INTO pause_row
    FROM arena_pauses
    WHERE id = NEW.pause_id
    FOR UPDATE;

    IF pause_row.state <> 'active' OR pause_row.series_id IS NULL THEN
        RAISE EXCEPTION 'Arena resume decision requires an active Series pause'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT first_participant_id, second_participant_id
    INTO series_first, series_second
    FROM arena_series
    WHERE id = pause_row.series_id;

    SELECT presence_state
    INTO first_snapshot_state
    FROM arena_pause_presence_snapshots
    WHERE pause_id = NEW.pause_id AND participant_id = NEW.first_participant_id;

    SELECT presence_state
    INTO second_snapshot_state
    FROM arena_pause_presence_snapshots
    WHERE pause_id = NEW.pause_id AND participant_id = NEW.second_participant_id;

    SELECT state
    INTO first_interval_state
    FROM arena_reconnect_intervals
    WHERE id = NEW.first_reconnect_interval_id
    FOR UPDATE;

    SELECT state
    INTO second_interval_state
    FROM arena_reconnect_intervals
    WHERE id = NEW.second_reconnect_interval_id
    FOR UPDATE;

    SELECT *
    INTO first_live
    FROM arena_presence_states
    WHERE series_id = pause_row.series_id
        AND participant_id = NEW.first_participant_id
    FOR UPDATE;

    SELECT *
    INTO second_live
    FROM arena_presence_states
    WHERE series_id = pause_row.series_id
        AND participant_id = NEW.second_participant_id
    FOR UPDATE;

    SELECT MAX(decision_number)
    INTO previous_number
    FROM arena_resume_decisions
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
        RAISE EXCEPTION 'Arena resume decision does not match durable presence evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down

-- Downgrading reopens the stale-read race. Production recovery should keep the
-- corrected guards and use roll-forward rather than this compatibility down.
LOCK TABLE
    arena_pauses,
    arena_presence_states,
    arena_reconnect_intervals,
    arena_resume_decisions
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
        WHERE series_id = NEW.series_id AND participant_id = NEW.participant_id;

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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION arena_resume_decision_insert_guard() RETURNS TRIGGER AS $$
DECLARE
    pause_row arena_pauses%ROWTYPE;
    series_first UUID;
    series_second UUID;
    first_snapshot_state VARCHAR(16);
    second_snapshot_state VARCHAR(16);
    first_live arena_presence_states%ROWTYPE;
    second_live arena_presence_states%ROWTYPE;
    first_interval_state VARCHAR(16);
    second_interval_state VARCHAR(16);
    previous_number BIGINT;
BEGIN
    SELECT *
    INTO pause_row
    FROM arena_pauses
    WHERE id = NEW.pause_id
    FOR UPDATE;

    IF pause_row.state <> 'active' OR pause_row.series_id IS NULL THEN
        RAISE EXCEPTION 'Arena resume decision requires an active Series pause'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT first_participant_id, second_participant_id
    INTO series_first, series_second
    FROM arena_series
    WHERE id = pause_row.series_id;

    SELECT presence_state
    INTO first_snapshot_state
    FROM arena_pause_presence_snapshots
    WHERE pause_id = NEW.pause_id AND participant_id = NEW.first_participant_id;

    SELECT presence_state
    INTO second_snapshot_state
    FROM arena_pause_presence_snapshots
    WHERE pause_id = NEW.pause_id AND participant_id = NEW.second_participant_id;

    SELECT *
    INTO first_live
    FROM arena_presence_states
    WHERE series_id = pause_row.series_id
        AND participant_id = NEW.first_participant_id;

    SELECT *
    INTO second_live
    FROM arena_presence_states
    WHERE series_id = pause_row.series_id
        AND participant_id = NEW.second_participant_id;

    SELECT state
    INTO first_interval_state
    FROM arena_reconnect_intervals
    WHERE id = NEW.first_reconnect_interval_id;

    SELECT state
    INTO second_interval_state
    FROM arena_reconnect_intervals
    WHERE id = NEW.second_reconnect_interval_id;

    SELECT MAX(decision_number)
    INTO previous_number
    FROM arena_resume_decisions
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
        RAISE EXCEPTION 'Arena resume decision does not match durable presence evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
