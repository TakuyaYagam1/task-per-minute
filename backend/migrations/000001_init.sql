-- +goose Up
CREATE TABLE players (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username VARCHAR(50) NOT NULL UNIQUE,
    session_token UUID,
    status VARCHAR(20) NOT NULL DEFAULT 'idle' CHECK (status IN ('idle', 'queued', 'in_duel')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title VARCHAR(255) NOT NULL,
    description TEXT NOT NULL,
    category VARCHAR(50) NOT NULL CHECK (
        category IN (
            'web',
            'crypto',
            'forensics',
            'reverse',
            'pwn',
            'misc'
        )
    ),
    difficulty VARCHAR(10) NOT NULL CHECK (difficulty IN ('easy', 'medium', 'hard')),
    time_limit INTEGER NOT NULL CHECK (time_limit > 0),
    flag VARCHAR(255) NOT NULL,
    hint_1 TEXT,
    hint_2 TEXT,
    hint_3 TEXT,
    task_url TEXT,
    source_file_url TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE duels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    player1_id UUID NOT NULL REFERENCES players(id) ON DELETE RESTRICT,
    player2_id UUID NOT NULL REFERENCES players(id) ON DELETE RESTRICT,
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'finished')),
    winner_id UUID REFERENCES players(id) ON DELETE RESTRICT,
    deadline TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    CHECK (player1_id <> player2_id),
    CHECK (
        winner_id IS NULL
        OR winner_id = player1_id
        OR winner_id = player2_id
    ),
    CHECK (
        (status = 'finished') = (finished_at IS NOT NULL)
    )
);
CREATE TABLE duel_player_tasks (
    duel_id UUID NOT NULL REFERENCES duels(id) ON DELETE CASCADE,
    player_id UUID NOT NULL REFERENCES players(id) ON DELETE RESTRICT,
    task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    solved BOOLEAN NOT NULL DEFAULT FALSE,
    solved_at TIMESTAMPTZ,
    PRIMARY KEY (duel_id, player_id),
    CHECK (solved = (solved_at IS NOT NULL))
);
CREATE TABLE player_task_history (
    player_id UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    solved_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (player_id, task_id)
);
-- +goose Down
DROP TABLE IF EXISTS player_task_history;
DROP TABLE IF EXISTS duel_player_tasks;
DROP TABLE IF EXISTS duels;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS players;
