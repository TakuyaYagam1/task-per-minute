-- +goose Up

-- Pause snapshots are durable recovery evidence. Freeze their owning pause and
-- live-presence surfaces while the insert guard gains a consistent lock order.
LOCK TABLE
    arena_pauses,
    arena_pause_presence_snapshots,
    arena_presence_states
IN SHARE ROW EXCLUSIVE MODE;

-- Lock pause before live presence, matching reconnect and resume operations.
-- The snapshot transaction retains the presence row until commit, so a
-- concurrent presence CAS either becomes part of the captured boundary or
-- waits until the complete pre-pause snapshot is durable.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION arena_pause_presence_snapshot_guard()
RETURNS TRIGGER AS $$
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
    FROM arena_pauses
    WHERE id = NEW.pause_id
    FOR UPDATE;

    IF pause_roster_id IS DISTINCT FROM NEW.roster_id
        OR (
            pause_series_id IS NOT NULL
            AND pause_series_id IS DISTINCT FROM NEW.series_id
        ) THEN
        RAISE EXCEPTION 'Arena pause snapshot crosses roster boundary'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT tournament_id, roster_id, state, presence_epoch, revision
    INTO live_tournament_id, live_roster_id, live_state, live_epoch, live_revision
    FROM arena_presence_states
    WHERE series_id = NEW.series_id AND participant_id = NEW.participant_id
    FOR UPDATE;

    IF live_tournament_id IS DISTINCT FROM pause_tournament_id
        OR live_roster_id IS DISTINCT FROM pause_roster_id
        OR live_state IS DISTINCT FROM NEW.presence_state
        OR live_epoch IS DISTINCT FROM NEW.presence_epoch
        OR live_revision IS DISTINCT FROM NEW.presence_revision THEN
        RAISE EXCEPTION 'Arena pre-pause snapshot must capture current presence CAS'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down

-- Downgrading reopens the stale snapshot race. Production recovery must keep
-- the corrected lock order and use roll-forward or a verified restore.
LOCK TABLE
    arena_pauses,
    arena_pause_presence_snapshots,
    arena_presence_states
IN SHARE ROW EXCLUSIVE MODE;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION arena_pause_presence_snapshot_guard()
RETURNS TRIGGER AS $$
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
    FROM arena_pauses
    WHERE id = NEW.pause_id;

    IF pause_roster_id IS DISTINCT FROM NEW.roster_id
        OR (
            pause_series_id IS NOT NULL
            AND pause_series_id IS DISTINCT FROM NEW.series_id
        ) THEN
        RAISE EXCEPTION 'Arena pause snapshot crosses roster boundary'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT tournament_id, roster_id, state, presence_epoch, revision
    INTO live_tournament_id, live_roster_id, live_state, live_epoch, live_revision
    FROM arena_presence_states
    WHERE series_id = NEW.series_id AND participant_id = NEW.participant_id;

    IF live_tournament_id IS DISTINCT FROM pause_tournament_id
        OR live_roster_id IS DISTINCT FROM pause_roster_id
        OR live_state IS DISTINCT FROM NEW.presence_state
        OR live_epoch IS DISTINCT FROM NEW.presence_epoch
        OR live_revision IS DISTINCT FROM NEW.presence_revision THEN
        RAISE EXCEPTION 'Arena pre-pause snapshot must capture current presence CAS'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
