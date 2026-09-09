-- name: FindOperatorReplayReserveCommand :one
SELECT command_id,
    exhaustion_command_id,
    tournament_id,
    roster_id,
    old_wave_id,
    series_id,
    slot_id,
    assignment_id,
    assignment_attempt_id,
    failed_game_id,
    closure_revision_id,
    from_snapshot_id,
    actor_id,
    reason,
    expected_assignment_revision,
    expected_pool_revision_id,
    expected_pool_revision,
    expected_history_revision_id,
    expected_history_revision,
    expected_artifact_revision_id,
    expected_artifact_revision,
    expected_reservation_revision_id,
    expected_reservation_revision,
    expected_category_revision_id,
    expected_category_revision,
    proposed_task_id,
    proposed_version,
    proposed_snapshot_id,
    evidence_id,
    edge_id,
    reservation_id,
    reserve_position,
    source_series_revision,
    resulting_series_revision,
    request_digest,
    evidence_digest,
    proof_digest,
    content_digest,
    authority_document,
    record_document,
    promoted_at,
    created_at
FROM operator_replay_reserves
WHERE command_id = sqlc.arg(command_id)
FOR UPDATE;

-- name: GetTournamentAdminReplayTime :one
SELECT clock_timestamp()::TIMESTAMPTZ AS db_now;

-- name: LockReplayReserveExhaustionForOperatorReserve :one
SELECT command_id,
    tournament_id,
    roster_id,
    old_wave_id,
    series_id,
    slot_id,
    assignment_id,
    assignment_attempt_id,
    failed_game_id,
    closure_revision_id,
    active_snapshot_id,
    from_snapshot_id,
    reserve_position,
    category,
    source_series_revision,
    resulting_series_revision,
    request_digest,
    authority_document,
    record_document,
    paused_at,
    created_at
FROM replay_reserve_exhaustions
WHERE command_id = sqlc.arg(exhaustion_command_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND old_wave_id = sqlc.arg(old_wave_id)
    AND series_id = sqlc.arg(series_id)
    AND slot_id = sqlc.arg(slot_id)
    AND assignment_id = sqlc.arg(assignment_id)
FOR UPDATE;

-- name: LockReplayReserveAuthority :one
SELECT authority.assignment_id,
    authority.tournament_id,
    authority.roster_id,
    authority.series_id,
    authority.slot_id,
    authority.assignment_attempt_id,
    authority.active_snapshot_id,
    authority.required_category,
    authority.assignment_revision,
    authority.pool_revision_id,
    authority.pool_revision,
    authority.history_revision_id,
    authority.history_revision,
    authority.artifact_revision_id,
    authority.artifact_revision,
    authority.reservation_revision_id,
    authority.reservation_revision,
    authority.category_revision_id,
    authority.category_revision,
    candidate_version.task_id AS candidate_task_id,
    candidate_version.version AS candidate_version,
    candidate_version.content_digest AS candidate_content_digest,
    candidate_task.enabled AS task_enabled,
    COALESCE(health.healthy, false) AS task_healthy,
    true AS task_mutation_locked,
    EXISTS (
        SELECT 1
        FROM task_delivery_receipts AS receipt
        WHERE receipt.task_id = candidate_version.task_id
            AND receipt.task_version = candidate_version.version
    ) AS task_publicly_exposed,
    authority.revision,
    authority.created_at,
    authority.updated_at,
    assignment.id AS current_assignment_id,
    assignment.revision AS current_assignment_revision,
    assignment.snapshot_id AS current_snapshot_id,
    candidate_task.id AS candidate_task_row_id,
    candidate_version.title AS candidate_title,
    candidate_version.description AS candidate_description,
    candidate_version.category AS candidate_category,
    candidate_version.difficulty AS candidate_difficulty,
    candidate_version.time_limit AS candidate_time_limit,
    candidate_version.flag AS candidate_flag,
    candidate_version.hint_1 AS candidate_hint_1,
    candidate_version.hint_2 AS candidate_hint_2,
    candidate_version.hint_3 AS candidate_hint_3,
    candidate_version.task_url AS candidate_task_url,
    candidate_version.source_file_url AS candidate_source_file_url,
    candidate_version.created_at AS candidate_created_at
FROM replay_reserve_authorities AS authority
JOIN assignments AS assignment ON assignment.id = authority.assignment_id
JOIN replay_reserve_authority_pool_versions AS authority_pool
    ON authority_pool.assignment_id = authority.assignment_id
    AND authority_pool.task_id = sqlc.arg(proposed_task_id)
    AND authority_pool.task_version = sqlc.arg(proposed_version)
JOIN task_versions AS candidate_version
    ON candidate_version.task_id = authority_pool.task_id
    AND candidate_version.version = authority_pool.task_version
JOIN tasks AS candidate_task ON candidate_task.id = candidate_version.task_id
LEFT JOIN LATERAL (
    SELECT attestation.healthy
    FROM task_version_health_attestations AS attestation
    WHERE attestation.task_id = candidate_version.task_id
        AND attestation.task_version = candidate_version.version
    ORDER BY attestation.revision DESC
    LIMIT 1
    FOR UPDATE
) AS health ON true
WHERE authority.tournament_id = sqlc.arg(tournament_id)
    AND authority.roster_id = sqlc.arg(roster_id)
    AND authority.series_id = sqlc.arg(series_id)
    AND authority.slot_id = sqlc.arg(slot_id)
    AND authority.assignment_id = sqlc.arg(assignment_id)
    AND authority.assignment_attempt_id = sqlc.arg(assignment_attempt_id)
    AND authority.active_snapshot_id = sqlc.arg(expected_snapshot_id)
    AND authority.assignment_revision = sqlc.arg(expected_assignment_revision)
    AND authority.pool_revision_id = sqlc.arg(expected_pool_revision_id)
    AND authority.pool_revision = sqlc.arg(expected_pool_revision)
    AND authority.history_revision_id = sqlc.arg(expected_history_revision_id)
    AND authority.history_revision = sqlc.arg(expected_history_revision)
    AND authority.artifact_revision_id = sqlc.arg(expected_artifact_revision_id)
    AND authority.artifact_revision = sqlc.arg(expected_artifact_revision)
    AND authority.reservation_revision_id = sqlc.arg(expected_reservation_revision_id)
    AND authority.reservation_revision = sqlc.arg(expected_reservation_revision)
    AND authority.category_revision_id = sqlc.arg(expected_category_revision_id)
    AND authority.category_revision = sqlc.arg(expected_category_revision)
    AND candidate_version.category = authority.required_category
    AND candidate_task.enabled
    AND candidate_task.deleted_at IS NULL
    AND COALESCE(health.healthy, false)
    AND NOT EXISTS (
        SELECT 1
        FROM task_delivery_receipts AS receipt
        WHERE receipt.task_id = candidate_version.task_id
            AND receipt.task_version = candidate_version.version
    )
    AND NOT EXISTS (
        SELECT 1
        FROM task_version_reservations AS used_reservation
        WHERE used_reservation.plan_id = assignment.plan_id
            AND used_reservation.branch_id = assignment.branch_id
            AND used_reservation.task_id = candidate_version.task_id
            AND used_reservation.task_version = candidate_version.version
            AND used_reservation.state = 'committed'
    )
    AND assignment.revision = authority.assignment_revision
    AND assignment.snapshot_id = authority.active_snapshot_id
FOR UPDATE OF authority, assignment, candidate_version, candidate_task;

-- A replay authority is created only from the committed final draft child
-- that produced this concrete assignment. Caller-supplied replay evidence is
-- deliberately absent: source revisions, category, and reservation head all
-- come from locked normalized rows.
-- name: CreateReplayReserveAuthority :one
INSERT INTO replay_reserve_authorities (
    assignment_id,
    tournament_id,
    roster_id,
    series_id,
    slot_id,
    assignment_attempt_id,
    active_snapshot_id,
    required_category,
    assignment_revision,
    pool_revision_id,
    pool_revision,
    history_revision_id,
    history_revision,
    artifact_revision_id,
    artifact_revision,
    reservation_revision_id,
    reservation_revision,
    category_revision_id,
    category_revision,
    revision,
    created_at,
    updated_at
)
SELECT assignment.id AS authority_assignment_id,
    source.tournament_id,
    source.roster_id,
    source.series_id,
    source.slot_id,
    assignment.attempt_id,
    assignment.snapshot_id,
    branch.category_sequence ->> 0,
    assignment.revision AS authority_assignment_revision,
    source.pool_revision_id,
    source.pool_revision,
    source.history_revision_id,
    source.history_revision,
    source.artifact_revision_id,
    source.artifact_revision,
    reservation.id AS authority_reservation_id,
    reservation.revision AS authority_reservation_revision,
    source.category_revision_id,
    source.category_revision,
    1,
    assignment.created_at AS authority_created_at,
    assignment.created_at AS authority_updated_at
FROM assignments AS assignment
JOIN assignment_plans AS plan ON plan.id = assignment.plan_id
JOIN assignment_branches AS branch
    ON branch.id = assignment.branch_id
    AND branch.plan_id = plan.id
JOIN exact_draft_assignment_child_sources AS source
    ON source.child_branch_id = branch.id
    AND source.plan_id = plan.id
JOIN task_version_reservations AS reservation
    ON reservation.id = assignment.reservation_id
    AND reservation.plan_id = plan.id
    AND reservation.branch_id = branch.id
JOIN game_attempts AS attempt
    ON attempt.id = assignment.attempt_id
    AND attempt.series_id = assignment.series_id
    AND attempt.roster_id = assignment.roster_id
WHERE assignment.id = sqlc.arg(assignment_id)
    AND assignment.state = 'active'
    AND plan.kind = 'exact_draft'
    AND plan.state = 'committed'
    AND branch.state = 'active'
    AND reservation.state = 'committed'
    AND attempt.slot_id = source.slot_id
RETURNING assignment_id;

-- name: CreateReplayReserveAuthorityPoolVersion :exec
INSERT INTO replay_reserve_authority_pool_versions (
    assignment_id,
    task_id,
    task_version,
    created_at
)
SELECT authority.assignment_id,
    candidate.task_id,
    candidate.task_version,
    authority.created_at
FROM replay_reserve_authorities AS authority
JOIN assignments AS assignment ON assignment.id = authority.assignment_id
JOIN exact_draft_assignment_child_candidates AS candidate
    ON candidate.plan_id = assignment.plan_id
    AND candidate.child_branch_id = assignment.branch_id
WHERE authority.assignment_id = sqlc.arg(assignment_id);

-- name: LockReplayReserveAuthorityPool :many
SELECT authority_version.assignment_id,
    authority_version.task_id,
    authority_version.task_version,
    authority_version.created_at
FROM replay_reserve_authority_pool_versions AS authority_version
WHERE authority_version.assignment_id = sqlc.arg(assignment_id)
ORDER BY authority_version.task_id, authority_version.task_version
FOR UPDATE;

-- name: AdvanceReplayReserveAuthorityCAS :one
UPDATE replay_reserve_authorities
SET revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE assignment_id = sqlc.arg(assignment_id)
    AND revision = sqlc.arg(expected_revision)
    AND active_snapshot_id = sqlc.arg(expected_snapshot_id)
RETURNING revision;

-- name: CreateOperatorReplayReserveCommand :one
INSERT INTO operator_replay_reserves (
    command_id,
    exhaustion_command_id,
    tournament_id,
    roster_id,
    old_wave_id,
    series_id,
    slot_id,
    assignment_id,
    assignment_attempt_id,
    failed_game_id,
    closure_revision_id,
    from_snapshot_id,
    actor_id,
    reason,
    expected_assignment_revision,
    expected_pool_revision_id,
    expected_pool_revision,
    expected_history_revision_id,
    expected_history_revision,
    expected_artifact_revision_id,
    expected_artifact_revision,
    expected_reservation_revision_id,
    expected_reservation_revision,
    expected_category_revision_id,
    expected_category_revision,
    proposed_task_id,
    proposed_version,
    proposed_snapshot_id,
    evidence_id,
    edge_id,
    reservation_id,
    reserve_position,
    source_series_revision,
    resulting_series_revision,
    request_digest,
    evidence_digest,
    proof_digest,
    content_digest,
    authority_document,
    record_document,
    promoted_at,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(exhaustion_command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(old_wave_id),
    sqlc.arg(series_id),
    sqlc.arg(slot_id),
    sqlc.arg(assignment_id),
    sqlc.arg(assignment_attempt_id),
    sqlc.arg(failed_game_id),
    sqlc.arg(closure_revision_id),
    sqlc.arg(from_snapshot_id),
    sqlc.arg(actor_id),
    sqlc.arg(reason),
    sqlc.arg(expected_assignment_revision),
    sqlc.arg(expected_pool_revision_id),
    sqlc.arg(expected_pool_revision),
    sqlc.arg(expected_history_revision_id),
    sqlc.arg(expected_history_revision),
    sqlc.arg(expected_artifact_revision_id),
    sqlc.arg(expected_artifact_revision),
    sqlc.arg(expected_reservation_revision_id),
    sqlc.arg(expected_reservation_revision),
    sqlc.arg(expected_category_revision_id),
    sqlc.arg(expected_category_revision),
    sqlc.arg(proposed_task_id),
    sqlc.arg(proposed_version),
    sqlc.arg(proposed_snapshot_id),
    sqlc.arg(evidence_id),
    sqlc.arg(edge_id),
    sqlc.arg(reservation_id),
    4,
    sqlc.arg(source_series_revision),
    sqlc.arg(resulting_series_revision),
    sqlc.arg(request_digest),
    sqlc.arg(evidence_digest),
    sqlc.arg(proof_digest),
    sqlc.arg(content_digest),
    sqlc.arg(authority_document),
    sqlc.arg(record_document),
    sqlc.arg(promoted_at),
    sqlc.arg(created_at)
)
RETURNING command_id;

-- name: FindReplayReplacementCommand :one
SELECT command_id,
    tournament_id,
    roster_id,
    old_wave_id,
    series_id,
    slot_id,
    assignment_id,
    assignment_attempt_id,
    failed_game_id,
    closure_revision_id,
    from_snapshot_id,
    replacement_assignment_attempt_id,
    replacement_game_id,
    replacement_wave_id,
    replacement_wave_revision_id,
    ready_window_id,
    ready_window_revision_id,
    snapshot_id,
    reserve_position,
    source_series_revision,
    resulting_series_revision,
    actor_id,
    reason,
    request_digest,
    authority_document,
    record_document,
    opened_at,
    created_at
FROM replay_replacements
WHERE command_id = sqlc.arg(command_id)
FOR UPDATE;

-- name: LockOperatorReplayReserveForReplacement :one
SELECT command_id,
    exhaustion_command_id,
    tournament_id,
    roster_id,
    old_wave_id,
    series_id,
    slot_id,
    assignment_id,
    assignment_attempt_id,
    failed_game_id,
    closure_revision_id,
    from_snapshot_id,
    actor_id,
    reason,
    expected_assignment_revision,
    expected_pool_revision_id,
    expected_pool_revision,
    expected_history_revision_id,
    expected_history_revision,
    expected_artifact_revision_id,
    expected_artifact_revision,
    expected_reservation_revision_id,
    expected_reservation_revision,
    expected_category_revision_id,
    expected_category_revision,
    proposed_task_id,
    proposed_version,
    proposed_snapshot_id,
    evidence_id,
    edge_id,
    reservation_id,
    reserve_position,
    source_series_revision,
    resulting_series_revision,
    request_digest,
    evidence_digest,
    proof_digest,
    content_digest,
    authority_document,
    record_document,
    promoted_at,
    created_at
FROM operator_replay_reserves
WHERE tournament_id = sqlc.arg(tournament_id)
    AND old_wave_id = sqlc.arg(old_wave_id)
    AND series_id = sqlc.arg(series_id)
    AND slot_id = sqlc.arg(slot_id)
    AND assignment_id = sqlc.arg(assignment_id)
    AND assignment_attempt_id = sqlc.arg(assignment_attempt_id)
    AND failed_game_id = sqlc.arg(failed_game_id)
FOR UPDATE;

-- name: CreateReplayReplacementCommand :one
INSERT INTO replay_replacements (
    command_id,
    tournament_id,
    roster_id,
    old_wave_id,
    series_id,
    slot_id,
    assignment_id,
    assignment_attempt_id,
    failed_game_id,
    closure_revision_id,
    from_snapshot_id,
    replacement_assignment_attempt_id,
    replacement_game_id,
    replacement_wave_id,
    replacement_wave_revision_id,
    ready_window_id,
    ready_window_revision_id,
    snapshot_id,
    reserve_position,
    source_series_revision,
    resulting_series_revision,
    actor_id,
    reason,
    request_digest,
    authority_document,
    record_document,
    opened_at,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(old_wave_id),
    sqlc.arg(series_id),
    sqlc.arg(slot_id),
    sqlc.arg(assignment_id),
    sqlc.arg(assignment_attempt_id),
    sqlc.arg(failed_game_id),
    sqlc.arg(closure_revision_id),
    sqlc.arg(from_snapshot_id),
    sqlc.arg(replacement_assignment_attempt_id),
    sqlc.arg(replacement_game_id),
    sqlc.arg(replacement_wave_id),
    sqlc.arg(replacement_wave_revision_id),
    sqlc.arg(ready_window_id),
    sqlc.arg(ready_window_revision_id),
    sqlc.arg(snapshot_id),
    sqlc.arg(reserve_position),
    sqlc.arg(source_series_revision),
    sqlc.arg(resulting_series_revision),
    sqlc.arg(actor_id),
    sqlc.arg(reason),
    sqlc.arg(request_digest),
    sqlc.arg(authority_document),
    sqlc.arg(record_document),
    sqlc.arg(opened_at),
    sqlc.arg(created_at)
)
RETURNING command_id;

-- name: LockReplayWorkflowSource :one
SELECT sqlc.embed(tournament),
    sqlc.embed(roster),
    sqlc.embed(old_wave),
    sqlc.embed(series),
    sqlc.embed(score_head),
    sqlc.embed(slot),
    sqlc.embed(failed_game),
    sqlc.embed(assignment),
    sqlc.embed(snapshot),
    sqlc.embed(plan),
    sqlc.embed(branch)
FROM tournaments AS tournament
JOIN rosters AS roster ON roster.tournament_id = tournament.id
JOIN waves AS old_wave
    ON old_wave.id = sqlc.arg(old_wave_id)
    AND old_wave.tournament_id = tournament.id
    AND old_wave.roster_id = roster.id
JOIN wave_series AS old_membership
    ON old_membership.wave_id = old_wave.id
JOIN series
    ON series.id = old_membership.series_id
    AND series.id = sqlc.arg(series_id)
    AND series.tournament_id = tournament.id
    AND series.roster_id = roster.id
JOIN series_score_heads AS score_head
    ON score_head.series_id = series.id
    AND score_head.roster_id = roster.id
JOIN game_slots AS slot
    ON slot.id = sqlc.arg(slot_id)
    AND slot.series_id = series.id
    AND slot.roster_id = roster.id
JOIN game_attempts AS failed_game
    ON failed_game.id = sqlc.arg(failed_game_id)
    AND failed_game.slot_id = slot.id
    AND failed_game.series_id = series.id
    AND failed_game.roster_id = roster.id
JOIN assignments AS assignment
    ON assignment.id = sqlc.arg(assignment_id)
    AND assignment.attempt_id = failed_game.id
    AND assignment.series_id = series.id
    AND assignment.roster_id = roster.id
JOIN task_snapshots AS snapshot ON snapshot.id = assignment.snapshot_id
JOIN assignment_plans AS plan ON plan.id = assignment.plan_id
JOIN assignment_branches AS branch
    ON branch.id = assignment.branch_id
    AND branch.plan_id = plan.id
WHERE tournament.id = sqlc.arg(tournament_id)
FOR UPDATE OF tournament,
    roster,
    old_wave,
    series,
    score_head,
    slot,
    failed_game,
    assignment,
    snapshot,
    plan,
    branch;

-- name: LockReplayWorkflowOldWaveExecution :many
SELECT sqlc.embed(ready_window),
    sqlc.embed(member),
    sqlc.embed(readiness)
FROM ready_windows AS ready_window
JOIN wave_members AS member
    ON member.wave_id = ready_window.wave_id
    AND member.roster_id = ready_window.roster_id
JOIN wave_readiness AS readiness
    ON readiness.wave_id = member.wave_id
    AND readiness.roster_id = member.roster_id
    AND readiness.participant_id = member.participant_id
    AND readiness.ready_window_id = ready_window.id
WHERE ready_window.wave_id = sqlc.arg(wave_id)
    AND ready_window.roster_id = sqlc.arg(roster_id)
ORDER BY member.participant_id
FOR UPDATE OF ready_window, member, readiness;

-- name: LockReplayWorkflowGameResultHeads :many
SELECT game.id AS game_attempt_id,
    result_head.current_revision_id AS result_revision_id,
    result_head.revision AS result_head_revision,
    result_revision.result_event_id,
    result_revision.previous_revision_id,
    result_revision.revision_number,
    result_revision.result_state,
    result_revision.result_reason,
    result_revision.winner_id,
    result_revision.created_at,
    event.occurred_at
FROM game_attempts AS game
JOIN official_result_heads AS result_head
    ON result_head.entity_kind = 'game_attempt'
    AND result_head.entity_id = game.id
    AND result_head.series_id = game.series_id
    AND result_head.roster_id = game.roster_id
JOIN official_result_revisions AS result_revision
    ON result_revision.id = result_head.current_revision_id
JOIN result_events AS event
    ON event.id = result_revision.result_event_id
WHERE game.series_id = sqlc.arg(series_id)
    AND game.roster_id = sqlc.arg(roster_id)
ORDER BY game.attempt_number, game.id
FOR UPDATE OF result_head, result_revision, event;

-- name: LockReplayWorkflowScoreHead :one
SELECT score_head.current_revision_id,
    score_head.revision AS head_revision,
    score_head.updated_at AS head_updated_at,
    score_revision.id AS score_revision_id,
    score_revision.previous_revision_id,
    score_revision.revision_number,
    score_revision.first_participant_wins,
    score_revision.second_participant_wins,
    score_revision.created_at AS score_recorded_at
FROM series_score_heads AS score_head
JOIN series_score_revisions AS score_revision
    ON score_revision.id = score_head.current_revision_id
WHERE score_head.series_id = sqlc.arg(series_id)
    AND score_head.roster_id = sqlc.arg(roster_id)
FOR UPDATE OF score_head, score_revision;

-- name: LockReplayWorkflowSeriesGraph :many
SELECT sqlc.embed(slot),
    sqlc.embed(game)
FROM game_slots AS slot
JOIN game_attempts AS game
    ON game.slot_id = slot.id
    AND game.series_id = slot.series_id
    AND game.roster_id = slot.roster_id
WHERE slot.series_id = sqlc.arg(series_id)
    AND slot.roster_id = sqlc.arg(roster_id)
ORDER BY slot.slot_number, game.attempt_number
FOR UPDATE OF slot, game;

-- name: LockReplayWorkflowReserveChain :many
SELECT sqlc.embed(edge),
    sqlc.embed(reservation),
    sqlc.embed(snapshot)
FROM assignment_plan_edges AS edge
JOIN task_version_reservations AS reservation
    ON reservation.edge_id = edge.id
    AND reservation.plan_id = edge.plan_id
    AND reservation.branch_id = edge.branch_id
    AND reservation.task_id = edge.task_id
    AND reservation.task_version = edge.task_version
JOIN task_snapshots AS snapshot
    ON snapshot.reservation_id = reservation.id
    AND snapshot.task_id = reservation.task_id
    AND snapshot.task_version = reservation.task_version
WHERE edge.plan_id = sqlc.arg(plan_id)
    AND edge.branch_id = sqlc.arg(branch_id)
ORDER BY edge.position
FOR UPDATE OF edge, reservation, snapshot;

-- name: LockReplayWorkflowParticipantReservations :many
SELECT participant.id AS participant_id,
    participant.player_id,
    reservation.reservation_id,
    reservation.tournament_id,
    reservation.revision,
    reservation.acquired_at,
    reservation.updated_at
FROM participants AS participant
JOIN participant_reservations AS reservation ON reservation.player_id = participant.player_id
WHERE participant.roster_id = sqlc.arg(roster_id)
    AND participant.id = ANY(sqlc.arg(participant_ids)::UUID[])
ORDER BY participant.id
FOR UPDATE OF participant, reservation;

-- name: LockReplayWorkflowTask :one
SELECT id,
    title,
    description,
    category,
    difficulty,
    time_limit,
    flag,
    hint_1,
    hint_2,
    hint_3,
    task_url,
    source_file_url,
    created_at
FROM tasks
WHERE id = sqlc.arg(task_id)
FOR UPDATE;

-- name: LockReplayWorkflowReceipts :many
SELECT id,
    assignment_id,
    attempt_id,
    roster_id,
    participant_id,
    instance_id,
    snapshot_id,
    task_id,
    task_version,
    delivered_at,
    created_at
FROM task_delivery_receipts
WHERE assignment_id = sqlc.arg(assignment_id)
ORDER BY delivered_at, id
FOR UPDATE;

-- name: LockReplayWorkflowRoutes :many
SELECT id,
    tournament_id,
    roster_id,
    wave_id,
    series_id,
    slot_id,
    game_attempt_id,
    category,
    routed_at,
    created_at
FROM wave_member_routes
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND wave_id = sqlc.arg(wave_id)
ORDER BY routed_at, id
FOR UPDATE;

-- name: CreateOperatorReplayReserveEdge :one
INSERT INTO assignment_plan_edges (
    id,
    plan_id,
    branch_id,
    position,
    task_id,
    task_version,
    operator_reserve_command_id,
    selection_evidence,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(plan_id),
    sqlc.arg(branch_id),
    4,
    sqlc.arg(task_id),
    sqlc.arg(task_version),
    sqlc.arg(operator_reserve_command_id),
    sqlc.arg(selection_evidence),
    sqlc.arg(created_at)
)
RETURNING id;

-- name: CreateOperatorReplayReserveReservation :one
INSERT INTO task_version_reservations (
    id,
    edge_id,
    plan_id,
    branch_id,
    task_id,
    task_version,
    revision,
    state,
    committed_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(edge_id),
    sqlc.arg(plan_id),
    sqlc.arg(branch_id),
    sqlc.arg(task_id),
    sqlc.arg(task_version),
    1,
    'committed',
    sqlc.arg(committed_at),
    sqlc.arg(created_at)
)
RETURNING id;

-- name: CreateOperatorReplayReserveSnapshot :one
INSERT INTO task_snapshots (
    id,
    reservation_id,
    task_id,
    task_version,
    kind,
    title,
    description,
    category,
    difficulty,
    time_limit,
    flag,
    hints,
    task_url,
    source_file_url,
    content_digest,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(reservation_id),
    sqlc.arg(task_id),
    sqlc.arg(task_version),
    'normal',
    sqlc.arg(title),
    sqlc.arg(description),
    sqlc.arg(category),
    sqlc.arg(difficulty),
    sqlc.arg(time_limit),
    sqlc.arg(flag),
    sqlc.arg(hints),
    sqlc.narg(task_url)::TEXT,
    sqlc.narg(source_file_url)::TEXT,
    sqlc.arg(content_digest),
    sqlc.arg(created_at)
)
RETURNING id;

-- name: TransitionReplaySeriesCAS :one
UPDATE series
SET state = sqlc.arg(next_state),
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(series_id)
    AND roster_id = sqlc.arg(roster_id)
    AND revision = sqlc.arg(expected_revision)
    AND state = sqlc.arg(expected_state)
RETURNING id,
    revision,
    state;

-- name: CreateReplayReplacementWave :one
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
RETURNING id;

-- name: CreateReplayReplacementWaveMember :exec
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

-- name: CreateReplayReplacementReadinessHead :one
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
RETURNING participant_id;

-- name: CreateReplayReplacementWaveSeries :exec
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

-- name: CreateReplayReplacementGameAttempt :one
INSERT INTO game_attempts (
    id,
    slot_id,
    series_id,
    roster_id,
    attempt_number,
    state,
    revision,
    created_at,
    updated_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(slot_id),
    sqlc.arg(series_id),
    sqlc.arg(roster_id),
    sqlc.arg(attempt_number),
    'planned',
    1,
    sqlc.arg(created_at),
    sqlc.arg(created_at)
)
RETURNING id;

-- name: CreateReplayReplacementAssignment :one
INSERT INTO assignments (
    id,
    attempt_id,
    series_id,
    roster_id,
    plan_id,
    branch_id,
    reservation_id,
    snapshot_id,
    task_id,
    task_version,
    created_at,
    updated_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(attempt_id),
    sqlc.arg(series_id),
    sqlc.arg(roster_id),
    sqlc.arg(plan_id),
    sqlc.arg(branch_id),
    sqlc.arg(reservation_id),
    sqlc.arg(snapshot_id),
    sqlc.arg(task_id),
    sqlc.arg(task_version),
    sqlc.arg(created_at),
    sqlc.arg(created_at)
)
RETURNING id;

-- name: CreateReplayReplacementReadyWindow :one
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
RETURNING id;

-- name: BindReplayReplacementReadinessHeads :many
UPDATE wave_readiness
SET ready_window_id = sqlc.arg(ready_window_id),
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE wave_id = sqlc.arg(wave_id)
    AND ready_window_id IS NULL
    AND NOT ready
    AND revision = 1
RETURNING participant_id;

-- name: OpenReplayReplacementWaveCAS :one
UPDATE waves
SET state = 'ready_window_open',
    revision = revision + 1,
    updated_at = sqlc.arg(opened_at)
WHERE id = sqlc.arg(wave_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND revision = 1
    AND state = 'planned'
RETURNING id;
