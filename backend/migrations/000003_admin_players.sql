-- +goose Up

ALTER TABLE players
    ADD COLUMN deleted_at TIMESTAMPTZ;

CREATE TABLE player_leaderboard_overrides (
    player_id UUID PRIMARY KEY REFERENCES players(id) ON DELETE CASCADE,
    wins INTEGER NOT NULL CHECK (wins >= 0),
    average_solve_time_ms BIGINT NOT NULL CHECK (average_solve_time_ms >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (
        (wins = 0 AND average_solve_time_ms = 0)
        OR (wins > 0 AND average_solve_time_ms > 0)
    )
);

CREATE INDEX players_deleted_at_idx ON players (deleted_at);

-- +goose Down

DROP INDEX IF EXISTS players_deleted_at_idx;
DROP TABLE IF EXISTS player_leaderboard_overrides;
ALTER TABLE players
    DROP COLUMN IF EXISTS deleted_at;
