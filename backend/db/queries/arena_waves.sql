-- name: CreateArenaWave :one
INSERT INTO arena_waves (
    id,
    tournament_id,
    roster_id,
    revision_id,
    revision,
    state,
    replaces_wave_id,
    created_at,
    updated_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(revision_id),
    1,
    'planned',
    sqlc.arg(replaces_wave_id),
    sqlc.arg(created_at),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    revision_id,
    revision,
    state,
    replaces_wave_id,
    created_at,
    updated_at,
    started_at,
    paused_at,
    closed_at;

-- name: CreateArenaWaveMember :exec
INSERT INTO arena_wave_members (
    wave_id,
    roster_id,
    participant_id,
    created_at
)
VALUES (
    sqlc.arg(wave_id),
    sqlc.arg(roster_id),
    sqlc.arg(participant_id),
    sqlc.arg(created_at)
);

-- name: CreateArenaReadyWindow :one
INSERT INTO arena_ready_windows (
    id,
    wave_id,
    roster_id,
    revision_id,
    state,
    opened_at,
    deadline,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(wave_id),
    sqlc.arg(roster_id),
    sqlc.arg(revision_id),
    'open',
    sqlc.arg(opened_at),
    sqlc.arg(deadline),
    sqlc.arg(opened_at)
)
RETURNING id,
    wave_id,
    roster_id,
    revision_id,
    state,
    opened_at,
    deadline,
    consumed_at,
    created_at;

-- name: OpenArenaWaveReadyWindowCAS :one
UPDATE arena_waves
SET state = 'ready_window_open',
    revision = revision + 1,
    updated_at = sqlc.arg(opened_at)
WHERE id = sqlc.arg(id)
    AND revision = sqlc.arg(expected_revision)
    AND state = 'planned'
RETURNING id,
    tournament_id,
    roster_id,
    revision_id,
    revision,
    state,
    replaces_wave_id,
    created_at,
    updated_at,
    started_at,
    paused_at,
    closed_at;

-- name: CreateArenaWaveReadiness :one
INSERT INTO arena_wave_readiness (
    ready_window_id,
    wave_id,
    roster_id,
    participant_id,
    ready_at
)
VALUES (
    sqlc.arg(ready_window_id),
    sqlc.arg(wave_id),
    sqlc.arg(roster_id),
    sqlc.arg(participant_id),
    sqlc.arg(ready_at)
)
ON CONFLICT (ready_window_id, participant_id) DO NOTHING
RETURNING ready_window_id,
    wave_id,
    roster_id,
    participant_id,
    ready_at;

-- name: CountArenaWaveReadiness :one
SELECT COUNT(*)
FROM arena_wave_readiness
WHERE ready_window_id = sqlc.arg(ready_window_id);

-- name: CountArenaWaveMembers :one
SELECT COUNT(*)
FROM arena_wave_members
WHERE wave_id = sqlc.arg(wave_id);

-- name: MarkArenaWaveReadinessCAS :one
UPDATE arena_waves
SET state = CASE
        WHEN sqlc.arg(all_ready)::BOOLEAN THEN 'ready'
        ELSE 'ready_window_open'
    END,
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
    AND revision = sqlc.arg(expected_revision)
    AND state IN ('ready_window_open', 'ready')
RETURNING id,
    tournament_id,
    roster_id,
    revision_id,
    revision,
    state,
    replaces_wave_id,
    created_at,
    updated_at,
    started_at,
    paused_at,
    closed_at;

-- name: ConsumeArenaReadyWindowCAS :one
UPDATE arena_ready_windows
SET state = 'consumed',
    consumed_at = sqlc.arg(started_at)
WHERE id = sqlc.arg(id)
    AND wave_id = sqlc.arg(wave_id)
    AND state = 'open'
    AND sqlc.arg(started_at)::TIMESTAMPTZ BETWEEN opened_at AND deadline
RETURNING id,
    wave_id,
    roster_id,
    revision_id,
    state,
    opened_at,
    deadline,
    consumed_at,
    created_at;

-- name: StartArenaWaveCAS :one
UPDATE arena_waves
SET state = 'active',
    revision = revision + 1,
    updated_at = sqlc.arg(started_at),
    started_at = sqlc.arg(started_at)
WHERE id = sqlc.arg(id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND revision = sqlc.arg(expected_revision)
    AND state = 'ready'
    AND started_at IS NULL
RETURNING id,
    tournament_id,
    roster_id,
    revision_id,
    revision,
    state,
    replaces_wave_id,
    created_at,
    updated_at,
    started_at,
    paused_at,
    closed_at;

-- name: CloseArenaReadyWindowCAS :one
UPDATE arena_ready_windows
SET state = sqlc.arg(next_state),
    consumed_at = NULL
WHERE id = sqlc.arg(id)
    AND wave_id = sqlc.arg(wave_id)
    AND state = sqlc.arg(expected_state)
RETURNING id,
    wave_id,
    roster_id,
    revision_id,
    state,
    opened_at,
    deadline,
    consumed_at,
    created_at;

-- name: TransitionArenaWaveCAS :one
UPDATE arena_waves
SET state = sqlc.arg(next_state),
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at),
    paused_at = sqlc.narg(paused_at)::TIMESTAMPTZ,
    closed_at = sqlc.narg(closed_at)::TIMESTAMPTZ
WHERE id = sqlc.arg(id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND revision = sqlc.arg(expected_revision)
    AND state = sqlc.arg(expected_state)
RETURNING id,
    tournament_id,
    roster_id,
    revision_id,
    revision,
    state,
    replaces_wave_id,
    created_at,
    updated_at,
    started_at,
    paused_at,
    closed_at;

-- name: GetArenaWave :one
SELECT id,
    tournament_id,
    roster_id,
    revision_id,
    revision,
    state,
    replaces_wave_id,
    created_at,
    updated_at,
    started_at,
    paused_at,
    closed_at
FROM arena_waves
WHERE id = sqlc.arg(id)
    AND tournament_id = sqlc.arg(tournament_id);

-- name: ListArenaWaveMembers :many
SELECT wave_id,
    roster_id,
    participant_id,
    created_at
FROM arena_wave_members
WHERE wave_id = sqlc.arg(wave_id)
ORDER BY participant_id;

-- name: GetArenaReadyWindow :one
SELECT id,
    wave_id,
    roster_id,
    revision_id,
    state,
    opened_at,
    deadline,
    consumed_at,
    created_at
FROM arena_ready_windows
WHERE wave_id = sqlc.arg(wave_id);

-- name: ListArenaWaveReadiness :many
SELECT ready_window_id,
    wave_id,
    roster_id,
    participant_id,
    ready_at
FROM arena_wave_readiness
WHERE ready_window_id = sqlc.arg(ready_window_id)
ORDER BY participant_id;
