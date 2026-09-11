-- name: LockTournamentLifecycleScope :one
SELECT roster.id
FROM tournaments AS tournament
JOIN rosters AS roster ON roster.tournament_id = tournament.id
WHERE tournament.id = sqlc.arg(tournament_id)
FOR UPDATE OF tournament, roster;

-- name: LockTournamentLifecycleAuthority :one
SELECT tournament.id,
    roster.id AS roster_id,
    tournament.preset,
    tournament.name,
    tournament.public_id,
    tournament.planned_roster_size,
    tournament.content_revision,
    tournament.state,
    tournament.paused_from_state,
    tournament.revision AS tournament_revision,
    tournament.created_at AS tournament_created_at,
    tournament.updated_at AS tournament_updated_at,
    tournament.started_at AS tournament_started_at,
    tournament.finished_at AS tournament_finished_at,
    roster_size.participant_count AS roster_size,
    projection.id AS projection_revision_id,
    projection.revision_number AS projection_revision
FROM tournaments AS tournament
JOIN rosters AS roster ON roster.tournament_id = tournament.id
JOIN LATERAL (
    SELECT revision.id, revision.revision_number
    FROM projection_revisions AS revision
    WHERE revision.tournament_id = tournament.id
        AND revision.roster_id = roster.id
        AND revision.state = 'published'
    ORDER BY revision.revision_number DESC, revision.id DESC
    LIMIT 1
    FOR UPDATE
) AS projection ON TRUE
JOIN LATERAL (
    SELECT COUNT(*)::INTEGER AS participant_count
    FROM participants AS participant
    WHERE participant.roster_id = roster.id
) AS roster_size ON TRUE
WHERE tournament.id = sqlc.arg(tournament_id)
FOR UPDATE OF tournament, roster;

-- name: FindTournamentLifecycleCommand :one
SELECT command_id,
    tournament_id,
    roster_id,
    actor_id,
    action,
    source_projection_revision_id,
    source_projection_revision,
    source_tournament_revision,
    source_tournament_state,
    resulting_tournament_revision,
    resulting_tournament_state,
    reason,
    pause_id,
    source_pause_command_id,
    execution_snapshot,
    preset,
    roster_size,
    tournament_created_at,
    tournament_updated_at,
    tournament_started_at,
    tournament_finished_at,
    executed_at,
    created_at
FROM tournament_lifecycle_commands
WHERE tournament_id = sqlc.arg(tournament_id)
    AND command_id = sqlc.arg(command_id);

-- name: CreateTournamentLifecycleCommand :one
INSERT INTO tournament_lifecycle_commands (
    command_id,
    tournament_id,
    roster_id,
    actor_id,
    action,
    source_projection_revision_id,
    source_projection_revision,
    source_tournament_revision,
    source_tournament_state,
    resulting_tournament_revision,
    resulting_tournament_state,
    reason,
    pause_id,
    source_pause_command_id,
    execution_snapshot,
    preset,
    roster_size,
    tournament_created_at,
    tournament_updated_at,
    tournament_started_at,
    tournament_finished_at,
    executed_at,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(actor_id),
    sqlc.arg(action),
    sqlc.arg(source_projection_revision_id),
    sqlc.arg(source_projection_revision),
    sqlc.arg(source_tournament_revision),
    sqlc.arg(source_tournament_state),
    sqlc.arg(resulting_tournament_revision),
    sqlc.arg(resulting_tournament_state),
    sqlc.narg(reason)::TEXT,
    sqlc.narg(pause_id)::UUID,
    sqlc.narg(source_pause_command_id)::UUID,
    sqlc.narg(execution_snapshot)::JSONB,
    sqlc.arg(preset),
    sqlc.arg(roster_size),
    sqlc.arg(tournament_created_at),
    sqlc.arg(tournament_updated_at),
    sqlc.narg(tournament_started_at)::TIMESTAMPTZ,
    sqlc.narg(tournament_finished_at)::TIMESTAMPTZ,
    sqlc.arg(executed_at),
    sqlc.arg(executed_at)
)
RETURNING command_id;

-- name: LockTournamentLifecycleWaves :many
SELECT id
FROM waves
WHERE tournament_id = sqlc.arg(tournament_id) AND roster_id = sqlc.arg(roster_id)
ORDER BY id
FOR UPDATE;

-- name: LockTournamentLifecycleSeries :many
SELECT id
FROM series
WHERE tournament_id = sqlc.arg(tournament_id) AND roster_id = sqlc.arg(roster_id)
ORDER BY id
FOR UPDATE;

-- name: LockTournamentLifecycleGames :many
SELECT attempt.id
FROM game_attempts AS attempt
JOIN series ON series.id = attempt.series_id AND series.roster_id = attempt.roster_id
WHERE series.tournament_id = sqlc.arg(tournament_id) AND attempt.roster_id = sqlc.arg(roster_id)
ORDER BY attempt.id
FOR UPDATE OF attempt;

-- name: LockTournamentLifecycleDraftRevisions :many
SELECT revision.id
FROM draft_revisions AS revision
JOIN drafts AS draft ON draft.id = revision.draft_id AND draft.roster_id = revision.roster_id
JOIN series ON series.id = draft.series_id AND series.roster_id = draft.roster_id
WHERE series.tournament_id = sqlc.arg(tournament_id) AND revision.roster_id = sqlc.arg(roster_id)
ORDER BY revision.id
FOR UPDATE OF revision;

-- name: LockTournamentLifecycleReadyWindows :many
SELECT ready_window.id
FROM ready_windows AS ready_window
JOIN waves AS wave ON wave.id = ready_window.wave_id AND wave.roster_id = ready_window.roster_id
WHERE wave.tournament_id = sqlc.arg(tournament_id) AND ready_window.roster_id = sqlc.arg(roster_id)
ORDER BY ready_window.id
FOR UPDATE OF ready_window;

-- name: LockTournamentLifecycleReadiness :many
SELECT readiness.participant_id
FROM wave_readiness AS readiness
JOIN waves AS wave ON wave.id = readiness.wave_id AND wave.roster_id = readiness.roster_id
WHERE wave.tournament_id = sqlc.arg(tournament_id) AND readiness.roster_id = sqlc.arg(roster_id)
ORDER BY readiness.wave_id, readiness.participant_id
FOR UPDATE OF readiness;

-- name: LockTournamentLifecycleAssignments :many
SELECT assignment.id
FROM assignments AS assignment
JOIN series ON series.id = assignment.series_id AND series.roster_id = assignment.roster_id
WHERE series.tournament_id = sqlc.arg(tournament_id) AND assignment.roster_id = sqlc.arg(roster_id)
ORDER BY assignment.id
FOR UPDATE OF assignment;

-- name: LockTournamentLifecycleChildPauses :many
SELECT pause.id
FROM pauses AS pause
WHERE pause.tournament_id = sqlc.arg(tournament_id)
    AND pause.roster_id = sqlc.arg(roster_id)
    AND pause.scope_kind <> 'tournament'
ORDER BY pause.id
FOR UPDATE OF pause;

-- name: LockTournamentLifecycleReconnect :many
SELECT reconnect.id
FROM reconnect_intervals AS reconnect
JOIN series ON series.id = reconnect.series_id AND series.roster_id = reconnect.roster_id
WHERE series.tournament_id = sqlc.arg(tournament_id) AND reconnect.roster_id = sqlc.arg(roster_id)
ORDER BY reconnect.id
FOR UPDATE OF reconnect;

-- name: LockTournamentLifecycleGolden :many
SELECT attempt.id
FROM golden_attempts AS attempt
WHERE attempt.tournament_id = sqlc.arg(tournament_id) AND attempt.roster_id = sqlc.arg(roster_id)
ORDER BY attempt.id
FOR UPDATE OF attempt;

-- name: GetTournamentExecutionRevisionSnapshot :one
SELECT public.tournament_execution_revision_snapshot(
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id)
) AS execution_snapshot;

-- name: CreateTournamentTechnicalPause :one
WITH authority AS MATERIALIZED (
    SELECT tournament.id,
        tournament.revision,
        tournament.state,
        projection.revision_number,
        public.tournament_execution_revision_snapshot(tournament.id, roster.id) AS execution_snapshot
    FROM tournaments AS tournament
    JOIN rosters AS roster ON roster.tournament_id = tournament.id
    JOIN projection_revisions AS projection
        ON projection.tournament_id = tournament.id
        AND projection.roster_id = roster.id
        AND projection.state = 'published'
    WHERE tournament.id = sqlc.arg(tournament_id)
        AND roster.id = sqlc.arg(roster_id)
        AND tournament.revision = sqlc.arg(expected_tournament_revision)
        AND tournament.state = sqlc.arg(expected_tournament_state)
        AND projection.revision_number = sqlc.arg(expected_projection_revision)
        AND public.tournament_execution_revision_snapshot(tournament.id, roster.id) = sqlc.arg(execution_snapshot)::JSONB
        AND (public.tournament_execution_revision_snapshot(tournament.id, roster.id) ->> 'incomplete_count')::BIGINT = 0
)
INSERT INTO pauses (
    id,
    tournament_id,
    roster_id,
    scope_kind,
    scope_id,
    reason,
    paused_from_state,
    state,
    current_revision_id,
    revision,
    started_at,
    created_at,
    updated_at
)
SELECT sqlc.arg(pause_id),
    authority.id AS tournament_id,
    sqlc.arg(roster_id),
    'tournament',
    authority.id AS scope_id,
    'operator',
    authority.state,
    'active',
    sqlc.arg(pause_revision_id),
    1,
    sqlc.arg(paused_at),
    sqlc.arg(paused_at),
    sqlc.arg(paused_at)
FROM authority
RETURNING id;

-- name: CreateTournamentTechnicalPauseRevision :one
INSERT INTO pause_revisions (
    id,
    pause_id,
    previous_revision_id,
    revision_number,
    state,
    transition_reason,
    created_at
)
SELECT sqlc.arg(pause_revision_id),
    pause.id,
    NULL,
    1,
    'active',
    NULL,
    sqlc.arg(paused_at)
FROM pauses AS pause
WHERE pause.id = sqlc.arg(pause_id)
    AND pause.tournament_id = sqlc.arg(tournament_id)
    AND pause.roster_id = sqlc.arg(roster_id)
    AND pause.scope_kind = 'tournament'
    AND pause.scope_id = pause.tournament_id
    AND pause.state = 'active'
    AND pause.current_revision_id = sqlc.arg(pause_revision_id)
    AND pause.revision = 1
RETURNING pause_id;

-- name: EnterTournamentTechnicalPause :one
WITH authority AS MATERIALIZED (
    SELECT tournament.id,
        tournament.revision,
        tournament.state
    FROM tournaments AS tournament
    JOIN rosters AS roster ON roster.tournament_id = tournament.id
    JOIN projection_revisions AS projection
        ON projection.tournament_id = tournament.id
        AND projection.roster_id = roster.id
        AND projection.state = 'published'
    JOIN pauses AS pause
        ON pause.tournament_id = tournament.id
        AND pause.roster_id = roster.id
        AND pause.id = sqlc.arg(pause_id)
        AND pause.scope_kind = 'tournament'
        AND pause.scope_id = tournament.id
        AND pause.state = 'active'
        AND pause.current_revision_id = sqlc.arg(pause_revision_id)
        AND pause.revision = 1
    JOIN pause_revisions AS revision
        ON revision.pause_id = pause.id
        AND revision.id = pause.current_revision_id
        AND revision.revision_number = pause.revision
        AND revision.state = pause.state
    WHERE tournament.id = sqlc.arg(tournament_id)
        AND roster.id = sqlc.arg(roster_id)
        AND tournament.revision = sqlc.arg(expected_tournament_revision)
        AND tournament.state = sqlc.arg(expected_tournament_state)
        AND projection.revision_number = sqlc.arg(expected_projection_revision)
        AND public.tournament_execution_revision_snapshot(tournament.id, roster.id) = sqlc.arg(execution_snapshot)::JSONB
        AND (public.tournament_execution_revision_snapshot(tournament.id, roster.id) ->> 'incomplete_count')::BIGINT = 0
    FOR UPDATE OF tournament, roster, projection, pause
)
UPDATE tournaments AS tournament
SET state = 'technical_pause',
    paused_from_state = authority.state,
    revision = tournament.revision + 1,
    updated_at = sqlc.arg(paused_at)
FROM authority
WHERE tournament.id = authority.id
    AND tournament.revision = sqlc.arg(expected_tournament_revision)
    AND tournament.state = sqlc.arg(expected_tournament_state)
RETURNING tournament.id,
    tournament.state,
    tournament.paused_from_state,
    tournament.revision,
    tournament.updated_at;

-- name: ResumeTournamentTechnicalPause :one
WITH authority AS MATERIALIZED (
    SELECT tournament.id,
        tournament.revision,
        tournament.paused_from_state,
        pause.id AS pause_id,
        pause.current_revision_id,
        pause.revision AS pause_revision,
        source.command_id AS source_pause_command_id,
        source.execution_snapshot
    FROM tournaments AS tournament
    JOIN rosters AS roster ON roster.tournament_id = tournament.id
    JOIN pauses AS pause
        ON pause.tournament_id = tournament.id
        AND pause.roster_id = roster.id
        AND pause.scope_kind = 'tournament'
        AND pause.scope_id = tournament.id
        AND pause.state = 'active'
    JOIN tournament_lifecycle_commands AS source
        ON source.tournament_id = tournament.id
        AND source.roster_id = roster.id
        AND source.action = 'pause'
        AND source.pause_id = pause.id
    JOIN projection_revisions AS projection
        ON projection.tournament_id = tournament.id
        AND projection.roster_id = roster.id
        AND projection.state = 'published'
    WHERE tournament.id = sqlc.arg(tournament_id)
        AND roster.id = sqlc.arg(roster_id)
        AND tournament.revision = sqlc.arg(expected_tournament_revision)
        AND tournament.state = 'technical_pause'
        AND tournament.paused_from_state = source.source_tournament_state
        AND source.resulting_tournament_revision = tournament.revision
        AND projection.revision_number = sqlc.arg(expected_projection_revision)
        AND source.execution_snapshot = sqlc.arg(execution_snapshot)::JSONB
        AND public.tournament_execution_revision_snapshot(tournament.id, roster.id) = source.execution_snapshot
        AND (source.execution_snapshot ->> 'incomplete_count')::BIGINT = 0
    FOR UPDATE OF tournament, pause, source, projection
), created_revision AS (
    INSERT INTO pause_revisions (
        id,
        pause_id,
        previous_revision_id,
        revision_number,
        state,
        transition_reason,
        created_at
    )
    SELECT sqlc.arg(pause_revision_id),
        authority.pause_id,
        authority.current_revision_id,
        authority.pause_revision + 1,
        'resumed',
        sqlc.arg(reason),
        sqlc.arg(resumed_at)
    FROM authority
    RETURNING pause_id
), resumed_pause AS (
    UPDATE pauses AS pause
    SET state = 'resumed',
        current_revision_id = sqlc.arg(pause_revision_id),
        revision = pause.revision + 1,
        resolved_at = sqlc.arg(resumed_at),
        updated_at = sqlc.arg(resumed_at)
    FROM authority, created_revision
    WHERE pause.id = authority.pause_id
        AND created_revision.pause_id = pause.id
        AND pause.current_revision_id = authority.current_revision_id
        AND pause.revision = authority.pause_revision
        AND pause.state = 'active'
    RETURNING pause.id
), updated AS (
    UPDATE tournaments AS tournament
    SET state = authority.paused_from_state,
        paused_from_state = NULL,
        revision = tournament.revision + 1,
        updated_at = sqlc.arg(resumed_at)
    FROM authority, resumed_pause
    WHERE tournament.id = authority.id
        AND resumed_pause.id = authority.pause_id
        AND tournament.revision = sqlc.arg(expected_tournament_revision)
        AND tournament.state = 'technical_pause'
        AND tournament.paused_from_state = authority.paused_from_state
    RETURNING tournament.id,
        tournament.state,
        tournament.paused_from_state,
        tournament.revision,
        tournament.updated_at,
        authority.pause_id,
        authority.source_pause_command_id
)
SELECT id,
    state,
    paused_from_state,
    revision,
    updated_at,
    pause_id,
    source_pause_command_id
FROM updated;

-- name: CancelTournamentTechnicalPause :one
WITH authority AS MATERIALIZED (
    SELECT pause.id,
        pause.current_revision_id,
        pause.revision
    FROM pauses AS pause
    JOIN tournaments AS tournament ON tournament.id = pause.tournament_id
    WHERE pause.tournament_id = sqlc.arg(tournament_id)
        AND pause.roster_id = sqlc.arg(roster_id)
        AND pause.scope_kind = 'tournament'
        AND pause.scope_id = tournament.id
        AND pause.state = 'active'
        AND tournament.state = 'cancelled'
        AND tournament.revision = sqlc.arg(resulting_tournament_revision)
    FOR UPDATE OF pause
), created_revision AS (
    INSERT INTO pause_revisions (
        id,
        pause_id,
        previous_revision_id,
        revision_number,
        state,
        transition_reason,
        created_at
    )
    SELECT sqlc.arg(pause_revision_id),
        authority.id,
        authority.current_revision_id,
        authority.revision + 1,
        'cancelled',
        sqlc.arg(reason),
        sqlc.arg(cancelled_at)
    FROM authority
    RETURNING pause_id
)
UPDATE pauses AS pause
SET state = 'cancelled',
    current_revision_id = sqlc.arg(pause_revision_id),
    revision = pause.revision + 1,
    resolved_at = sqlc.arg(cancelled_at),
    updated_at = sqlc.arg(cancelled_at)
FROM authority, created_revision
WHERE pause.id = authority.id
    AND created_revision.pause_id = pause.id
    AND pause.current_revision_id = authority.current_revision_id
    AND pause.revision = authority.revision
    AND pause.state = 'active'
RETURNING pause.id;
