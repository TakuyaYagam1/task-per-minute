-- name: CreateArenaTournament :one
INSERT INTO arena_tournaments (
    id,
    preset,
    state,
    revision,
    created_at,
    updated_at
)
VALUES (
    sqlc.arg(id),
    'arena_v1',
    'draft',
    1,
    sqlc.arg(created_at),
    sqlc.arg(created_at)
)
RETURNING id,
    preset,
    state,
    paused_from_state,
    revision,
    created_at,
    updated_at,
    started_at,
    finished_at;

-- name: CreateArenaRoster :one
INSERT INTO arena_rosters (
    id,
    tournament_id,
    revision,
    created_at,
    updated_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    1,
    sqlc.arg(created_at),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    revision,
    locked_at,
    execution_started_at,
    created_at,
    updated_at;

-- name: GetArenaTournament :one
SELECT id,
    preset,
    state,
    paused_from_state,
    revision,
    created_at,
    updated_at,
    started_at,
    finished_at
FROM arena_tournaments
WHERE id = sqlc.arg(id);

-- name: GetActiveArenaTournament :one
SELECT id,
    preset,
    state,
    paused_from_state,
    revision,
    created_at,
    updated_at,
    started_at,
    finished_at
FROM arena_tournaments
WHERE state IN ('swiss', 'golden', 'playoffs', 'technical_pause');

-- name: ListArenaTournaments :many
SELECT id,
    preset,
    state,
    paused_from_state,
    revision,
    created_at,
    updated_at,
    started_at,
    finished_at
FROM arena_tournaments
ORDER BY created_at DESC,
    id;

-- name: UpdateArenaTournamentCAS :one
UPDATE arena_tournaments
SET state = sqlc.arg(next_state),
    paused_from_state = sqlc.narg(paused_from_state)::VARCHAR,
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at),
    started_at = sqlc.narg(started_at)::TIMESTAMPTZ,
    finished_at = sqlc.narg(finished_at)::TIMESTAMPTZ
WHERE id = sqlc.arg(id)
    AND revision = sqlc.arg(expected_revision)
    AND state = sqlc.arg(expected_state)
RETURNING id,
    preset,
    state,
    paused_from_state,
    revision,
    created_at,
    updated_at,
    started_at,
    finished_at;

-- name: GetArenaRoster :one
SELECT id,
    tournament_id,
    revision,
    locked_at,
    execution_started_at,
    created_at,
    updated_at
FROM arena_rosters
WHERE id = sqlc.arg(id);

-- name: LockArenaRosterForUpdate :one
SELECT id,
    tournament_id,
    revision,
    locked_at,
    execution_started_at,
    created_at,
    updated_at
FROM arena_rosters
WHERE id = sqlc.arg(id)
FOR UPDATE;

-- name: InsertArenaParticipant :one
INSERT INTO arena_participants (
    id,
    roster_id,
    player_id,
    seed,
    attendance,
    created_at,
    updated_at
)
SELECT sqlc.arg(id),
    roster.id,
    sqlc.arg(player_id),
    sqlc.arg(seed),
    sqlc.arg(attendance),
    sqlc.arg(created_at),
    sqlc.arg(created_at)
FROM arena_rosters AS roster
WHERE roster.id = sqlc.arg(roster_id)
    AND roster.locked_at IS NULL
    AND roster.execution_started_at IS NULL
RETURNING id,
    roster_id,
    player_id,
    seed,
    attendance,
    created_at,
    updated_at;

-- name: UpdateArenaParticipantAttendanceCAS :one
UPDATE arena_participants AS participant
SET attendance = sqlc.arg(next_attendance),
    updated_at = sqlc.arg(updated_at)
FROM arena_rosters AS roster
WHERE participant.id = sqlc.arg(id)
    AND participant.attendance = sqlc.arg(expected_attendance)
    AND roster.id = participant.roster_id
    AND roster.locked_at IS NULL
    AND roster.execution_started_at IS NULL
RETURNING participant.id,
    participant.roster_id,
    participant.player_id,
    participant.seed,
    participant.attendance,
    participant.created_at,
    participant.updated_at;

-- name: ListArenaParticipants :many
SELECT participant.id,
    participant.roster_id,
    roster.tournament_id,
    participant.player_id,
    participant.seed,
    participant.attendance,
    participant.created_at,
    participant.updated_at
FROM arena_participants AS participant
JOIN arena_rosters AS roster ON roster.id = participant.roster_id
WHERE participant.roster_id = sqlc.arg(roster_id)
ORDER BY participant.seed,
    participant.id;

-- name: ListCheckedInArenaPlayerIDs :many
SELECT player_id
FROM arena_participants
WHERE roster_id = sqlc.arg(roster_id)
    AND attendance = 'checked_in'
ORDER BY player_id;

-- name: ReserveCheckedInArenaParticipants :many
INSERT INTO participant_reservations (
    player_id,
    owner_kind,
    owner_id,
    arena_tournament_id,
    revision,
    acquired_at,
    updated_at
)
SELECT participant.player_id,
    'arena',
    roster.tournament_id AS reservation_owner_id,
    roster.tournament_id AS reservation_tournament_id,
    1,
    sqlc.arg(acquired_at),
    sqlc.arg(acquired_at)
FROM arena_participants AS participant
JOIN arena_rosters AS roster ON roster.id = participant.roster_id
WHERE participant.roster_id = sqlc.arg(roster_id)
    AND participant.attendance = 'checked_in'
ORDER BY participant.player_id
ON CONFLICT (player_id) DO NOTHING
RETURNING player_id;

-- name: LockArenaRosterCAS :one
UPDATE arena_rosters
SET revision = revision + 1,
    locked_at = sqlc.arg(locked_at),
    updated_at = sqlc.arg(locked_at)
WHERE id = sqlc.arg(id)
    AND revision = sqlc.arg(expected_revision)
    AND locked_at IS NULL
    AND execution_started_at IS NULL
RETURNING id,
    tournament_id,
    revision,
    locked_at,
    execution_started_at,
    created_at,
    updated_at;

-- name: UnlockArenaRosterCAS :one
UPDATE arena_rosters
SET revision = revision + 1,
    locked_at = NULL,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
    AND revision = sqlc.arg(expected_revision)
    AND locked_at IS NOT NULL
    AND execution_started_at IS NULL
RETURNING id,
    tournament_id,
    revision,
    locked_at,
    execution_started_at,
    created_at,
    updated_at;

-- name: MarkArenaRosterExecutionStartedCAS :one
UPDATE arena_rosters
SET revision = revision + 1,
    execution_started_at = sqlc.arg(started_at),
    updated_at = sqlc.arg(started_at)
WHERE id = sqlc.arg(id)
    AND revision = sqlc.arg(expected_revision)
    AND locked_at IS NOT NULL
    AND execution_started_at IS NULL
RETURNING id,
    tournament_id,
    revision,
    locked_at,
    execution_started_at,
    created_at,
    updated_at;

-- name: ReleaseArenaReservations :execrows
DELETE FROM participant_reservations
WHERE owner_kind = 'arena'
    AND owner_id = sqlc.arg(tournament_id)
    AND arena_tournament_id = sqlc.arg(tournament_id);

-- name: ListArenaReservations :many
SELECT player_id,
    reservation_id,
    owner_kind,
    owner_id,
    arena_tournament_id,
    casual_duel_id,
    revision,
    acquired_at,
    updated_at
FROM participant_reservations
WHERE owner_kind = 'arena'
    AND arena_tournament_id = sqlc.arg(tournament_id)
ORDER BY player_id;
