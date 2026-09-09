-- name: GetCorrectionSource :one
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
FROM official_result_revisions AS revision
INNER JOIN official_result_heads AS head
    ON head.entity_kind = revision.entity_kind
    AND head.entity_id = revision.entity_id
WHERE revision.id = sqlc.arg(source_revision_id)
    AND revision.tournament_id = sqlc.arg(tournament_id)
    AND revision.roster_id = sqlc.arg(roster_id)
    AND revision.entity_kind = 'game_attempt'
    AND revision.entity_id = sqlc.arg(attempt_id)
    AND revision.series_id = sqlc.arg(series_id);

-- name: ListCorrectionDescendants :many
WITH RECURSIVE affected_artifact_ids(artifact_id) AS (
    SELECT dependency.artifact_id
    FROM projection_dependencies AS dependency
    WHERE dependency.tournament_id = sqlc.arg(tournament_id)
        AND dependency.roster_id = sqlc.arg(roster_id)
        AND dependency.official_result_revision_id = sqlc.arg(source_revision_id)
    UNION
    SELECT dependency.artifact_id
    FROM projection_dependencies AS dependency
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
INNER JOIN projection_artifacts AS artifact
    ON artifact.id = affected.artifact_id
INNER JOIN projection_revision_artifacts AS membership
    ON membership.artifact_id = artifact.id
INNER JOIN projection_revisions AS revision
    ON revision.id = membership.revision_id
WHERE artifact.tournament_id = sqlc.arg(tournament_id)
    AND artifact.roster_id = sqlc.arg(roster_id)
ORDER BY revision.revision_number, artifact.artifact_kind, artifact.id;

-- name: LockCorrectionTournamentScope :one
SELECT tournament.id AS tournament_id,
    tournament.state AS tournament_state,
    tournament.revision AS tournament_revision,
    tournament.preset AS tournament_preset,
    tournament.created_at AS tournament_created_at,
    tournament.updated_at AS tournament_updated_at,
    tournament.started_at AS tournament_started_at,
    tournament.finished_at AS tournament_finished_at,
    roster.id AS roster_id,
    (
        SELECT COUNT(*)
        FROM participants AS participant
        WHERE participant.roster_id = roster.id
    )::INTEGER AS roster_size
FROM tournaments AS tournament
INNER JOIN rosters AS roster
    ON roster.tournament_id = tournament.id
WHERE tournament.id = sqlc.arg(tournament_id)
    AND roster.id = sqlc.arg(roster_id)
FOR UPDATE OF tournament, roster;

-- name: LockCorrectionSeriesAttempts :many
SELECT attempt.id
FROM game_attempts AS attempt
INNER JOIN series AS series
    ON series.id = attempt.series_id
    AND series.roster_id = attempt.roster_id
WHERE series.id = sqlc.arg(series_id)
    AND series.tournament_id = sqlc.arg(tournament_id)
    AND series.roster_id = sqlc.arg(roster_id)
ORDER BY attempt.id
FOR UPDATE OF attempt;

-- name: LockCorrectionOfficialHeads :many
SELECT entity_kind,
    entity_id,
    series_id,
    roster_id,
    game_attempt_id,
    current_revision_id,
    revision,
    updated_at
FROM official_result_heads
WHERE roster_id = sqlc.arg(roster_id)
    AND (
        (entity_kind = 'game_attempt' AND entity_id = sqlc.arg(attempt_id))
        OR (entity_kind = 'series' AND entity_id = sqlc.arg(series_id))
    )
ORDER BY entity_kind
FOR UPDATE;

-- name: ListTournamentAdminCorrectionGameResults :many
SELECT game_slot.id AS slot_id,
    game_slot.slot_number,
    game_attempt.id AS game_attempt_id,
    game_attempt.attempt_number,
    game_attempt.state AS game_state,
    game_attempt.result_reason AS game_reason,
    game_attempt.winner_id AS game_winner_id,
    revision.id AS result_revision_id,
    revision.previous_revision_id,
    revision.revision_number,
    revision.result_state,
    revision.result_reason,
    revision.winner_id AS revision_winner_id,
    revision.result_event_id,
    revision.created_at AS revision_created_at,
    revision.command_id AS result_command_id,
    revision.actor_kind AS result_actor_kind,
    revision.actor_id AS result_actor_id,
    revision.source_projection_revision_id AS result_source_projection_revision_id,
    revision.source_projection_revision AS result_source_projection_revision,
	result_event.server_sequence AS result_server_sequence,
	result_event.idempotency_key AS result_command_idempotency_key,
	result_event.submission_event_id,
    result_event.occurred_at
FROM game_slots AS game_slot
INNER JOIN game_attempts AS game_attempt
    ON game_attempt.slot_id = game_slot.id
    AND game_attempt.series_id = game_slot.series_id
    AND game_attempt.roster_id = game_slot.roster_id
INNER JOIN official_result_heads AS head
    ON head.entity_kind = 'game_attempt'
    AND head.entity_id = game_attempt.id
    AND head.series_id = game_attempt.series_id
    AND head.roster_id = game_attempt.roster_id
    AND head.current_revision_id = game_attempt.result_revision_id
INNER JOIN official_result_revisions AS revision
    ON revision.id = head.current_revision_id
    AND revision.entity_kind = head.entity_kind
    AND revision.entity_id = head.entity_id
    AND revision.series_id = head.series_id
    AND revision.roster_id = head.roster_id
INNER JOIN result_events AS result_event
    ON result_event.id = revision.result_event_id
    AND result_event.tournament_id = revision.tournament_id
    AND result_event.roster_id = revision.roster_id
    AND result_event.series_id = revision.series_id
    AND result_event.attempt_id = game_attempt.id
WHERE game_slot.series_id = sqlc.arg(series_id)
    AND game_slot.roster_id = sqlc.arg(roster_id)
    AND revision.tournament_id = sqlc.arg(tournament_id)
ORDER BY game_slot.slot_number, game_attempt.attempt_number
FOR UPDATE OF head;

-- name: GetTournamentAdminCorrectionSolve :one
SELECT result_event.submission_event_id,
    result_event.occurred_at AS solved_at,
    submission.payload_digest AS evidence_digest
FROM result_events AS result_event
LEFT JOIN submission_events AS submission
    ON submission.id = result_event.submission_event_id
    AND submission.tournament_id = result_event.tournament_id
    AND submission.roster_id = result_event.roster_id
    AND submission.series_id = result_event.series_id
    AND submission.attempt_id = result_event.attempt_id
WHERE result_event.id = sqlc.arg(result_event_id)
    AND result_event.tournament_id = sqlc.arg(tournament_id)
    AND result_event.roster_id = sqlc.arg(roster_id)
    AND result_event.series_id = sqlc.arg(series_id)
    AND result_event.attempt_id = sqlc.arg(game_attempt_id);

-- name: LockCorrectionCutoffWaves :many
SELECT id,
    state,
    started_at
FROM waves
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY id
FOR UPDATE;

-- name: LockCorrectionAssignments :many
SELECT assignment.id
FROM assignments AS assignment
INNER JOIN series AS series ON series.id = assignment.series_id
WHERE series.tournament_id = sqlc.arg(tournament_id)
    AND series.roster_id = sqlc.arg(roster_id)
ORDER BY assignment.id
FOR UPDATE OF assignment;

-- name: LockCorrectionGoldenAttempts :many
SELECT id
FROM golden_attempts
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY id
FOR UPDATE;

-- name: LockCorrectionDescendants :many
WITH RECURSIVE affected_artifact_ids(artifact_id) AS (
    SELECT dependency.artifact_id
    FROM projection_dependencies AS dependency
    WHERE dependency.tournament_id = sqlc.arg(tournament_id)
        AND dependency.roster_id = sqlc.arg(roster_id)
        AND dependency.official_result_revision_id = sqlc.arg(source_revision_id)
    UNION
    SELECT dependency.artifact_id
    FROM projection_dependencies AS dependency
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
INNER JOIN projection_artifacts AS artifact
    ON artifact.id = affected.artifact_id
INNER JOIN projection_revision_artifacts AS membership
    ON membership.artifact_id = artifact.id
INNER JOIN projection_revisions AS revision
    ON revision.id = membership.revision_id
WHERE artifact.tournament_id = sqlc.arg(tournament_id)
    AND artifact.roster_id = sqlc.arg(roster_id)
ORDER BY revision.revision_number, artifact.artifact_kind, artifact.id
FOR UPDATE OF artifact, revision;

-- name: GetCorrectionCutoff :one
WITH source_revision AS (
    SELECT source.created_at
    FROM official_result_revisions AS source
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
        FROM waves AS wave
        CROSS JOIN source_revision AS source
        WHERE wave.tournament_id = tournament.id
            AND wave.roster_id = sqlc.arg(roster_id)
            AND wave.started_at > source.created_at
    ) THEN 'wave_started'
    WHEN EXISTS (
        SELECT 1
        FROM task_delivery_receipts AS receipt
        INNER JOIN assignments AS assignment
            ON assignment.id = receipt.assignment_id
        CROSS JOIN source_revision AS source
        WHERE assignment.series_id = sqlc.arg(series_id)
            AND receipt.delivered_at > source.created_at
    ) THEN 'task_delivered'
    WHEN EXISTS (
        SELECT 1
        FROM result_events AS result_event
        CROSS JOIN source_revision AS source
        WHERE result_event.tournament_id = tournament.id
            AND result_event.roster_id = sqlc.arg(roster_id)
            AND result_event.series_id = sqlc.arg(series_id)
            AND result_event.result_reason IN ('no_show', 'operator_forfeit')
            AND result_event.occurred_at > source.created_at
    ) THEN 'no_show_or_forfeit'
    WHEN EXISTS (
        SELECT 1
        FROM golden_memberships AS membership
        CROSS JOIN source_revision AS source
        WHERE membership.tournament_id = tournament.id
            AND membership.roster_id = sqlc.arg(roster_id)
            AND membership.selection_kind = 'direct'
            AND membership.selected_at > source.created_at
    ) THEN 'golden_direct_allocated'
    ELSE ''::TEXT
END AS cutoff_code
FROM tournaments AS tournament
WHERE tournament.id = sqlc.arg(tournament_id);

-- name: LockCorrectionOpenReadyWindows :many
SELECT wave.id AS wave_id,
    wave.revision AS wave_revision,
    wave.state AS wave_state,
    ready_window.id AS ready_window_id,
    ready_window.state AS ready_window_state
FROM waves AS wave
INNER JOIN ready_windows AS ready_window
    ON ready_window.wave_id = wave.id
    AND ready_window.roster_id = wave.roster_id
WHERE wave.tournament_id = sqlc.arg(tournament_id)
    AND wave.roster_id = sqlc.arg(roster_id)
    AND wave.state IN ('ready_window_open', 'ready')
    AND ready_window.state = 'open'
ORDER BY wave.id
FOR UPDATE OF wave, ready_window;

-- name: AdvanceCorrectionOfficialHeadCAS :one
UPDATE official_result_heads
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

-- name: CorrectGameAttemptCAS :one
UPDATE game_attempts AS attempt
SET state = sqlc.arg(result_state),
    result_reason = sqlc.arg(result_reason),
    winner_id = sqlc.arg(winner_id),
    result_revision_id = sqlc.arg(result_revision_id),
    revision = attempt.revision + 1,
    updated_at = sqlc.arg(corrected_at)
FROM series AS series
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

-- name: CorrectSeriesCAS :one
UPDATE series
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

-- name: ListTournamentAdminCorrectionReservations :many
SELECT reservation.id,
    plan.tournament_id,
    plan.id AS owner_id,
    reservation.revision,
    reservation.state,
    reservation.disclosed_at,
    edge.selection_evidence
FROM task_version_reservations AS reservation
INNER JOIN assignment_plan_edges AS edge
    ON edge.id = reservation.edge_id
    AND edge.plan_id = reservation.plan_id
    AND edge.branch_id = reservation.branch_id
    AND edge.task_id = reservation.task_id
    AND edge.task_version = reservation.task_version
INNER JOIN assignment_plans AS plan
    ON plan.id = reservation.plan_id
WHERE plan.tournament_id = sqlc.arg(tournament_id)
    AND plan.roster_id = sqlc.arg(roster_id)
    AND reservation.state = 'reserved'
    AND reservation.disclosed_at IS NULL
ORDER BY reservation.id
FOR UPDATE OF reservation;

-- name: ReleaseTournamentAdminCorrectionReservationCAS :one
UPDATE task_version_reservations AS reservation
SET state = 'released',
    revision = reservation.revision + 1,
    released_at = sqlc.arg(released_at),
    release_reason = sqlc.arg(release_reason)
FROM assignment_plans AS plan
WHERE reservation.id = sqlc.arg(reservation_id)
    AND reservation.plan_id = plan.id
    AND plan.tournament_id = sqlc.arg(tournament_id)
    AND plan.roster_id = sqlc.arg(roster_id)
    AND reservation.plan_id = sqlc.arg(owner_id)
    AND reservation.revision = sqlc.arg(expected_revision)
    AND reservation.state = 'reserved'
    AND reservation.disclosed_at IS NULL
RETURNING reservation.id,
    reservation.edge_id,
    reservation.plan_id,
    reservation.branch_id,
    reservation.task_id,
    reservation.task_version,
    reservation.revision,
    reservation.state,
    reservation.disclosed_at,
    reservation.committed_at,
    reservation.released_at,
    reservation.release_reason,
    reservation.superseded_at,
    reservation.supersession_reason,
    reservation.created_at;

-- name: GetTournamentAdminCorrectionCommand :one
SELECT command_id,
    tournament_id,
    roster_id,
    series_id,
    game_attempt_id,
    actor_id,
    source_projection_revision_id,
    source_projection_revision,
    resulting_projection_revision_id,
    resulting_projection_revision,
    request_digest,
    plan_digest,
    validation_digest,
    plan_document,
    evidence_document,
    result_commit_id,
    executed_at,
    created_at
FROM result_correction_commits
WHERE command_id = sqlc.arg(command_id);

-- name: GetTournamentAdminCorrectionConsistency :one
SELECT result_commit.tournament_id = correction.tournament_id AS tournament_matches,
    result_commit.roster_id = correction.roster_id AS roster_matches,
    result_commit.series_id = correction.series_id AS series_matches,
    result_commit.attempt_id = correction.game_attempt_id AS game_matches,
    result_commit.idempotency_key = correction.command_id AS command_matches,
    audit_event.actor_kind = 'operator' AS actor_kind_matches,
    audit_event.actor_id = correction.actor_id AS actor_matches,
    correction.plan_document ->> 'schema' = 'result-correction-plan-v1' AS plan_schema_matches,
    correction.evidence_document ->> 'schema' = 'result-correction-evidence-v1' AS evidence_schema_matches,
    correction.evidence_document ->> 'command_id' = correction.command_id::TEXT AS evidence_command_matches,
    correction.evidence_document ->> 'tournament_id' = correction.tournament_id::TEXT AS evidence_tournament_matches,
    correction.evidence_document ->> 'series_id' = correction.series_id::TEXT AS evidence_series_matches,
    correction.evidence_document ->> 'game_id' = correction.game_attempt_id::TEXT AS evidence_game_matches,
    correction.evidence_document ->> 'operator_id' = correction.actor_id::TEXT AS evidence_actor_matches,
    correction.evidence_document ->> 'validation_digest' = encode(correction.validation_digest, 'hex') AS validation_matches
FROM result_correction_commits AS correction
INNER JOIN result_commits AS result_commit
    ON result_commit.id = correction.result_commit_id
INNER JOIN audit_events AS audit_event
    ON audit_event.id = result_commit.audit_event_id
WHERE correction.command_id = sqlc.arg(command_id);

-- name: ListTournamentAdminCorrectionPlans :many
SELECT command_id,
    plan_document,
    evidence_document,
    executed_at
FROM result_correction_commits
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY executed_at, command_id;

-- name: CreateTournamentAdminCorrectionCommand :one
INSERT INTO result_correction_commits (
    command_id,
    tournament_id,
    roster_id,
    series_id,
    game_attempt_id,
    actor_id,
    source_projection_revision_id,
    source_projection_revision,
    resulting_projection_revision_id,
    resulting_projection_revision,
    request_digest,
    plan_digest,
    validation_digest,
    plan_document,
    evidence_document,
    result_commit_id,
    executed_at,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(game_attempt_id),
    sqlc.arg(actor_id),
    sqlc.arg(source_projection_revision_id),
    sqlc.arg(source_projection_revision),
    sqlc.arg(resulting_projection_revision_id),
    sqlc.arg(resulting_projection_revision),
    sqlc.arg(request_digest),
    sqlc.arg(plan_digest),
    sqlc.arg(validation_digest),
    sqlc.arg(plan_document),
    sqlc.arg(evidence_document),
    sqlc.arg(result_commit_id),
    sqlc.arg(executed_at),
    sqlc.arg(executed_at)
)
RETURNING command_id,
    tournament_id,
    roster_id,
    series_id,
    game_attempt_id,
    actor_id,
    source_projection_revision_id,
    source_projection_revision,
    resulting_projection_revision_id,
    resulting_projection_revision,
    request_digest,
    plan_digest,
    validation_digest,
    plan_document,
    evidence_document,
    result_commit_id,
    executed_at,
    created_at;

-- name: GetTournamentAdminCorrectionTime :one
SELECT clock_timestamp()::TIMESTAMPTZ AS db_now;

-- name: ListTournamentAdminCorrectionProjectionParticipants :many
SELECT participant.id,
    participant.seed
FROM participants AS participant
WHERE participant.roster_id = sqlc.arg(roster_id)
ORDER BY participant.seed, participant.id
FOR KEY SHARE;

-- name: CreateSwissPointLedgerEntry :one
INSERT INTO swiss_point_ledger_entries (
    id,
    tournament_id,
    roster_id,
    round_id,
    round_number,
    source_kind,
    source_series_id,
    series_result_revision_id,
    bye_revision_id,
    result_label,
    participant_id,
    opponent_id,
    points,
    effective_time_ns,
    accepted_solve_time_ns,
    stable_seed,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(round_id),
    sqlc.arg(round_number),
    sqlc.arg(source_kind),
    sqlc.narg(source_series_id)::UUID,
    sqlc.narg(series_result_revision_id)::UUID,
    sqlc.narg(bye_revision_id)::UUID,
    sqlc.narg(result_label)::TEXT,
    sqlc.arg(participant_id),
    sqlc.narg(opponent_id)::UUID,
    sqlc.arg(points),
    sqlc.arg(effective_time_ns),
    sqlc.narg(accepted_solve_time_ns)::BIGINT,
    sqlc.arg(stable_seed),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    round_id,
    round_number,
    source_kind,
    source_series_id,
    series_result_revision_id,
    bye_revision_id,
    result_label,
    participant_id,
    opponent_id,
    points,
    effective_time_ns,
    accepted_solve_time_ns,
    stable_seed,
    created_at;

-- name: ListTournamentAdminCorrectionSwissPointLedger :many
SELECT entry.id,
    entry.tournament_id,
    entry.roster_id,
    entry.round_id,
    wave.revision_id AS round_revision_id,
    entry.round_number,
    entry.source_kind,
    entry.source_series_id,
    entry.series_result_revision_id,
    entry.bye_revision_id,
    entry.result_label,
    entry.participant_id,
    entry.opponent_id,
    entry.points,
    entry.effective_time_ns,
    entry.accepted_solve_time_ns,
    entry.stable_seed,
    entry.created_at
FROM swiss_point_ledger_entries AS entry
JOIN swiss_rounds AS round
    ON round.id = entry.round_id
    AND round.roster_id = entry.roster_id
JOIN swiss_wave_links AS link
    ON link.round_id = round.id
    AND link.tournament_id = entry.tournament_id
    AND link.roster_id = entry.roster_id
JOIN waves AS wave
    ON wave.id = link.wave_id
    AND wave.tournament_id = entry.tournament_id
    AND wave.roster_id = entry.roster_id
WHERE entry.tournament_id = sqlc.arg(tournament_id)
    AND entry.roster_id = sqlc.arg(roster_id)
    AND wave.state = 'completed'
    AND (
        (entry.source_kind = 'series' AND EXISTS (
            SELECT 1
            FROM series
            WHERE series.id = entry.source_series_id
                AND series.tournament_id = entry.tournament_id
                AND series.roster_id = entry.roster_id
                AND series.state IN ('completed', 'cancelled')
                AND series.current_result_revision_id = entry.series_result_revision_id
        ))
        OR (entry.source_kind = 'bye' AND EXISTS (
            SELECT 1
            FROM swiss_wave_links AS bye_link
            WHERE bye_link.round_id = entry.round_id
                AND bye_link.tournament_id = entry.tournament_id
                AND bye_link.roster_id = entry.roster_id
                AND bye_link.bye_participant_id = entry.participant_id
                AND bye_link.bye_revision_id = entry.bye_revision_id
        ))
    )
ORDER BY entry.round_number,
    entry.round_id,
    entry.source_kind,
    entry.source_series_id,
    entry.bye_revision_id,
    entry.participant_id
FOR UPDATE OF entry, round, wave;

-- name: LockTournamentAdminCorrectionSwissPointSourceSeries :many
SELECT series.id
FROM series
WHERE series.tournament_id = sqlc.arg(tournament_id)
    AND series.roster_id = sqlc.arg(roster_id)
    AND series.state IN ('completed', 'cancelled')
    AND EXISTS (
        SELECT 1
        FROM swiss_point_ledger_entries AS entry
        JOIN swiss_wave_links AS link
            ON link.round_id = entry.round_id
            AND link.tournament_id = entry.tournament_id
            AND link.roster_id = entry.roster_id
        JOIN waves AS wave
            ON wave.id = link.wave_id
            AND wave.tournament_id = entry.tournament_id
            AND wave.roster_id = entry.roster_id
        WHERE entry.tournament_id = series.tournament_id
            AND entry.roster_id = series.roster_id
            AND entry.source_kind = 'series'
            AND entry.source_series_id = series.id
            AND wave.state = 'completed'
            AND series.current_result_revision_id = entry.series_result_revision_id
    )
ORDER BY series.id
FOR UPDATE OF series;

-- name: ListTournamentAdminCorrectionProjectionSeries :many
SELECT series.id,
    series.first_participant_id,
    series.second_participant_id,
    series.state,
    series.first_participant_wins,
    series.second_participant_wins,
    series.winner_id,
    series.current_result_revision_id
FROM series
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY series.id
FOR KEY SHARE;

-- name: LockTournamentAdminCorrectionTopFour :many
SELECT member.participant_id,
    member.position,
    position_commit.id AS golden_position_commit_id
FROM projection_revision_artifacts AS revision_artifact
INNER JOIN projection_artifacts AS artifact
    ON artifact.id = revision_artifact.artifact_id
    AND artifact.tournament_id = revision_artifact.tournament_id
    AND artifact.roster_id = revision_artifact.roster_id
    AND artifact.artifact_kind = revision_artifact.artifact_kind
INNER JOIN projection_artifact_members AS member
    ON member.artifact_id = artifact.id
    AND member.tournament_id = artifact.tournament_id
    AND member.roster_id = artifact.roster_id
    AND member.artifact_kind = artifact.artifact_kind
LEFT JOIN golden_position_commits AS position_commit
    ON position_commit.tournament_id = member.tournament_id
    AND position_commit.roster_id = member.roster_id
    AND position_commit.participant_id = member.participant_id
    AND position_commit.position = member.position
    AND EXISTS (
        SELECT 1
        FROM projection_dependencies AS dependency
        WHERE dependency.artifact_id = artifact.id
            AND dependency.tournament_id = artifact.tournament_id
            AND dependency.roster_id = artifact.roster_id
            AND dependency.dependency_kind = 'golden_position'
            AND dependency.golden_position_commit_id = position_commit.id
    )
WHERE revision_artifact.revision_id = sqlc.arg(projection_revision_id)
    AND revision_artifact.tournament_id = sqlc.arg(tournament_id)
    AND revision_artifact.roster_id = sqlc.arg(roster_id)
    AND revision_artifact.artifact_kind = 'top4'
ORDER BY member.position, member.participant_id
FOR UPDATE OF revision_artifact, artifact, member;

-- name: LockTournamentAdminCorrectionPlayoffBracket :many
WITH latest_playoff AS (
    SELECT progression.command_id,
        progression.tournament_id,
        progression.roster_id
    FROM tournament_stage_progressions AS progression
    WHERE progression.tournament_id = sqlc.arg(tournament_id)
        AND progression.roster_id = sqlc.arg(roster_id)
        AND progression.resulting_tournament_revision <= sqlc.arg(tournament_revision)
        AND progression.resulting_tournament_state = 'playoffs'
    ORDER BY progression.resulting_tournament_revision DESC, progression.command_id
    LIMIT 1
    FOR UPDATE
)
SELECT semifinal.position,
    series.id AS series_id,
    series.first_participant_id,
    series.second_participant_id,
    series.state,
    series.first_participant_wins,
    series.second_participant_wins
FROM latest_playoff AS progression
INNER JOIN tournament_stage_playoff_semifinals AS semifinal
    ON semifinal.command_id = progression.command_id
    AND semifinal.tournament_id = progression.tournament_id
    AND semifinal.roster_id = progression.roster_id
INNER JOIN series
    ON series.id = semifinal.series_id
    AND series.tournament_id = semifinal.tournament_id
    AND series.roster_id = semifinal.roster_id
ORDER BY semifinal.position, semifinal.series_id
FOR UPDATE OF semifinal, series;

-- name: CreateResultProjectionNodeAuthority :exec
INSERT INTO result_projection_node_authorities (
    id,
    tournament_id,
    roster_id,
    source_kind,
    result_commit_id,
    correction_command_id,
    wave_id,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(source_kind),
    sqlc.narg(result_commit_id)::uuid,
    sqlc.narg(correction_command_id)::uuid,
    sqlc.narg(wave_id)::uuid,
    sqlc.arg(created_at)
)
ON CONFLICT (id) DO NOTHING;

-- name: CreateResultProjectionNode :exec
INSERT INTO result_projection_nodes (
    id,
    authority_id,
    tournament_id,
    roster_id,
    artifact_kind,
    entity_id,
    revision_number,
    previous_node_id,
    payload,
    payload_digest,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(authority_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(artifact_kind),
    sqlc.arg(entity_id),
    sqlc.arg(revision_number),
    sqlc.narg(previous_node_id)::uuid,
    sqlc.arg(payload),
    sqlc.arg(payload_digest),
    sqlc.arg(created_at)
)
ON CONFLICT (id) DO NOTHING;

-- name: GetResultProjectionNode :one
SELECT id,
    authority_id,
    tournament_id,
    roster_id,
    artifact_kind,
    entity_id,
    revision_number,
    previous_node_id,
    payload,
    payload_digest,
    created_at
FROM result_projection_nodes
WHERE id = sqlc.arg(id);

-- name: GetCorrectionProjectionNodeByRevision :one
SELECT id,
    authority_id,
    tournament_id,
    roster_id,
    artifact_kind,
    entity_id,
    revision_number,
    previous_node_id,
    payload,
    payload_digest,
    created_at
FROM result_projection_nodes
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND artifact_kind = sqlc.arg(artifact_kind)
    AND entity_id = sqlc.arg(entity_id)
    AND revision_number = sqlc.arg(revision_number);

-- name: LockCorrectionLatestProjectionNode :one
SELECT id,
    authority_id,
    tournament_id,
    roster_id,
    artifact_kind,
    entity_id,
    revision_number,
    previous_node_id,
    payload,
    payload_digest,
    created_at
FROM result_projection_nodes
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND artifact_kind = sqlc.arg(artifact_kind)
    AND entity_id = sqlc.arg(entity_id)
ORDER BY revision_number DESC
LIMIT 1
FOR UPDATE;

-- name: GetCorrectionStageReceiptBridge :one
SELECT progression.command_id,
    progression.source_projection_revision_id,
    progression.source_projection_revision,
    progression.resulting_projection_revision_id,
    progression.resulting_projection_revision
FROM tournament_stage_progressions AS progression
INNER JOIN final_swiss_projection_receipts AS receipt
    ON receipt.projection_revision_id = progression.source_projection_revision_id
    AND receipt.tournament_id = progression.tournament_id
    AND receipt.roster_id = progression.roster_id
WHERE progression.tournament_id = sqlc.arg(tournament_id)
    AND progression.roster_id = sqlc.arg(roster_id)
    AND progression.action = 'start_playoffs'
    AND progression.source_projection_revision_id = sqlc.arg(receipt_projection_revision_id)
    AND progression.source_projection_revision = sqlc.arg(receipt_projection_revision)
    AND progression.resulting_projection_revision_id = sqlc.arg(current_projection_revision_id)
    AND progression.resulting_projection_revision = sqlc.arg(current_projection_revision)
FOR KEY SHARE OF progression, receipt;

-- name: GetCorrectionResultProjectionNodeForSource :one
WITH candidates AS (
    SELECT binding.node_id,
        1::smallint AS priority
    FROM correction_projection_bindings AS binding
    WHERE binding.tournament_id = sqlc.arg(tournament_id)
        AND binding.roster_id = sqlc.arg(roster_id)
        AND binding.artifact_kind = sqlc.arg(artifact_kind)
        AND binding.entity_id = sqlc.arg(entity_id)
        AND binding.source_id = sqlc.arg(source_id)

    UNION

    SELECT node.id,
        3::smallint
    FROM result_projection_nodes AS node
    WHERE node.id = sqlc.arg(source_id)

    UNION

    SELECT receipt_game.game_result_node_id,
        2::smallint
    FROM final_swiss_projection_receipt_games AS receipt_game
    WHERE sqlc.arg(artifact_kind) = 'game_result'
        AND receipt_game.tournament_id = sqlc.arg(tournament_id)
        AND receipt_game.roster_id = sqlc.arg(roster_id)
        AND receipt_game.game_attempt_id = sqlc.arg(entity_id)
        AND receipt_game.game_result_revision_id = sqlc.arg(source_id)

    UNION

    SELECT CASE sqlc.arg(artifact_kind)
        WHEN 'series_score' THEN receipt_series.score_node_id
        WHEN 'series_result' THEN receipt_series.series_result_node_id
    END,
        2::smallint
    FROM final_swiss_projection_receipt_series AS receipt_series
    WHERE sqlc.arg(artifact_kind) IN ('series_score', 'series_result')
        AND receipt_series.tournament_id = sqlc.arg(tournament_id)
        AND receipt_series.roster_id = sqlc.arg(roster_id)
        AND receipt_series.series_id = sqlc.arg(entity_id)
        AND CASE sqlc.arg(artifact_kind)
            WHEN 'series_score' THEN receipt_series.score_revision_id
            WHEN 'series_result' THEN receipt_series.series_result_revision_id
        END = sqlc.arg(source_id)
), exact_candidate AS (
    SELECT selected.node_id
    FROM candidates AS selected
    WHERE selected.node_id IS NOT NULL
        AND selected.priority = (
            SELECT MIN(minimum.priority)
            FROM candidates AS minimum
            WHERE minimum.node_id IS NOT NULL
        )
        AND (
            SELECT COUNT(DISTINCT preferred.node_id)
            FROM candidates AS preferred
            WHERE preferred.node_id IS NOT NULL
                AND preferred.priority = (
                    SELECT MIN(nested_minimum.priority)
                    FROM candidates AS nested_minimum
                    WHERE nested_minimum.node_id IS NOT NULL
                )
        ) = 1
)
SELECT node.id,
    node.authority_id,
    node.tournament_id,
    node.roster_id,
    node.artifact_kind,
    node.entity_id,
    node.revision_number,
    node.previous_node_id,
    node.payload,
    node.payload_digest,
    node.created_at
FROM result_projection_nodes AS node
INNER JOIN exact_candidate AS candidate
    ON candidate.node_id = node.id
WHERE node.tournament_id = sqlc.arg(tournament_id)
    AND node.roster_id = sqlc.arg(roster_id)
    AND node.artifact_kind = sqlc.arg(artifact_kind)
    AND node.entity_id = sqlc.arg(entity_id);

-- name: ListResultProjectionNodes :many
SELECT id,
    authority_id,
    tournament_id,
    roster_id,
    artifact_kind,
    entity_id,
    revision_number,
    previous_node_id,
    payload,
    payload_digest,
    created_at
FROM result_projection_nodes
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY artifact_kind, entity_id, revision_number;

-- name: CreateResultProjectionDependency :exec
INSERT INTO result_projection_dependencies (
    authority_id,
    source_node_id,
    derived_node_id,
    created_at
)
VALUES (
    sqlc.arg(authority_id),
    sqlc.arg(source_node_id),
    sqlc.arg(derived_node_id),
    sqlc.arg(created_at)
)
ON CONFLICT (source_node_id, derived_node_id) DO NOTHING;

-- name: ListResultProjectionDependencies :many
SELECT dependency.authority_id,
    dependency.source_node_id,
    dependency.derived_node_id,
    dependency.created_at
FROM result_projection_dependencies AS dependency
INNER JOIN result_projection_nodes AS source
    ON source.id = dependency.source_node_id
INNER JOIN result_projection_nodes AS derived
    ON derived.id = dependency.derived_node_id
WHERE source.tournament_id = sqlc.arg(tournament_id)
    AND source.roster_id = sqlc.arg(roster_id)
    AND derived.tournament_id = source.tournament_id
    AND derived.roster_id = source.roster_id
ORDER BY dependency.source_node_id, dependency.derived_node_id;

-- name: CreateCorrectionProjectionDecision :exec
INSERT INTO correction_projection_decisions (
    id,
    command_id,
    sequence_number,
    projection_node_id,
    payload,
    payload_digest,
    recorded_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(command_id),
    sqlc.arg(sequence_number),
    sqlc.arg(projection_node_id),
    sqlc.arg(payload),
    sqlc.arg(payload_digest),
    sqlc.arg(recorded_at),
    sqlc.arg(created_at)
)
ON CONFLICT (id) DO NOTHING;

-- name: ListCorrectionProjectionDecisions :many
SELECT decision.id,
    decision.command_id,
    decision.sequence_number,
    decision.projection_node_id,
    decision.payload,
    decision.payload_digest,
    decision.recorded_at,
    decision.created_at
FROM correction_projection_decisions AS decision
INNER JOIN result_projection_nodes AS node
    ON node.id = decision.projection_node_id
WHERE node.tournament_id = sqlc.arg(tournament_id)
    AND node.roster_id = sqlc.arg(roster_id)
ORDER BY decision.recorded_at, decision.command_id, decision.sequence_number;

-- name: CreateCorrectionProjectionBinding :exec
INSERT INTO correction_projection_bindings (
    command_id,
    tournament_id,
    roster_id,
    artifact_kind,
    entity_id,
    source_id,
    node_id,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(artifact_kind),
    sqlc.arg(entity_id),
    sqlc.arg(source_id),
    sqlc.arg(node_id),
    sqlc.arg(created_at)
)
ON CONFLICT (
    tournament_id,
    roster_id,
    artifact_kind,
    entity_id,
    source_id
) DO NOTHING;

-- name: GetCorrectionProjectionBinding :one
SELECT command_id,
    tournament_id,
    roster_id,
    artifact_kind,
    entity_id,
    source_id,
    node_id,
    created_at
FROM correction_projection_bindings
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND artifact_kind = sqlc.arg(artifact_kind)
    AND entity_id = sqlc.arg(entity_id)
    AND source_id = sqlc.arg(source_id);

-- name: LockCorrectionGoldenStageGroups :many
WITH authority AS (
    SELECT progression.command_id
    FROM tournaments AS tournament
    INNER JOIN tournament_stage_progressions AS progression
        ON progression.tournament_id = tournament.id
        AND progression.roster_id = sqlc.arg(roster_id)
        AND progression.action IN ('start_golden', 'correction_start_golden', 'correction_refresh_golden')
        AND progression.resulting_tournament_state = 'golden'
        AND progression.resulting_tournament_revision = sqlc.arg(tournament_revision)
    WHERE tournament.id = sqlc.arg(tournament_id)
        AND tournament.state = 'golden'
        AND tournament.revision = sqlc.arg(tournament_revision)
    FOR UPDATE OF tournament, progression
)
SELECT group_revision.group_id,
    group_revision.revision_id AS group_revision_id,
    group_revision.stage_progression_command_id,
    group_revision.source_projection_revision_id,
    group_revision.source_projection_revision,
    group_revision.position_from,
    group_revision.position_to,
    group_revision.definition_digest
FROM authority
INNER JOIN golden_group_revisions AS group_revision
    ON group_revision.stage_progression_command_id = authority.command_id
    AND group_revision.tournament_id = sqlc.arg(tournament_id)
    AND group_revision.roster_id = sqlc.arg(roster_id)
ORDER BY group_revision.position_from, group_revision.position_to, group_revision.group_id
FOR UPDATE OF group_revision;

-- name: LockCorrectionGoldenStageMembers :many
SELECT group_revision.group_id,
    group_revision.revision_id AS group_revision_id,
    member.participant_id,
    member.standing_position
FROM golden_group_revisions AS group_revision
INNER JOIN tournament_stage_tie_group_members AS member
    ON member.command_id = group_revision.stage_progression_command_id
    AND member.tournament_id = group_revision.tournament_id
    AND member.roster_id = group_revision.roster_id
    AND member.group_id = group_revision.group_id
WHERE group_revision.stage_progression_command_id = sqlc.arg(stage_progression_command_id)
    AND group_revision.tournament_id = sqlc.arg(tournament_id)
    AND group_revision.roster_id = sqlc.arg(roster_id)
ORDER BY group_revision.position_from, group_revision.group_id, member.standing_position
FOR KEY SHARE OF group_revision, member;

-- name: LockCorrectionGoldenLatestStateAttempts :many
WITH latest_state AS (
    SELECT DISTINCT ON (state_revision.group_revision_id)
        state_revision.revision_id,
        state_revision.tournament_id,
        state_revision.roster_id,
        state_revision.group_id,
        state_revision.group_revision_id,
        state_revision.revision_number,
        state_revision.payload_digest
    FROM golden_state_revisions AS state_revision
    INNER JOIN golden_group_revisions AS group_revision
        ON group_revision.revision_id = state_revision.group_revision_id
        AND group_revision.tournament_id = state_revision.tournament_id
        AND group_revision.roster_id = state_revision.roster_id
    WHERE group_revision.stage_progression_command_id = sqlc.arg(stage_progression_command_id)
        AND state_revision.tournament_id = sqlc.arg(tournament_id)
        AND state_revision.roster_id = sqlc.arg(roster_id)
    ORDER BY state_revision.group_revision_id, state_revision.revision_number DESC
)
SELECT latest_state.group_id,
    latest_state.group_revision_id,
    latest_state.revision_id AS state_revision_id,
    latest_state.revision_number AS state_revision,
    latest_state.payload_digest AS state_payload_digest,
    state_attempt.attempt_id,
    state_attempt.attempt_number,
    state_attempt.previous_attempt_id,
    state_attempt.state AS snapshot_state,
    state_attempt.retained_at,
    state_attempt.started_at AS snapshot_started_at,
    state_attempt.finished_at AS snapshot_finished_at,
    attempt.state AS attempt_state,
    attempt.disclosed_at,
    attempt.ready_at,
    attempt.started_at AS attempt_started_at,
    attempt.paused_at,
    attempt.completed_at,
    attempt.cancelled_at,
    attempt.cancellation_reason
FROM latest_state
INNER JOIN golden_state_attempts AS state_attempt
    ON state_attempt.state_revision_id = latest_state.revision_id
    AND state_attempt.tournament_id = latest_state.tournament_id
    AND state_attempt.roster_id = latest_state.roster_id
INNER JOIN golden_attempts AS attempt
    ON attempt.id = state_attempt.attempt_id
    AND attempt.tournament_id = state_attempt.tournament_id
    AND attempt.roster_id = state_attempt.roster_id
ORDER BY latest_state.group_revision_id, state_attempt.attempt_number
FOR UPDATE OF state_attempt, attempt;

-- name: LockCorrectionGoldenLatestStateMembers :many
WITH latest_state AS (
    SELECT DISTINCT ON (state_revision.group_revision_id)
        state_revision.revision_id,
        state_revision.group_id,
        state_revision.group_revision_id,
        state_revision.tournament_id,
        state_revision.roster_id
    FROM golden_state_revisions AS state_revision
    INNER JOIN golden_group_revisions AS group_revision
        ON group_revision.revision_id = state_revision.group_revision_id
        AND group_revision.tournament_id = state_revision.tournament_id
        AND group_revision.roster_id = state_revision.roster_id
    WHERE group_revision.stage_progression_command_id = sqlc.arg(stage_progression_command_id)
        AND state_revision.tournament_id = sqlc.arg(tournament_id)
        AND state_revision.roster_id = sqlc.arg(roster_id)
    ORDER BY state_revision.group_revision_id, state_revision.revision_number DESC
)
SELECT latest_state.group_id,
    latest_state.group_revision_id,
    latest_state.revision_id AS state_revision_id,
    state_member.participant_id,
    state_member.excluded,
    state_member.position
FROM latest_state
INNER JOIN golden_state_members AS state_member
    ON state_member.state_revision_id = latest_state.revision_id
    AND state_member.tournament_id = latest_state.tournament_id
    AND state_member.roster_id = latest_state.roster_id
ORDER BY latest_state.group_revision_id, state_member.position
FOR KEY SHARE OF state_member;

-- name: LockCorrectionGoldenLatestStateAttemptMembers :many
WITH latest_state AS (
    SELECT DISTINCT ON (state_revision.group_revision_id)
        state_revision.revision_id,
        state_revision.group_revision_id,
        state_revision.tournament_id,
        state_revision.roster_id
    FROM golden_state_revisions AS state_revision
    INNER JOIN golden_group_revisions AS group_revision
        ON group_revision.revision_id = state_revision.group_revision_id
        AND group_revision.tournament_id = state_revision.tournament_id
        AND group_revision.roster_id = state_revision.roster_id
    WHERE group_revision.stage_progression_command_id = sqlc.arg(stage_progression_command_id)
        AND state_revision.tournament_id = sqlc.arg(tournament_id)
        AND state_revision.roster_id = sqlc.arg(roster_id)
    ORDER BY state_revision.group_revision_id, state_revision.revision_number DESC
)
SELECT latest_state.group_revision_id,
    latest_state.revision_id AS state_revision_id,
    attempt_member.attempt_id,
    attempt_member.participant_id,
    attempt_member.position
FROM latest_state
INNER JOIN golden_state_attempt_members AS attempt_member
    ON attempt_member.state_revision_id = latest_state.revision_id
    AND attempt_member.tournament_id = latest_state.tournament_id
    AND attempt_member.roster_id = latest_state.roster_id
ORDER BY latest_state.group_revision_id, attempt_member.attempt_id, attempt_member.position
FOR KEY SHARE OF attempt_member;

-- name: LockCorrectionGoldenPrestartHeads :many
SELECT repository_scope.group_id,
    repository_scope.group_revision_id,
    repository_scope.id AS repository_scope_id,
    repository_head.revision_id,
    repository_head.revision_number,
    repository_head.payload_digest,
    repository_revision.payload
FROM golden_repository_scopes AS repository_scope
INNER JOIN golden_group_revisions AS group_revision
    ON group_revision.revision_id = repository_scope.group_revision_id
    AND group_revision.group_id = repository_scope.group_id
    AND group_revision.tournament_id = repository_scope.tournament_id
    AND group_revision.roster_id = repository_scope.roster_id
INNER JOIN golden_repository_heads AS repository_head
    ON repository_head.scope_id = repository_scope.id
INNER JOIN golden_repository_revisions AS repository_revision
    ON repository_revision.scope_id = repository_head.scope_id
    AND repository_revision.revision_id = repository_head.revision_id
    AND repository_revision.revision_number = repository_head.revision_number
    AND repository_revision.payload_digest = repository_head.payload_digest
WHERE repository_scope.aggregate_kind = 'prestart'
    AND group_revision.stage_progression_command_id = sqlc.arg(stage_progression_command_id)
    AND repository_scope.tournament_id = sqlc.arg(tournament_id)
    AND repository_scope.roster_id = sqlc.arg(roster_id)
ORDER BY repository_scope.group_revision_id, repository_scope.id
FOR UPDATE OF repository_scope, repository_head, repository_revision;

-- name: LockCorrectionGoldenStateRevisions :many
SELECT state_revision.revision_id AS state_revision_id,
    state_revision.group_revision_id,
    state_revision.payload_digest
FROM golden_state_revisions AS state_revision
INNER JOIN golden_group_revisions AS group_revision
    ON group_revision.revision_id = state_revision.group_revision_id
    AND group_revision.tournament_id = state_revision.tournament_id
    AND group_revision.roster_id = state_revision.roster_id
WHERE group_revision.stage_progression_command_id = sqlc.arg(stage_progression_command_id)
    AND state_revision.tournament_id = sqlc.arg(tournament_id)
    AND state_revision.roster_id = sqlc.arg(roster_id)
ORDER BY state_revision.group_revision_id, state_revision.revision_number
FOR KEY SHARE OF state_revision, group_revision;

-- name: LockCorrectionGoldenPositionLedgerRevisions :many
SELECT ledger_revision.revision_id AS ledger_revision_id,
    ledger_revision.group_revision_id,
    ledger_revision.payload_digest
FROM golden_position_ledger_revisions AS ledger_revision
INNER JOIN golden_group_revisions AS group_revision
    ON group_revision.revision_id = ledger_revision.group_revision_id
    AND group_revision.tournament_id = ledger_revision.tournament_id
    AND group_revision.roster_id = ledger_revision.roster_id
WHERE group_revision.stage_progression_command_id = sqlc.arg(stage_progression_command_id)
    AND ledger_revision.tournament_id = sqlc.arg(tournament_id)
    AND ledger_revision.roster_id = sqlc.arg(roster_id)
ORDER BY ledger_revision.group_revision_id, ledger_revision.revision_number
FOR KEY SHARE OF ledger_revision, group_revision;

-- name: CancelCorrectionGoldenAttempt :one
UPDATE golden_attempts
SET state = 'cancelled',
    cancelled_at = sqlc.arg(cancelled_at),
    cancellation_reason = sqlc.arg(cancellation_reason)
WHERE id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND state = sqlc.arg(expected_state)
RETURNING id,
    tournament_id,
    roster_id,
    state,
    cancelled_at,
    cancellation_reason;

-- name: AdvanceCorrectionGoldenStage :one
UPDATE tournaments
SET state = 'playoffs',
    paused_from_state = NULL,
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(tournament_id)
    AND state = 'golden'
    AND revision = sqlc.arg(expected_tournament_revision)
RETURNING id,
    state,
    revision,
    updated_at;

-- name: AdvanceCorrectionTournamentStageCAS :one
UPDATE tournaments
SET state = sqlc.arg(resulting_tournament_state),
    paused_from_state = NULL,
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(tournament_id)
    AND state = sqlc.arg(expected_tournament_state)
    AND revision = sqlc.arg(expected_tournament_revision)
RETURNING id,
    state,
    revision,
    updated_at;

-- name: CreateCorrectionTournamentLifecycleCommand :one
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
    NULL,
    NULL,
    NULL,
    NULL,
    sqlc.arg(preset),
    sqlc.arg(roster_size),
    sqlc.arg(tournament_created_at),
    sqlc.arg(tournament_updated_at),
    sqlc.narg(tournament_started_at)::TIMESTAMPTZ,
    sqlc.narg(tournament_finished_at)::TIMESTAMPTZ,
    sqlc.arg(executed_at),
    sqlc.arg(created_at)
)
RETURNING command_id;

-- name: CreateCorrectionStageProgression :one
WITH authority AS MATERIALIZED (
    SELECT tournament.id,
        roster.id AS roster_id
    FROM tournaments AS tournament
    INNER JOIN rosters AS roster
        ON roster.tournament_id = tournament.id
    INNER JOIN projection_revisions AS source_revision
        ON source_revision.id = sqlc.arg(source_projection_revision_id)
        AND source_revision.tournament_id = tournament.id
        AND source_revision.roster_id = roster.id
        AND source_revision.revision_number = sqlc.arg(source_projection_revision)
    INNER JOIN projection_revisions AS resulting_revision
        ON resulting_revision.id = sqlc.arg(resulting_projection_revision_id)
        AND resulting_revision.tournament_id = tournament.id
        AND resulting_revision.roster_id = roster.id
        AND resulting_revision.revision_number = sqlc.arg(resulting_projection_revision)
    WHERE tournament.id = sqlc.arg(tournament_id)
        AND roster.id = sqlc.arg(roster_id)
        AND tournament.state = sqlc.arg(resulting_tournament_state)
        AND tournament.revision = sqlc.arg(resulting_tournament_revision)
        AND source_revision.state = 'superseded'
        AND source_revision.superseded_by_revision_id = resulting_revision.id
        AND resulting_revision.state = 'published'
    FOR KEY SHARE OF tournament, roster, source_revision, resulting_revision
)
INSERT INTO tournament_stage_progressions (
    command_id,
    tournament_id,
    roster_id,
    actor_id,
    action,
    source_tournament_revision,
    source_tournament_state,
    source_projection_revision_id,
    source_projection_revision,
    resulting_projection_revision_id,
    resulting_projection_revision,
    resulting_tournament_revision,
    resulting_tournament_state,
    proof,
    proof_digest,
    executed_at,
    created_at
)
SELECT sqlc.arg(command_id),
    authority.id,
    authority.roster_id,
    sqlc.arg(actor_id),
    sqlc.arg(action),
    sqlc.arg(source_tournament_revision),
    sqlc.arg(source_tournament_state),
    sqlc.arg(source_projection_revision_id),
    sqlc.arg(source_projection_revision),
    sqlc.arg(resulting_projection_revision_id),
    sqlc.arg(resulting_projection_revision),
    sqlc.arg(resulting_tournament_revision),
    sqlc.arg(resulting_tournament_state),
    sqlc.arg(proof)::JSONB,
    sqlc.arg(proof_digest),
    sqlc.arg(executed_at),
    sqlc.arg(created_at)
FROM authority
RETURNING command_id;

-- name: CreateCorrectionStageTieGroup :one
INSERT INTO tournament_stage_tie_groups (
    command_id,
    tournament_id,
    roster_id,
    group_id,
    group_revision_id,
    source_projection_revision_id,
    source_projection_revision,
    position_from,
    position_to,
    proof,
    proof_digest,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(group_id),
    sqlc.arg(group_revision_id),
    sqlc.arg(source_projection_revision_id),
    sqlc.arg(source_projection_revision),
    sqlc.arg(position_from),
    sqlc.arg(position_to),
    sqlc.arg(proof)::JSONB,
    sqlc.arg(proof_digest),
    sqlc.arg(created_at)
)
RETURNING group_id;

-- name: CreateCorrectionStageTieGroupMember :one
INSERT INTO tournament_stage_tie_group_members (
    command_id,
    tournament_id,
    roster_id,
    group_id,
    participant_id,
    standing_position,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(group_id),
    sqlc.arg(participant_id),
    sqlc.arg(standing_position),
    sqlc.arg(created_at)
)
RETURNING participant_id;

-- name: CreateCorrectionGoldenGroupRevision :one
INSERT INTO golden_group_revisions (
    revision_id,
    group_id,
    stage_progression_command_id,
    tournament_id,
    roster_id,
    source_projection_revision_id,
    source_projection_revision,
    position_from,
    position_to,
    definition,
    definition_digest,
    created_at
)
VALUES (
    sqlc.arg(revision_id),
    sqlc.arg(group_id),
    sqlc.arg(stage_progression_command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(source_projection_revision_id),
    sqlc.arg(source_projection_revision),
    sqlc.arg(position_from),
    sqlc.arg(position_to),
    sqlc.arg(definition)::JSONB,
    sqlc.arg(definition_digest),
    sqlc.arg(created_at)
)
RETURNING revision_id;

-- name: CreateGoldenCorrectionStageTombstone :one
INSERT INTO golden_correction_stage_tombstones (
    command_id,
    tournament_id,
    roster_id,
    source_tournament_revision,
    resulting_tournament_revision,
    resulting_tournament_state,
    source_projection_revision_id,
    source_projection_revision,
    resulting_projection_revision_id,
    resulting_projection_revision,
    proof_digest,
    corrected_at,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(source_tournament_revision),
    sqlc.arg(resulting_tournament_revision),
    sqlc.arg(resulting_tournament_state),
    sqlc.arg(source_projection_revision_id),
    sqlc.arg(source_projection_revision),
    sqlc.arg(resulting_projection_revision_id),
    sqlc.arg(resulting_projection_revision),
    sqlc.arg(proof_digest),
    sqlc.arg(corrected_at),
    sqlc.arg(created_at)
)
RETURNING command_id;

-- name: CreateGoldenCorrectionGroupTombstone :one
INSERT INTO golden_correction_group_tombstones (
    command_id,
    tournament_id,
    roster_id,
    group_id,
    group_revision_id,
    successor_revision_id,
    replacement_group_id,
    superseded_at,
    proof_digest,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(group_id),
    sqlc.arg(group_revision_id),
    sqlc.arg(successor_revision_id),
    sqlc.narg(replacement_group_id)::uuid,
    sqlc.arg(superseded_at),
    sqlc.arg(proof_digest),
    sqlc.arg(created_at)
)
RETURNING group_revision_id;

-- name: CreateGoldenCorrectionStateTombstone :one
INSERT INTO golden_correction_state_tombstones (
    command_id,
    tournament_id,
    roster_id,
    group_revision_id,
    state_revision_id,
    payload_digest,
    tombstoned_at,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(group_revision_id),
    sqlc.arg(state_revision_id),
    sqlc.arg(payload_digest),
    sqlc.arg(tombstoned_at),
    sqlc.arg(created_at)
)
RETURNING state_revision_id;

-- name: CreateGoldenCorrectionPositionTombstone :one
INSERT INTO golden_correction_position_tombstones (
    command_id,
    tournament_id,
    roster_id,
    group_revision_id,
    ledger_revision_id,
    payload_digest,
    tombstoned_at,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(group_revision_id),
    sqlc.arg(ledger_revision_id),
    sqlc.arg(payload_digest),
    sqlc.arg(tombstoned_at),
    sqlc.arg(created_at)
)
RETURNING ledger_revision_id;

-- name: CreateGoldenCorrectionAttemptTombstone :one
INSERT INTO golden_correction_attempt_tombstones (
    command_id,
    tournament_id,
    roster_id,
    group_revision_id,
    successor_revision_id,
    state_revision_id,
    attempt_id,
    prior_state,
    retained_at,
    cancelled_at,
    cancellation_reason,
    state_payload_digest,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(group_revision_id),
    sqlc.arg(successor_revision_id),
    sqlc.arg(state_revision_id),
    sqlc.arg(attempt_id),
    sqlc.arg(prior_state),
    sqlc.arg(retained_at),
    sqlc.arg(cancelled_at),
    sqlc.arg(cancellation_reason),
    sqlc.arg(state_payload_digest),
    sqlc.arg(created_at)
)
RETURNING attempt_id;

-- name: SealGoldenCorrectionTombstones :one
INSERT INTO golden_correction_tombstone_seals (
    command_id,
    tournament_id,
    roster_id,
    proof_digest,
    sealed_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(proof_digest),
    sqlc.arg(sealed_at)
)
RETURNING command_id;
