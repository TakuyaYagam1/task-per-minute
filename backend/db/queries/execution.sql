-- name: CreateWave :one
INSERT INTO waves (
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

-- name: CreateWaveMember :exec
INSERT INTO wave_members (
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

-- name: CreateWaveReadinessHead :one
INSERT INTO wave_readiness (
    wave_id,
    roster_id,
    participant_id,
    ready,
    revision,
    created_at,
    updated_at
)
VALUES (
    sqlc.arg(wave_id),
    sqlc.arg(roster_id),
    sqlc.arg(participant_id),
    false,
    1,
    sqlc.arg(created_at),
    sqlc.arg(created_at)
)
RETURNING ready_window_id,
    wave_id,
    roster_id,
    participant_id,
    ready,
    revision,
    ready_at,
    created_at,
    updated_at;

-- name: CreateSeries :one
INSERT INTO series (
    id,
    tournament_id,
    roster_id,
    first_participant_id,
    second_participant_id,
    format,
    state,
    revision,
    created_at,
    updated_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(first_participant_id),
    sqlc.arg(second_participant_id),
    sqlc.arg(format),
    'planned',
    1,
    sqlc.arg(created_at),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    first_participant_id,
    second_participant_id,
    format,
    state,
    first_participant_wins,
    second_participant_wins,
    winner_id,
    current_score_revision_id,
    current_result_revision_id,
    revision,
    created_at,
    updated_at,
    started_at,
    finished_at;

-- name: CreateInitialSeriesScoreRevision :exec
INSERT INTO series_score_revisions (
    id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    previous_revision_id,
    revision_number,
    operation,
    command_id,
    actor_kind,
    actor_id,
    command_attempt_id,
    source_projection_revision_id,
    source_projection_revision,
    first_participant_wins,
    second_participant_wins,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    NULL,
    NULL,
    1,
    'initialize',
    sqlc.arg(command_id),
    'server',
    NULL,
    NULL,
    sqlc.arg(source_projection_revision_id),
    sqlc.arg(source_projection_revision),
    0,
    0,
    sqlc.arg(created_at)
);

-- The initial score head is created with the planned Series and is the sole
-- allowed planned-state score authority. Binding the Series pointer here
-- prevents an orphan head or a mutable zero-score compatibility path.
-- name: CreateInitialSeriesScoreHead :one
WITH created_head AS (
    INSERT INTO series_score_heads (
        series_id,
        roster_id,
        current_revision_id,
        revision,
        updated_at
    )
    VALUES (
        sqlc.arg(series_id),
        sqlc.arg(roster_id),
        sqlc.arg(initial_score_revision_id),
        1,
        sqlc.arg(updated_at)
    )
    RETURNING series_id,
        roster_id,
        current_revision_id
),
bound_series AS (
    UPDATE series
    SET current_score_revision_id = created_head.current_revision_id
    FROM created_head
    WHERE series.id = created_head.series_id
        AND series.roster_id = created_head.roster_id
        AND series.state = 'planned'
        AND series.current_score_revision_id IS NULL
        AND series.current_result_revision_id IS NULL
    RETURNING series.id
)
SELECT id
FROM bound_series;

-- name: CreateWaveSeries :exec
INSERT INTO wave_series (
    wave_id,
    tournament_id,
    roster_id,
    series_id,
    created_at
)
VALUES (
    sqlc.arg(wave_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(created_at)
);

-- name: CreateReadyWindow :one
INSERT INTO ready_windows (
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

-- name: BindWaveReadinessHeads :many
UPDATE wave_readiness
SET ready_window_id = sqlc.arg(ready_window_id),
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE wave_id = sqlc.arg(wave_id)
    AND ready_window_id IS NULL
    AND NOT ready
    AND revision = 1
RETURNING ready_window_id,
    wave_id,
    roster_id,
    participant_id,
    ready,
    revision,
    ready_at,
    created_at,
    updated_at;

-- name: OpenWaveReadyWindowCAS :one
UPDATE waves
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

-- name: MarkWaveMemberReadyCAS :one
UPDATE wave_readiness
SET ready = true,
    ready_at = sqlc.arg(ready_at),
    revision = revision + 1,
    updated_at = sqlc.arg(ready_at)
WHERE wave_id = sqlc.arg(wave_id)
    AND participant_id = sqlc.arg(participant_id)
    AND ready_window_id = sqlc.arg(ready_window_id)
    AND revision = sqlc.arg(expected_revision)
    AND NOT ready
RETURNING ready_window_id,
    wave_id,
    roster_id,
    participant_id,
    ready,
    revision,
    ready_at,
    created_at,
    updated_at;

-- name: GetWaveReadinessHead :one
SELECT ready_window_id,
    wave_id,
    roster_id,
    participant_id,
    ready,
    revision,
    ready_at,
    created_at,
    updated_at
FROM wave_readiness
WHERE wave_id = sqlc.arg(wave_id)
    AND participant_id = sqlc.arg(participant_id);

-- name: CountWaveReadiness :one
SELECT COUNT(*)
FROM wave_readiness
WHERE ready_window_id = sqlc.arg(ready_window_id)
    AND ready;

-- name: CountWaveMembers :one
SELECT COUNT(*)
FROM wave_members
WHERE wave_id = sqlc.arg(wave_id);

-- name: MarkWaveReadinessCAS :one
UPDATE waves
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

-- name: ConsumeReadyWindowCAS :one
UPDATE ready_windows
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

-- name: StartWaveCAS :one
UPDATE waves
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

-- name: ClearWaveReadinessHeads :many
UPDATE wave_readiness
SET ready = false,
    ready_at = NULL,
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE wave_id = sqlc.arg(wave_id)
    AND ready_window_id = sqlc.arg(ready_window_id)
    AND ready
RETURNING ready_window_id,
    wave_id,
    roster_id,
    participant_id,
    ready,
    revision,
    ready_at,
    created_at,
    updated_at;

-- name: CloseReadyWindowCAS :one
UPDATE ready_windows
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

-- name: TransitionWaveCAS :one
UPDATE waves
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

-- Closing a Wave is a new immutable result authority identity. Keep this CAS
-- separate from transitions which intentionally retain the running identity.
-- name: CloseWaveCAS :one
UPDATE waves
SET state = 'completed',
    revision_id = sqlc.arg(revision_id)::UUID,
    revision = revision + 1,
    updated_at = sqlc.arg(closed_at)::TIMESTAMPTZ,
    paused_at = NULL,
    closed_at = sqlc.arg(closed_at)::TIMESTAMPTZ
WHERE id = sqlc.arg(id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND revision = sqlc.arg(expected_revision)
    AND state = 'active'
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

-- name: GetWave :one
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
FROM waves
WHERE id = sqlc.arg(id)
    AND tournament_id = sqlc.arg(tournament_id);

-- name: ListWaveMembers :many
SELECT wave_id,
    roster_id,
    participant_id,
    created_at
FROM wave_members
WHERE wave_id = sqlc.arg(wave_id)
ORDER BY participant_id;

-- name: ListWaveSeries :many
SELECT series.id,
    series.tournament_id,
    series.roster_id,
    series.first_participant_id,
    series.second_participant_id,
    series.format,
    series.state,
    series.first_participant_wins,
    series.second_participant_wins,
    series.winner_id,
    series.current_score_revision_id,
    series.current_result_revision_id,
    series.revision,
    series.created_at,
    series.updated_at,
    series.started_at,
    series.finished_at
FROM wave_series AS wave_series
JOIN series AS series ON series.id = wave_series.series_id
WHERE wave_series.wave_id = sqlc.arg(wave_id)
ORDER BY series.id;

-- name: GetReadyWindow :one
SELECT id,
    wave_id,
    roster_id,
    revision_id,
    state,
    opened_at,
    deadline,
    consumed_at,
    created_at
FROM ready_windows
WHERE wave_id = sqlc.arg(wave_id);

-- name: ListWaveReadinessHeads :many
SELECT ready_window_id,
    wave_id,
    roster_id,
    participant_id,
    ready,
    revision,
    ready_at,
    created_at,
    updated_at
FROM wave_readiness
WHERE wave_id = sqlc.arg(wave_id)
ORDER BY participant_id;
