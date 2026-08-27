-- +goose Up

-- Pause and reconnect evidence may already have synthetic writers. Freeze both
-- surfaces while retained terminal-pause ownership is checked and the cancel
-- guard is installed atomically. Ordinary reads remain available.
LOCK TABLE
    arena_pauses,
    arena_reconnect_intervals
IN SHARE ROW EXCLUSIVE MODE;

-- Migration 000023 prevents a new interval from starting under a terminal
-- pause. Also reject any interval that remained open after a pause was
-- cancelled before this transition guard existed.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM arena_reconnect_intervals AS reconnect_interval
        JOIN arena_pauses AS pause ON pause.id = reconnect_interval.pause_id
        WHERE reconnect_interval.state = 'open'
            AND pause.state <> 'active'
    ) THEN
        RAISE EXCEPTION 'terminal Arena pause retains an open reconnect interval'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;
-- +goose StatementEnd

-- Interval creation already locks pause before interval. Pause cancellation
-- follows the same order, then locks retained intervals deterministically. A
-- concurrent insert therefore commits first and blocks cancellation, or sees
-- the terminal pause after cancellation commits and fails its own guard.
-- +goose StatementBegin
CREATE FUNCTION arena_pause_cancel_reconnect_guard() RETURNS TRIGGER AS $$
BEGIN
    PERFORM 1
    FROM arena_reconnect_intervals
    WHERE pause_id = NEW.id
    ORDER BY participant_id, interval_number, id
    FOR UPDATE;

    IF EXISTS (
        SELECT 1
        FROM arena_reconnect_intervals
        WHERE pause_id = NEW.id AND state = 'open'
    ) THEN
        RAISE EXCEPTION 'Arena pause cancellation requires closed reconnect intervals'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_pause_cancel_reconnect_guard
BEFORE UPDATE ON arena_pauses
FOR EACH ROW
WHEN (OLD.state = 'active' AND NEW.state = 'cancelled')
EXECUTE FUNCTION arena_pause_cancel_reconnect_guard();

-- +goose Down

-- Downgrading permits an active pause to be cancelled while reconnect evidence
-- remains open. Production recovery must preserve the corrected guard and use
-- roll-forward or a verified restore.
LOCK TABLE
    arena_pauses,
    arena_reconnect_intervals
IN SHARE ROW EXCLUSIVE MODE;

DROP TRIGGER IF EXISTS arena_pause_cancel_reconnect_guard ON arena_pauses;
DROP FUNCTION IF EXISTS arena_pause_cancel_reconnect_guard();
