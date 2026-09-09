-- name: LockOperatorResultAuthority :one
SELECT tournament.id AS tournament_id,
    tournament.state AS tournament_state,
    roster.id AS roster_id,
    series.id AS series_id,
    series.revision AS authority_revision,
    projection.id AS projection_revision_id,
    projection.revision_number AS projection_revision
FROM series
JOIN rosters AS roster
    ON roster.id = series.roster_id
    AND roster.tournament_id = series.tournament_id
JOIN tournaments AS tournament
    ON tournament.id = roster.tournament_id
JOIN LATERAL (
    SELECT revision.id, revision.revision_number
    FROM projection_revisions AS revision
    WHERE revision.tournament_id = tournament.id
        AND revision.roster_id = roster.id AND revision.state = 'published'
    FOR UPDATE
) AS projection ON TRUE
WHERE tournament.id = sqlc.arg(tournament_id)
    AND series.id = sqlc.arg(series_id)
FOR UPDATE OF tournament, roster, series;

-- name: GetOperatorResultCommand :one
SELECT command_id,
    tournament_id,
    roster_id,
    series_id,
    actor_id,
    action,
    expected_authority_revision,
    request_digest,
    request_document,
    commit_id,
    result_event_id,
    executed_at,
    created_at
FROM operator_result_commands
WHERE command_id = sqlc.arg(command_id);

-- name: GetOperatorResultTime :one
SELECT clock_timestamp()::TIMESTAMPTZ AS db_now;

-- No-show locks the shared publication/Wave authority before any attempt,
-- matching WaveStart's projection/header -> Series -> Game order. The lateral
-- publication lock also precedes outer row locks in operator workflow prepare.
-- name: LockOperatorNoShowPublication :one
SELECT wave.roster_id
FROM tournaments AS tournament
JOIN rosters AS roster ON roster.tournament_id = tournament.id
JOIN LATERAL (
    SELECT revision.id
    FROM projection_revisions AS revision
    WHERE revision.tournament_id = tournament.id
        AND revision.roster_id = roster.id AND revision.state = 'published'
    FOR UPDATE
) AS projection ON TRUE
JOIN waves AS wave ON wave.tournament_id = tournament.id AND wave.roster_id = roster.id
JOIN ready_windows AS ready_window ON ready_window.wave_id = wave.id AND ready_window.roster_id = roster.id
WHERE tournament.id = sqlc.arg(tournament_id) AND wave.id = sqlc.arg(wave_id)
    AND ready_window.id = sqlc.arg(ready_window_id)
    AND EXISTS (
        SELECT 1 FROM wave_series AS member
        WHERE member.wave_id = wave.id AND member.series_id = sqlc.arg(series_id)
            AND member.tournament_id = tournament.id AND member.roster_id = roster.id
    )
FOR UPDATE OF tournament, roster, wave, ready_window;

-- name: LockOperatorNoShowAttempt :one
SELECT attempt.id
FROM game_attempts AS attempt
INNER JOIN series AS series
    ON series.id = attempt.series_id
    AND series.tournament_id = sqlc.arg(tournament_id)
WHERE attempt.id = sqlc.arg(attempt_id)
    AND attempt.series_id = sqlc.arg(series_id)
FOR UPDATE OF attempt;

-- A pre-start operator forfeit may omit an expected Game. In that case the
-- latest planned or ready attempt is the only legal sequence anchor.
-- name: LockOperatorForfeitAttempt :one
SELECT attempt.id
FROM game_attempts AS attempt
INNER JOIN game_slots AS slot
    ON slot.id = attempt.slot_id
    AND slot.series_id = attempt.series_id
    AND slot.roster_id = attempt.roster_id
INNER JOIN series AS series
    ON series.id = attempt.series_id
    AND series.tournament_id = sqlc.arg(tournament_id)
WHERE series.id = sqlc.arg(series_id)
    AND (
        (
            sqlc.narg(expected_game_id)::UUID IS NOT NULL
            AND attempt.id = sqlc.narg(expected_game_id)::UUID
        )
        OR (
            sqlc.narg(expected_game_id)::UUID IS NULL
            AND attempt.state IN ('planned', 'ready')
        )
    )
ORDER BY slot.slot_number DESC, attempt.attempt_number DESC, attempt.id DESC
LIMIT 1
FOR UPDATE OF attempt;

-- name: LockOperatorNoShowSnapshot :one
SELECT sqlc.embed(series),
    sqlc.embed(score_head),
    sqlc.embed(wave),
    sqlc.embed(ready_window)
FROM series
JOIN series_score_heads AS score_head
    ON score_head.series_id = series.id
    AND score_head.roster_id = series.roster_id
JOIN wave_series
    ON wave_series.series_id = series.id
    AND wave_series.tournament_id = series.tournament_id
    AND wave_series.roster_id = series.roster_id
JOIN waves AS wave
    ON wave.id = wave_series.wave_id
    AND wave.tournament_id = series.tournament_id
    AND wave.roster_id = series.roster_id
JOIN ready_windows AS ready_window
    ON ready_window.wave_id = wave.id
    AND ready_window.roster_id = wave.roster_id
WHERE series.id = sqlc.arg(series_id)
    AND series.tournament_id = sqlc.arg(tournament_id)
    AND series.revision = sqlc.arg(expected_authority_revision)
    AND series.state = sqlc.arg(expected_series_state)
    AND wave.id = sqlc.arg(wave_id)
    AND wave.revision_id = sqlc.arg(expected_wave_revision_id)
    AND wave.state = 'ready_window_open'
    AND ready_window.id = sqlc.arg(ready_window_id)
    AND ready_window.revision_id = sqlc.arg(expected_window_revision_id)
    AND ready_window.state = 'open'
FOR UPDATE OF series, score_head, wave, ready_window;

-- name: LockOperatorNoShowReadiness :many
SELECT readiness.ready_window_id,
    readiness.wave_id,
    readiness.roster_id,
    readiness.participant_id,
    readiness.ready,
    readiness.revision,
    readiness.ready_at,
    readiness.created_at,
    readiness.updated_at
FROM wave_readiness AS readiness
WHERE readiness.wave_id = sqlc.arg(wave_id)
    AND readiness.roster_id = sqlc.arg(roster_id)
    AND readiness.ready_window_id = sqlc.arg(ready_window_id)
ORDER BY readiness.participant_id
FOR UPDATE OF readiness;

-- name: LockOperatorForfeitSnapshot :one
SELECT sqlc.embed(series),
    sqlc.embed(score_head),
    COALESCE(series_revision.revision_number, 0)::BIGINT AS series_result_revision,
    COALESCE(active_pause.paused_from_state, '')::TEXT AS series_resume_state
FROM series
JOIN series_score_heads AS score_head
    ON score_head.series_id = series.id
    AND score_head.roster_id = series.roster_id
LEFT JOIN official_result_revisions AS series_revision
    ON series_revision.id = series.current_result_revision_id
    AND series_revision.entity_kind = 'series'
    AND series_revision.entity_id = series.id
    AND series_revision.roster_id = series.roster_id
LEFT JOIN LATERAL (
    SELECT pause.paused_from_state
    FROM pauses AS pause
    WHERE pause.tournament_id = series.tournament_id
        AND pause.roster_id = series.roster_id
        AND pause.series_id = series.id
        AND pause.state = 'active'
    ORDER BY pause.started_at DESC, pause.id DESC
    LIMIT 1
) AS active_pause ON TRUE
WHERE series.id = sqlc.arg(series_id)
    AND series.tournament_id = sqlc.arg(tournament_id)
    AND series.revision = sqlc.arg(expected_authority_revision)
FOR UPDATE OF series, score_head;

-- name: AssertOperatorProjectionRevision :one
SELECT id
FROM projection_revisions
WHERE id = sqlc.arg(projection_revision_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND revision_number = sqlc.arg(projection_revision)
    AND state = 'published'
FOR UPDATE;

-- name: CreateOperatorNoShowCommit :one
INSERT INTO normal_no_show_commits (
    id,
    tournament_id,
    roster_id,
    wave_id,
    ready_window_id,
    ready_window_revision_id,
    series_id,
    result_event_id,
    series_score_revision_id,
    series_result_revision_id,
    audit_event_id,
    outbox_event_id,
    projection_evidence_id,
    command_id,
    expected_authority_revision,
    expected_wave_revision,
    action,
    resolved_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(wave_id),
    sqlc.arg(ready_window_id),
    sqlc.arg(ready_window_revision_id),
    sqlc.arg(series_id),
    sqlc.arg(result_event_id),
    sqlc.arg(series_score_revision_id),
    sqlc.arg(series_result_revision_id),
    sqlc.arg(audit_event_id),
    sqlc.arg(outbox_event_id),
    sqlc.arg(projection_evidence_id),
    sqlc.arg(command_id),
    sqlc.arg(expected_authority_revision),
    sqlc.arg(expected_wave_revision),
    sqlc.arg(action),
    sqlc.arg(resolved_at),
    sqlc.arg(resolved_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    wave_id,
    ready_window_id,
    ready_window_revision_id,
    series_id,
    result_event_id,
    series_score_revision_id,
    series_result_revision_id,
    audit_event_id,
    outbox_event_id,
    projection_evidence_id,
    command_id,
    expected_authority_revision,
    expected_wave_revision,
    action,
    resolved_at,
    created_at;

-- name: CreateOperatorNoShowCommitGame :exec
INSERT INTO normal_no_show_commit_games (
    commit_id,
    game_attempt_id,
    game_result_revision_id,
    position
)
VALUES (
    sqlc.arg(commit_id),
    sqlc.arg(game_attempt_id),
    sqlc.arg(game_result_revision_id),
    sqlc.arg(position)
);

-- name: CreateOperatorForfeitCommit :one
INSERT INTO operator_forfeit_commits (
    id,
    tournament_id,
    roster_id,
    series_id,
    anchor_attempt_id,
    result_event_id,
    series_score_revision_id,
    series_result_revision_id,
    audit_event_id,
    outbox_event_id,
    projection_evidence_id,
    command_id,
    actor_id,
    forfeiting_participant_id,
    expected_authority_revision,
    source_projection_revision_id,
    source_projection_revision,
    rule_id,
    reason,
    evidence_ids,
    resolved_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(anchor_attempt_id),
    sqlc.arg(result_event_id),
    sqlc.arg(series_score_revision_id),
    sqlc.arg(series_result_revision_id),
    sqlc.arg(audit_event_id),
    sqlc.arg(outbox_event_id),
    sqlc.arg(projection_evidence_id),
    sqlc.arg(command_id),
    sqlc.arg(actor_id),
    sqlc.arg(forfeiting_participant_id),
    sqlc.arg(expected_authority_revision),
    sqlc.arg(source_projection_revision_id),
    sqlc.arg(source_projection_revision),
    sqlc.arg(rule_id),
    sqlc.arg(reason),
    sqlc.arg(evidence_ids),
    sqlc.arg(resolved_at),
    sqlc.arg(resolved_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    series_id,
    anchor_attempt_id,
    result_event_id,
    series_score_revision_id,
    series_result_revision_id,
    audit_event_id,
    outbox_event_id,
    projection_evidence_id,
    command_id,
    actor_id,
    forfeiting_participant_id,
    expected_authority_revision,
    source_projection_revision_id,
    source_projection_revision,
    rule_id,
    reason,
    evidence_ids,
    resolved_at,
    created_at;

-- name: CreateOperatorResultCommand :one
INSERT INTO operator_result_commands (
    command_id,
    tournament_id,
    roster_id,
    series_id,
    actor_id,
    action,
    expected_authority_revision,
    request_digest,
    request_document,
    commit_id,
    result_event_id,
    executed_at,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(actor_id),
    sqlc.arg(action),
    sqlc.arg(expected_authority_revision),
    sqlc.arg(request_digest),
    sqlc.arg(request_document),
    sqlc.arg(commit_id),
    sqlc.arg(result_event_id),
    sqlc.arg(executed_at),
    sqlc.arg(executed_at)
)
RETURNING command_id,
    tournament_id,
    roster_id,
    series_id,
    actor_id,
    action,
    expected_authority_revision,
    request_digest,
    request_document,
    commit_id,
    result_event_id,
    executed_at,
    created_at;
