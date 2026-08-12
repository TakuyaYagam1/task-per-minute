-- +goose Up

CREATE UNIQUE INDEX players_session_token_idx
    ON players (session_token)
    WHERE session_token IS NOT NULL;

CREATE INDEX tasks_difficulty_idx ON tasks (difficulty);

CREATE INDEX duel_player_tasks_player_duel_idx
    ON duel_player_tasks (player_id, duel_id);

CREATE INDEX player_task_history_player_idx
    ON player_task_history (player_id);

CREATE INDEX duels_status_deadline_idx ON duels (status, deadline);

-- +goose Down

DROP INDEX IF EXISTS duels_status_deadline_idx;
DROP INDEX IF EXISTS player_task_history_player_idx;
DROP INDEX IF EXISTS duel_player_tasks_player_duel_idx;
DROP INDEX IF EXISTS tasks_difficulty_idx;
DROP INDEX IF EXISTS players_session_token_idx;
