-- +goose Up

CREATE TABLE arena_rosters (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tournament_id UUID NOT NULL UNIQUE REFERENCES arena_tournaments(id) ON DELETE RESTRICT,
    revision BIGINT NOT NULL DEFAULT 1,
    locked_at TIMESTAMPTZ,
    execution_started_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_rosters_revision_check CHECK (revision >= 1),
    CONSTRAINT arena_rosters_lock_check CHECK (
        execution_started_at IS NULL OR locked_at IS NOT NULL
    ),
    CONSTRAINT arena_rosters_timestamps_check CHECK (
        updated_at >= created_at
        AND (locked_at IS NULL OR locked_at >= created_at)
        AND (execution_started_at IS NULL OR execution_started_at >= locked_at)
    )
);

CREATE TABLE arena_participants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    roster_id UUID NOT NULL REFERENCES arena_rosters(id) ON DELETE RESTRICT,
    player_id UUID NOT NULL REFERENCES players(id) ON DELETE RESTRICT,
    seed INTEGER NOT NULL,
    attendance VARCHAR(20) NOT NULL DEFAULT 'invited',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_participants_seed_check CHECK (seed >= 1),
    CONSTRAINT arena_participants_attendance_check CHECK (
        attendance IN ('invited', 'registered', 'checked_in', 'withdrawn')
    ),
    CONSTRAINT arena_participants_timestamps_check CHECK (updated_at >= created_at),
    CONSTRAINT arena_participants_roster_player_key UNIQUE (roster_id, player_id),
    CONSTRAINT arena_participants_roster_seed_key UNIQUE (roster_id, seed)
);

-- The player primary key is the cross-mode arbitration boundary. owner_id is
-- the CAS identity; typed references retain referential integrity once an
-- Arena reservation or a casual queue claim is promoted to an active Duel.
CREATE TABLE participant_reservations (
    player_id UUID PRIMARY KEY REFERENCES players(id) ON DELETE CASCADE,
    reservation_id UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    owner_kind VARCHAR(20) NOT NULL,
    owner_id UUID NOT NULL,
    arena_tournament_id UUID REFERENCES arena_tournaments(id) ON DELETE RESTRICT,
    casual_duel_id UUID REFERENCES duels(id) ON DELETE RESTRICT,
    revision BIGINT NOT NULL DEFAULT 1,
    acquired_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT participant_reservations_owner_kind_check CHECK (
        owner_kind IN ('arena', 'casual_queue', 'casual_duel')
    ),
    CONSTRAINT participant_reservations_owner_shape_check CHECK (
        (
            owner_kind = 'arena'
            AND arena_tournament_id IS NOT NULL
            AND arena_tournament_id = owner_id
            AND casual_duel_id IS NULL
        )
        OR (
            owner_kind = 'casual_queue'
            AND arena_tournament_id IS NULL
            AND casual_duel_id IS NULL
        )
        OR (
            owner_kind = 'casual_duel'
            AND arena_tournament_id IS NULL
            AND casual_duel_id IS NOT NULL
            AND casual_duel_id = owner_id
        )
    ),
    CONSTRAINT participant_reservations_revision_check CHECK (revision >= 1),
    CONSTRAINT participant_reservations_timestamps_check CHECK (updated_at >= acquired_at)
);

CREATE INDEX participant_reservations_arena_tournament_idx
    ON participant_reservations (arena_tournament_id)
    WHERE arena_tournament_id IS NOT NULL;

CREATE INDEX participant_reservations_casual_duel_idx
    ON participant_reservations (casual_duel_id)
    WHERE casual_duel_id IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS participant_reservations_casual_duel_idx;
DROP INDEX IF EXISTS participant_reservations_arena_tournament_idx;
DROP TABLE IF EXISTS participant_reservations;
DROP TABLE IF EXISTS arena_participants;
DROP TABLE IF EXISTS arena_rosters;
