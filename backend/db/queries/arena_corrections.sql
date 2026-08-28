-- name: GetArenaCorrectionSource :one
SELECT revision.id,
    revision.tournament_id,
    revision.roster_id,
    revision.entity_kind,
    revision.entity_id,
    revision.series_id,
    revision.game_attempt_id,
    revision.result_event_id,
    revision.previous_revision_id,
    revision.revision_number,
    revision.result_state,
    revision.result_reason,
    revision.winner_id,
    revision.created_at,
    head.current_revision_id,
    head.revision AS head_revision
FROM arena_official_result_revisions AS revision
INNER JOIN arena_official_result_heads AS head
    ON head.entity_kind = revision.entity_kind
    AND head.entity_id = revision.entity_id
WHERE revision.id = sqlc.arg(source_revision_id)
    AND revision.tournament_id = sqlc.arg(tournament_id)
    AND revision.roster_id = sqlc.arg(roster_id)
    AND revision.entity_kind = 'game_attempt'
    AND revision.entity_id = sqlc.arg(attempt_id)
    AND revision.series_id = sqlc.arg(series_id);

-- name: ListArenaCorrectionDescendants :many
WITH RECURSIVE affected_artifact_ids(artifact_id) AS (
    SELECT dependency.artifact_id
    FROM arena_projection_dependencies AS dependency
    WHERE dependency.tournament_id = sqlc.arg(tournament_id)
        AND dependency.roster_id = sqlc.arg(roster_id)
        AND dependency.official_result_revision_id = sqlc.arg(source_revision_id)
    UNION
    SELECT dependency.artifact_id
    FROM arena_projection_dependencies AS dependency
    INNER JOIN affected_artifact_ids AS affected
        ON affected.artifact_id = dependency.depends_on_artifact_id
    WHERE dependency.tournament_id = sqlc.arg(tournament_id)
        AND dependency.roster_id = sqlc.arg(roster_id)
)
SELECT artifact.id AS artifact_id,
    artifact.artifact_kind,
    artifact.produced_by_revision_id,
    membership.revision_id,
    revision.revision_number,
    revision.state AS revision_state
FROM affected_artifact_ids AS affected
INNER JOIN arena_projection_artifacts AS artifact
    ON artifact.id = affected.artifact_id
INNER JOIN arena_projection_revision_artifacts AS membership
    ON membership.artifact_id = artifact.id
INNER JOIN arena_projection_revisions AS revision
    ON revision.id = membership.revision_id
WHERE artifact.tournament_id = sqlc.arg(tournament_id)
    AND artifact.roster_id = sqlc.arg(roster_id)
ORDER BY revision.revision_number, artifact.artifact_kind, artifact.id;

-- name: LockArenaCorrectionTournamentScope :one
SELECT tournament.id AS tournament_id,
    tournament.state AS tournament_state,
    roster.id AS roster_id
FROM arena_tournaments AS tournament
INNER JOIN arena_rosters AS roster
    ON roster.tournament_id = tournament.id
WHERE tournament.id = sqlc.arg(tournament_id)
    AND roster.id = sqlc.arg(roster_id)
FOR UPDATE OF tournament, roster;

-- name: LockArenaCorrectionSeriesAttempts :many
SELECT attempt.id
FROM arena_game_attempts AS attempt
INNER JOIN arena_series AS series
    ON series.id = attempt.series_id
    AND series.roster_id = attempt.roster_id
WHERE series.id = sqlc.arg(series_id)
    AND series.tournament_id = sqlc.arg(tournament_id)
    AND series.roster_id = sqlc.arg(roster_id)
ORDER BY attempt.id
FOR UPDATE OF attempt;

-- name: LockArenaCorrectionOfficialHeads :many
SELECT entity_kind,
    entity_id,
    series_id,
    roster_id,
    game_attempt_id,
    current_revision_id,
    revision,
    updated_at
FROM arena_official_result_heads
WHERE roster_id = sqlc.arg(roster_id)
    AND (
        (entity_kind = 'game_attempt' AND entity_id = sqlc.arg(attempt_id))
        OR (entity_kind = 'series' AND entity_id = sqlc.arg(series_id))
    )
ORDER BY entity_kind
FOR UPDATE;

-- name: LockArenaCorrectionCutoffWaves :many
SELECT id,
    state,
    started_at
FROM arena_waves
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY id
FOR UPDATE;

-- name: LockArenaCorrectionAssignments :many
SELECT assignment.id
FROM arena_assignments AS assignment
INNER JOIN arena_series AS series ON series.id = assignment.series_id
WHERE series.tournament_id = sqlc.arg(tournament_id)
    AND series.roster_id = sqlc.arg(roster_id)
ORDER BY assignment.id
FOR UPDATE OF assignment;

-- name: LockArenaCorrectionGoldenAttempts :many
SELECT id
FROM arena_golden_attempts
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY id
FOR UPDATE;

-- name: LockArenaCorrectionDescendants :many
WITH RECURSIVE affected_artifact_ids(artifact_id) AS (
    SELECT dependency.artifact_id
    FROM arena_projection_dependencies AS dependency
    WHERE dependency.tournament_id = sqlc.arg(tournament_id)
        AND dependency.roster_id = sqlc.arg(roster_id)
        AND dependency.official_result_revision_id = sqlc.arg(source_revision_id)
    UNION
    SELECT dependency.artifact_id
    FROM arena_projection_dependencies AS dependency
    INNER JOIN affected_artifact_ids AS affected
        ON affected.artifact_id = dependency.depends_on_artifact_id
    WHERE dependency.tournament_id = sqlc.arg(tournament_id)
        AND dependency.roster_id = sqlc.arg(roster_id)
)
SELECT artifact.id AS artifact_id,
    artifact.artifact_kind,
    artifact.produced_by_revision_id,
    membership.revision_id,
    revision.revision_number,
    revision.state AS revision_state
FROM affected_artifact_ids AS affected
INNER JOIN arena_projection_artifacts AS artifact
    ON artifact.id = affected.artifact_id
INNER JOIN arena_projection_revision_artifacts AS membership
    ON membership.artifact_id = artifact.id
INNER JOIN arena_projection_revisions AS revision
    ON revision.id = membership.revision_id
WHERE artifact.tournament_id = sqlc.arg(tournament_id)
    AND artifact.roster_id = sqlc.arg(roster_id)
ORDER BY revision.revision_number, artifact.artifact_kind, artifact.id
FOR UPDATE OF artifact, revision;

-- name: GetArenaCorrectionCutoff :one
WITH source_revision AS (
    SELECT source.created_at
    FROM arena_official_result_revisions AS source
    WHERE source.id = sqlc.arg(source_revision_id)
        AND source.tournament_id = sqlc.arg(tournament_id)
        AND source.roster_id = sqlc.arg(roster_id)
        AND source.series_id = sqlc.arg(series_id)
)
SELECT CASE
    WHEN tournament.state IN ('completed', 'cancelled')
        THEN 'tournament_terminal'
    WHEN EXISTS (
        SELECT 1
        FROM arena_waves AS wave
        CROSS JOIN source_revision AS source
        WHERE wave.tournament_id = tournament.id
            AND wave.roster_id = sqlc.arg(roster_id)
            AND wave.started_at > source.created_at
    ) THEN 'wave_started'
    WHEN EXISTS (
        SELECT 1
        FROM arena_task_delivery_receipts AS receipt
        INNER JOIN arena_assignments AS assignment
            ON assignment.id = receipt.assignment_id
        CROSS JOIN source_revision AS source
        WHERE assignment.series_id = sqlc.arg(series_id)
            AND receipt.delivered_at > source.created_at
    ) THEN 'task_delivered'
    WHEN EXISTS (
        SELECT 1
        FROM arena_result_events AS result_event
        CROSS JOIN source_revision AS source
        WHERE result_event.tournament_id = tournament.id
            AND result_event.roster_id = sqlc.arg(roster_id)
            AND result_event.series_id = sqlc.arg(series_id)
            AND result_event.result_reason IN ('no_show', 'operator_forfeit')
            AND result_event.occurred_at > source.created_at
    ) THEN 'no_show_or_forfeit'
    WHEN EXISTS (
        SELECT 1
        FROM arena_golden_memberships AS membership
        CROSS JOIN source_revision AS source
        WHERE membership.tournament_id = tournament.id
            AND membership.roster_id = sqlc.arg(roster_id)
            AND membership.selection_kind = 'direct'
            AND membership.selected_at > source.created_at
    ) THEN 'golden_direct_allocated'
    ELSE ''::TEXT
END AS cutoff_code
FROM arena_tournaments AS tournament
WHERE tournament.id = sqlc.arg(tournament_id);

-- name: LockArenaCorrectionOpenReadyWindows :many
SELECT wave.id AS wave_id,
    wave.revision AS wave_revision,
    wave.state AS wave_state,
    ready_window.id AS ready_window_id,
    ready_window.state AS ready_window_state
FROM arena_waves AS wave
INNER JOIN arena_ready_windows AS ready_window
    ON ready_window.wave_id = wave.id
    AND ready_window.roster_id = wave.roster_id
WHERE wave.tournament_id = sqlc.arg(tournament_id)
    AND wave.roster_id = sqlc.arg(roster_id)
    AND wave.state IN ('ready_window_open', 'ready')
    AND ready_window.state = 'open'
ORDER BY wave.id
FOR UPDATE OF wave, ready_window;

-- name: AdvanceArenaCorrectionOfficialHeadCAS :one
UPDATE arena_official_result_heads
SET current_revision_id = sqlc.arg(current_revision_id),
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE entity_kind = sqlc.arg(entity_kind)
    AND entity_id = sqlc.arg(entity_id)
    AND roster_id = sqlc.arg(roster_id)
    AND current_revision_id = sqlc.arg(expected_revision_id)
    AND revision = sqlc.arg(expected_revision)
RETURNING entity_kind,
    entity_id,
    series_id,
    roster_id,
    game_attempt_id,
    current_revision_id,
    revision,
    updated_at;

-- name: CorrectArenaGameAttemptCAS :one
UPDATE arena_game_attempts AS attempt
SET state = sqlc.arg(result_state),
    result_reason = sqlc.arg(result_reason),
    winner_id = sqlc.arg(winner_id),
    result_revision_id = sqlc.arg(result_revision_id),
    revision = attempt.revision + 1,
    updated_at = sqlc.arg(corrected_at)
FROM arena_series AS series
WHERE attempt.id = sqlc.arg(attempt_id)
    AND attempt.series_id = sqlc.arg(series_id)
    AND attempt.roster_id = sqlc.arg(roster_id)
    AND series.id = attempt.series_id
    AND series.tournament_id = sqlc.arg(tournament_id)
    AND attempt.revision = sqlc.arg(expected_revision)
    AND attempt.state = sqlc.arg(expected_state)
    AND attempt.result_revision_id = sqlc.arg(expected_result_revision_id)
RETURNING attempt.id,
    attempt.slot_id,
    attempt.series_id,
    attempt.roster_id,
    attempt.attempt_number,
    attempt.state,
    attempt.result_reason,
    attempt.winner_id,
    attempt.result_revision_id,
    attempt.revision,
    attempt.created_at,
    attempt.updated_at,
    attempt.started_at,
    attempt.finished_at;

-- name: CorrectArenaSeriesCAS :one
UPDATE arena_series
SET state = sqlc.arg(next_state),
    first_participant_wins = sqlc.arg(first_participant_wins),
    second_participant_wins = sqlc.arg(second_participant_wins),
    winner_id = sqlc.arg(winner_id),
    current_score_revision_id = sqlc.arg(score_revision_id),
    current_result_revision_id = sqlc.arg(result_revision_id),
    revision = revision + 1,
    updated_at = sqlc.arg(corrected_at)
WHERE id = sqlc.arg(series_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND revision = sqlc.arg(expected_revision)
    AND state = sqlc.arg(expected_state)
    AND current_score_revision_id = sqlc.arg(expected_score_revision_id)
    AND current_result_revision_id IS NOT DISTINCT FROM sqlc.narg(expected_result_revision_id)::UUID
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
