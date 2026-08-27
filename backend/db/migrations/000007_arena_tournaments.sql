-- +goose Up

CREATE TABLE arena_tournaments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    preset VARCHAR(32) NOT NULL DEFAULT 'arena_v1',
    state VARCHAR(32) NOT NULL DEFAULT 'draft',
    paused_from_state VARCHAR(32),
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    CONSTRAINT arena_tournaments_preset_check CHECK (preset = 'arena_v1'),
    CONSTRAINT arena_tournaments_state_check CHECK (
        state IN (
            'draft',
            'registration',
            'roster_locked',
            'swiss',
            'golden',
            'playoffs',
            'technical_pause',
            'completed',
            'cancelled'
        )
    ),
    CONSTRAINT arena_tournaments_pause_origin_check CHECK (
        (
            state = 'technical_pause'
            AND paused_from_state IS NOT NULL
            AND paused_from_state IN ('swiss', 'golden', 'playoffs')
        )
        OR (
            state <> 'technical_pause'
            AND paused_from_state IS NULL
        )
    ),
    CONSTRAINT arena_tournaments_revision_check CHECK (revision >= 1),
    CONSTRAINT arena_tournaments_timestamps_check CHECK (
        updated_at >= created_at
        AND (started_at IS NULL OR started_at >= created_at)
        AND (finished_at IS NULL OR finished_at >= created_at)
        AND (
            (state IN ('draft', 'registration', 'roster_locked') AND started_at IS NULL AND finished_at IS NULL)
            OR (state IN ('swiss', 'golden', 'playoffs', 'technical_pause') AND started_at IS NOT NULL AND finished_at IS NULL)
            OR (state = 'completed' AND started_at IS NOT NULL AND finished_at IS NOT NULL)
            OR (state = 'cancelled' AND finished_at IS NOT NULL)
        )
    )
);

-- A constant-expression partial unique index makes the active-event slot a
-- database invariant, including concurrent lifecycle transitions.
CREATE UNIQUE INDEX arena_tournaments_single_active_idx
    ON arena_tournaments ((1))
    WHERE state IN ('swiss', 'golden', 'playoffs', 'technical_pause');

CREATE INDEX arena_tournaments_state_created_idx
    ON arena_tournaments (state, created_at DESC);

-- +goose Down

DROP INDEX IF EXISTS arena_tournaments_state_created_idx;
DROP INDEX IF EXISTS arena_tournaments_single_active_idx;
DROP TABLE IF EXISTS arena_tournaments;
