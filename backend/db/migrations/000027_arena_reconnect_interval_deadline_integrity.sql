-- +goose Up

-- Reconnect intervals retain server-authoritative deadline evidence. Freeze the
-- interval surface while existing terminal timestamps are checked and the
-- missing deadline constraint is installed atomically.
LOCK TABLE arena_reconnect_intervals IN SHARE ROW EXCLUSIVE MODE;

-- A terminal interval must close no later than its recorded update. A reconnect
-- is timely only through its deadline, while expiry cannot be recorded before
-- that deadline. Cancellation remains independent of deadline expiry.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM arena_reconnect_intervals
        WHERE state <> 'open'
            AND (
                closed_at > updated_at
                OR (state = 'reconnected' AND closed_at > deadline_at)
                OR (state = 'expired' AND closed_at < deadline_at)
            )
    ) THEN
        RAISE EXCEPTION 'terminal Arena reconnect interval violates its deadline evidence'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE arena_reconnect_intervals
    ADD CONSTRAINT arena_reconnect_intervals_terminal_time_check CHECK (
        state = 'open'
        OR (
            closed_at <= updated_at
            AND (
                (state = 'reconnected' AND closed_at <= deadline_at)
                OR (state = 'expired' AND closed_at >= deadline_at)
                OR state = 'cancelled'
            )
        )
    );

-- +goose Down

-- Downgrading permits early expiry, late reconnect, and terminal evidence whose
-- close time follows its update time. Production recovery must keep the
-- corrected constraint and use roll-forward or a verified restore.
LOCK TABLE arena_reconnect_intervals IN SHARE ROW EXCLUSIVE MODE;

ALTER TABLE arena_reconnect_intervals
    DROP CONSTRAINT IF EXISTS arena_reconnect_intervals_terminal_time_check;
