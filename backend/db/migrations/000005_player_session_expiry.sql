-- +goose Up

ALTER TABLE players
    ADD COLUMN session_expires_at TIMESTAMPTZ;

CREATE INDEX players_session_expires_at_idx
    ON players (session_expires_at)
    WHERE session_expires_at IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS players_session_expires_at_idx;

ALTER TABLE players
    DROP COLUMN IF EXISTS session_expires_at;
