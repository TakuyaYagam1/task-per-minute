-- name: LockGoldenAttempt :one
SELECT id,
    tournament_id,
    roster_id,
    attempt_number,
    previous_attempt_id,
    state,
    disclosed_at,
    ready_at,
    started_at,
    paused_at,
    completed_at,
    cancelled_at,
    cancellation_reason,
    superseded_at,
    supersession_reason,
    created_at
FROM golden_attempts
WHERE id = sqlc.arg(id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
FOR UPDATE;

-- name: CreateGoldenAttempt :one
INSERT INTO golden_attempts (
    id,
    tournament_id,
    roster_id,
    attempt_number,
    previous_attempt_id,
    state,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(attempt_number),
    sqlc.arg(previous_attempt_id),
    'prepared',
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    attempt_number,
    previous_attempt_id,
    state,
    disclosed_at,
    ready_at,
    started_at,
    paused_at,
    completed_at,
    cancelled_at,
    cancellation_reason,
    superseded_at,
    supersession_reason,
    created_at;

-- name: CreateGoldenMembership :one
INSERT INTO golden_memberships (
    id,
    attempt_id,
    tournament_id,
    roster_id,
    participant_id,
    selection_kind,
    reserve_position,
    selected_at,
    excluded_at,
    exclusion_reason,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(attempt_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(participant_id),
    sqlc.arg(selection_kind),
    sqlc.arg(reserve_position),
    sqlc.arg(selected_at),
    sqlc.arg(excluded_at),
    sqlc.narg(exclusion_reason)::TEXT,
    sqlc.arg(created_at)
)
RETURNING id,
    attempt_id,
    tournament_id,
    roster_id,
    participant_id,
    selection_kind,
    reserve_position,
    selected_at,
    ready_at,
    no_show_at,
    participation_established_at,
    excluded_at,
    exclusion_reason,
    created_at;

-- name: MarkGoldenMembershipReady :one
UPDATE golden_memberships
SET ready_at = sqlc.arg(ready_at)
WHERE id = sqlc.arg(id)
    AND attempt_id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND selection_kind IN ('direct', 'reserve')
    AND ready_at IS NULL
    AND no_show_at IS NULL
RETURNING id,
    attempt_id,
    tournament_id,
    roster_id,
    participant_id,
    selection_kind,
    reserve_position,
    selected_at,
    ready_at,
    no_show_at,
    participation_established_at,
    excluded_at,
    exclusion_reason,
    created_at;

-- name: MarkGoldenMembershipNoShow :one
UPDATE golden_memberships
SET no_show_at = sqlc.arg(no_show_at)
WHERE id = sqlc.arg(id)
    AND attempt_id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND selection_kind IN ('direct', 'reserve')
    AND ready_at IS NULL
    AND no_show_at IS NULL
RETURNING id,
    attempt_id,
    tournament_id,
    roster_id,
    participant_id,
    selection_kind,
    reserve_position,
    selected_at,
    ready_at,
    no_show_at,
    participation_established_at,
    excluded_at,
    exclusion_reason,
    created_at;

-- name: EstablishGoldenParticipation :one
UPDATE golden_memberships
SET participation_established_at = sqlc.arg(established_at)
WHERE id = sqlc.arg(id)
    AND attempt_id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND ready_at IS NOT NULL
    AND no_show_at IS NULL
    AND participation_established_at IS NULL
RETURNING id,
    attempt_id,
    tournament_id,
    roster_id,
    participant_id,
    selection_kind,
    reserve_position,
    selected_at,
    ready_at,
    no_show_at,
    participation_established_at,
    excluded_at,
    exclusion_reason,
    created_at;

-- name: CreateGoldenReservePromotion :one
INSERT INTO golden_reserve_promotions (
    id,
    attempt_id,
    tournament_id,
    roster_id,
    reserve_membership_id,
    reserve_participant_id,
    replaced_membership_id,
    replaced_participant_id,
    reason,
    promoted_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(attempt_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(reserve_membership_id),
    sqlc.arg(reserve_participant_id),
    sqlc.arg(replaced_membership_id),
    sqlc.arg(replaced_participant_id),
    sqlc.arg(reason),
    sqlc.arg(promoted_at),
    sqlc.arg(created_at)
)
RETURNING id,
    attempt_id,
    tournament_id,
    roster_id,
    reserve_membership_id,
    reserve_participant_id,
    replaced_membership_id,
    replaced_participant_id,
    reason,
    promoted_at,
    created_at;

-- name: UpdateGoldenAttemptCAS :one
UPDATE golden_attempts
SET state = sqlc.arg(next_state),
    disclosed_at = sqlc.arg(disclosed_at),
    ready_at = sqlc.arg(ready_at),
    started_at = sqlc.arg(started_at),
    paused_at = sqlc.arg(paused_at),
    completed_at = sqlc.arg(completed_at),
    cancelled_at = sqlc.arg(cancelled_at),
    cancellation_reason = sqlc.narg(cancellation_reason)::TEXT,
    superseded_at = sqlc.arg(superseded_at),
    supersession_reason = sqlc.narg(supersession_reason)::TEXT
WHERE id = sqlc.arg(id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND state = sqlc.arg(expected_state)
RETURNING id,
    tournament_id,
    roster_id,
    attempt_number,
    previous_attempt_id,
    state,
    disclosed_at,
    ready_at,
    started_at,
    paused_at,
    completed_at,
    cancelled_at,
    cancellation_reason,
    superseded_at,
    supersession_reason,
    created_at;

-- name: CreateGoldenReadyDisconnect :one
INSERT INTO golden_ready_disconnects (
    id,
    membership_id,
    attempt_id,
    tournament_id,
    roster_id,
    participant_id,
    sequence_number,
    state,
    disconnected_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(membership_id),
    sqlc.arg(attempt_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(participant_id),
    sqlc.arg(sequence_number),
    'open',
    sqlc.arg(disconnected_at),
    sqlc.arg(created_at)
)
RETURNING id,
    membership_id,
    attempt_id,
    tournament_id,
    roster_id,
    participant_id,
    sequence_number,
    state,
    disconnected_at,
    reconnected_at,
    expired_at,
    created_at;

-- name: CloseGoldenReadyDisconnectCAS :one
UPDATE golden_ready_disconnects
SET state = sqlc.arg(next_state),
    reconnected_at = sqlc.arg(reconnected_at),
    expired_at = sqlc.arg(expired_at)
WHERE id = sqlc.arg(id)
    AND attempt_id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND state = 'open'
RETURNING id,
    membership_id,
    attempt_id,
    tournament_id,
    roster_id,
    participant_id,
    sequence_number,
    state,
    disconnected_at,
    reconnected_at,
    expired_at,
    created_at;

-- name: CreateGoldenProvisionalSubmission :one
INSERT INTO golden_provisional_submissions (
    id,
    attempt_id,
    tournament_id,
    roster_id,
    membership_id,
    participant_id,
    server_sequence,
    idempotency_key,
    provisional_position,
    elapsed_milliseconds,
    status,
    rejection_reason,
    payload_digest,
    submitted_at,
    received_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(attempt_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(membership_id),
    sqlc.arg(participant_id),
    sqlc.arg(server_sequence),
    sqlc.arg(idempotency_key),
    sqlc.arg(provisional_position),
    sqlc.arg(elapsed_milliseconds),
    sqlc.arg(status),
    sqlc.narg(rejection_reason)::TEXT,
    sqlc.arg(payload_digest),
    sqlc.arg(submitted_at),
    sqlc.arg(received_at),
    sqlc.arg(created_at)
)
RETURNING id,
    attempt_id,
    tournament_id,
    roster_id,
    membership_id,
    participant_id,
    server_sequence,
    idempotency_key,
    provisional_position,
    elapsed_milliseconds,
    status,
    rejection_reason,
    payload_digest,
    submitted_at,
    received_at,
    created_at;

-- name: GetGoldenSubmissionByIdempotencyKey :one
SELECT id,
    attempt_id,
    tournament_id,
    roster_id,
    membership_id,
    participant_id,
    server_sequence,
    idempotency_key,
    provisional_position,
    elapsed_milliseconds,
    status,
    rejection_reason,
    payload_digest,
    submitted_at,
    received_at,
    created_at
FROM golden_provisional_submissions
WHERE idempotency_key = sqlc.arg(idempotency_key);

-- name: NextGoldenAttemptSubmissionRevision :one
SELECT COALESCE(latest.revision_number + 1, 1)::bigint AS revision_number,
    latest.revision_id AS previous_revision_id
FROM (VALUES (sqlc.arg(attempt_id)::uuid)) AS scope(id)
LEFT JOIN LATERAL (
    SELECT revision_id, revision_number
    FROM golden_attempt_submission_revisions
    WHERE attempt_id = scope.id
    ORDER BY revision_number DESC
    LIMIT 1
) AS latest ON true;

-- name: CreateGoldenPositionCommit :one
INSERT INTO golden_position_commits (
    id,
    attempt_id,
    tournament_id,
    roster_id,
    membership_id,
    participant_id,
    provisional_submission_id,
    previous_position_commit_id,
    position,
    committed_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(attempt_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(membership_id),
    sqlc.arg(participant_id),
    sqlc.arg(provisional_submission_id),
    sqlc.arg(previous_position_commit_id),
    sqlc.arg(position),
    sqlc.arg(committed_at),
    sqlc.arg(created_at)
)
RETURNING id,
    attempt_id,
    tournament_id,
    roster_id,
    membership_id,
    participant_id,
    provisional_submission_id,
    previous_position_commit_id,
    position,
    committed_at,
    created_at;

-- name: CreateGoldenRecoveryRevision :one
INSERT INTO golden_recovery_revisions (
    id,
    attempt_id,
    tournament_id,
    roster_id,
    revision_number,
    previous_revision_id,
    state,
    recovery_evidence,
    recorded_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(attempt_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(revision_number),
    sqlc.arg(previous_revision_id),
    sqlc.arg(state),
    sqlc.arg(recovery_evidence),
    sqlc.arg(recorded_at),
    sqlc.arg(created_at)
)
RETURNING id,
    attempt_id,
    tournament_id,
    roster_id,
    revision_number,
    previous_revision_id,
    state,
    recovery_evidence,
    recorded_at,
    created_at;

-- name: GetLatestGoldenRecoveryRevision :one
SELECT id,
    attempt_id,
    tournament_id,
    roster_id,
    revision_number,
    previous_revision_id,
    state,
    recovery_evidence,
    recorded_at,
    created_at
FROM golden_recovery_revisions
WHERE attempt_id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY revision_number DESC
LIMIT 1;

-- name: GetGoldenAttemptScoped :one
SELECT id,
    tournament_id,
    roster_id,
    attempt_number,
    previous_attempt_id,
    state,
    disclosed_at,
    ready_at,
    started_at,
    paused_at,
    completed_at,
    cancelled_at,
    cancellation_reason,
    superseded_at,
    supersession_reason,
    created_at
FROM golden_attempts
WHERE id = sqlc.arg(id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id);

-- name: ListGoldenAttempts :many
SELECT id,
    tournament_id,
    roster_id,
    attempt_number,
    previous_attempt_id,
    state,
    disclosed_at,
    ready_at,
    started_at,
    paused_at,
    completed_at,
    cancelled_at,
    cancellation_reason,
    superseded_at,
    supersession_reason,
    created_at
FROM golden_attempts
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY attempt_number;

-- name: ListGoldenMemberships :many
SELECT id,
    attempt_id,
    tournament_id,
    roster_id,
    participant_id,
    selection_kind,
    reserve_position,
    selected_at,
    ready_at,
    no_show_at,
    participation_established_at,
    excluded_at,
    exclusion_reason,
    created_at
FROM golden_memberships
WHERE attempt_id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY selection_kind, reserve_position, participant_id;

-- name: ListGoldenReadyDisconnects :many
SELECT id,
    membership_id,
    attempt_id,
    tournament_id,
    roster_id,
    participant_id,
    sequence_number,
    state,
    disconnected_at,
    reconnected_at,
    expired_at,
    created_at
FROM golden_ready_disconnects
WHERE attempt_id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY membership_id, sequence_number;

-- name: ListGoldenProvisionalSubmissions :many
SELECT id,
    attempt_id,
    tournament_id,
    roster_id,
    membership_id,
    participant_id,
    server_sequence,
    idempotency_key,
    provisional_position,
    elapsed_milliseconds,
    status,
    rejection_reason,
    payload_digest,
    submitted_at,
    received_at,
    created_at
FROM golden_provisional_submissions
WHERE attempt_id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY server_sequence;

-- name: ListGoldenPositionCommits :many
SELECT id,
    attempt_id,
    tournament_id,
    roster_id,
    membership_id,
    participant_id,
    provisional_submission_id,
    previous_position_commit_id,
    position,
    committed_at,
    created_at
FROM golden_position_commits
WHERE attempt_id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY position;

-- name: ListGoldenReservePromotions :many
SELECT id,
    attempt_id,
    tournament_id,
    roster_id,
    reserve_membership_id,
    reserve_participant_id,
    replaced_membership_id,
    replaced_participant_id,
    reason,
    promoted_at,
    created_at
FROM golden_reserve_promotions
WHERE attempt_id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY promoted_at, id;

-- name: ListGoldenRecoveryRevisions :many
SELECT id,
    attempt_id,
    tournament_id,
    roster_id,
    revision_number,
    previous_revision_id,
    state,
    recovery_evidence,
    recorded_at,
    created_at
FROM golden_recovery_revisions
WHERE attempt_id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY revision_number;

-- The following writes form one immutable Golden authority snapshot. Callers
-- must execute the root and all child writes inside the same outer transaction.

-- name: CreateGoldenExactPlanSnapshot :one
INSERT INTO golden_exact_plan_snapshots (
    plan_id,
    plan_revision_id,
    tournament_id,
    roster_id,
    plan_set_id,
    source_projection_revision_id,
    source_projection_revision,
    source_projection_previous_revision_id,
    source_standings_artifact_id,
    source_standings_payload_digest,
    group_set_revision_id,
    group_set_revision,
    pool_revision_id,
    pool_revision,
    history_revision_id,
    history_revision,
    task_health_revision_id,
    task_health_revision,
    artifact_revision_id,
    artifact_revision,
    reservation_revision_id,
    reservation_revision,
    membership_revision_id,
    membership_revision,
    source_payload_digest,
    group_digest,
    pool_digest,
    history_digest,
    task_health_digest,
    artifact_digest,
    reservation_digest,
    membership_digest,
    proof_hash,
    created_at
)
VALUES (
    sqlc.arg(plan_id),
    sqlc.arg(plan_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(plan_set_id),
    sqlc.arg(source_projection_revision_id),
    sqlc.arg(source_projection_revision),
    sqlc.narg(source_projection_previous_revision_id),
    sqlc.arg(source_standings_artifact_id),
    sqlc.arg(source_standings_payload_digest),
    sqlc.arg(group_set_revision_id),
    sqlc.arg(group_set_revision),
    sqlc.arg(pool_revision_id),
    sqlc.arg(pool_revision),
    sqlc.arg(history_revision_id),
    sqlc.arg(history_revision),
    sqlc.arg(task_health_revision_id),
    sqlc.arg(task_health_revision),
    sqlc.arg(artifact_revision_id),
    sqlc.arg(artifact_revision),
    sqlc.arg(reservation_revision_id),
    sqlc.arg(reservation_revision),
    sqlc.arg(membership_revision_id),
    sqlc.arg(membership_revision),
    sqlc.arg(source_payload_digest),
    sqlc.arg(group_digest),
    sqlc.arg(pool_digest),
    sqlc.arg(history_digest),
    sqlc.arg(task_health_digest),
    sqlc.arg(artifact_digest),
    sqlc.arg(reservation_digest),
    sqlc.arg(membership_digest),
    sqlc.arg(proof_hash),
    sqlc.arg(created_at)
)
RETURNING plan_id;

-- name: CreateGoldenExactPlanSnapshotGroup :one
INSERT INTO golden_exact_plan_snapshot_groups (
    plan_id,
    tournament_id,
    roster_id,
    group_id,
    group_revision_id,
    source_projection_revision_id,
    source_projection_revision,
    position_from,
    position_to,
    group_ordinal,
    definition_digest,
    created_at
)
VALUES (
    sqlc.arg(plan_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(group_id),
    sqlc.arg(group_revision_id),
    sqlc.arg(source_projection_revision_id),
    sqlc.arg(source_projection_revision),
    sqlc.arg(position_from),
    sqlc.arg(position_to),
    sqlc.arg(group_ordinal),
    sqlc.arg(definition_digest),
    sqlc.arg(created_at)
)
RETURNING group_revision_id;

-- name: CreateGoldenExactPlanSnapshotMember :one
INSERT INTO golden_exact_plan_snapshot_members (
    plan_id,
    group_revision_id,
    tournament_id,
    roster_id,
    participant_id,
    position,
    created_at
)
VALUES (
    sqlc.arg(plan_id),
    sqlc.arg(group_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(participant_id),
    sqlc.arg(position),
    sqlc.arg(created_at)
)
RETURNING participant_id;

-- name: CreateGoldenExactPlanSnapshotEdge :one
INSERT INTO golden_exact_plan_snapshot_edges (
    plan_id,
    group_revision_id,
    tournament_id,
    roster_id,
    edge_id,
    reservation_id,
    snapshot_id,
    task_id,
    task_version,
    position,
    content_digest,
    created_at
)
VALUES (
    sqlc.arg(plan_id),
    sqlc.arg(group_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(edge_id),
    sqlc.arg(reservation_id),
    sqlc.arg(snapshot_id),
    sqlc.arg(task_id),
    sqlc.arg(task_version),
    sqlc.arg(position),
    sqlc.arg(content_digest),
    sqlc.arg(created_at)
)
RETURNING edge_id;

-- name: CreateGoldenExactPlanSnapshotCandidate :one
INSERT INTO golden_exact_plan_snapshot_candidates (
    plan_id,
    tournament_id,
    roster_id,
    pool_revision_id,
    task_id,
    task_version,
    exists_in_source,
    enabled,
    healthy,
    mutation_locked,
    publicly_exposed,
    artifact_digest,
    created_at
)
VALUES (
    sqlc.arg(plan_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(pool_revision_id),
    sqlc.arg(task_id),
    sqlc.arg(task_version),
    sqlc.arg(exists_in_source),
    sqlc.arg(enabled),
    sqlc.arg(healthy),
    sqlc.arg(mutation_locked),
    sqlc.arg(publicly_exposed),
    sqlc.arg(artifact_digest),
    sqlc.arg(created_at)
)
RETURNING task_id;

-- name: CreateGoldenExactPlanSnapshotHistory :one
INSERT INTO golden_exact_plan_snapshot_history (
    plan_id,
    tournament_id,
    roster_id,
    participant_id,
    task_id,
    task_version,
    created_at
)
VALUES (
    sqlc.arg(plan_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(participant_id),
    sqlc.arg(task_id),
    sqlc.arg(task_version),
    sqlc.arg(created_at)
)
RETURNING participant_id;

-- name: CreateGoldenExactPlanSnapshotParticipantReservation :one
INSERT INTO golden_exact_plan_snapshot_participant_reservations (
    plan_id,
    tournament_id,
    roster_id,
    participant_id,
    player_id,
    reservation_id,
    revision,
    acquired_at,
    updated_at,
    created_at
)
VALUES (
    sqlc.arg(plan_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(participant_id),
    sqlc.arg(player_id),
    sqlc.arg(reservation_id),
    sqlc.arg(revision),
    sqlc.arg(acquired_at),
    sqlc.arg(updated_at),
    sqlc.arg(created_at)
)
RETURNING participant_id;

-- name: CreateGoldenExactPlanSnapshotReservation :one
INSERT INTO golden_exact_plan_snapshot_reservations (
    plan_id,
    tournament_id,
    roster_id,
    task_id,
    task_version,
    reservation_id,
    owner_plan_id,
    owner_plan_revision_id,
    created_at
)
VALUES (
    sqlc.arg(plan_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(task_id),
    sqlc.arg(task_version),
    sqlc.arg(reservation_id),
    sqlc.arg(owner_plan_id),
    sqlc.arg(owner_plan_revision_id),
    sqlc.arg(created_at)
)
RETURNING reservation_id;

-- name: SealGoldenExactPlanSnapshot :one
INSERT INTO golden_exact_plan_snapshot_seals (
    plan_id,
    tournament_id,
    roster_id,
    proof_hash,
    sealed_at
)
VALUES (
    sqlc.arg(plan_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(proof_hash),
    sqlc.arg(sealed_at)
)
RETURNING plan_id;

-- name: CreateGoldenStateRevision :one
INSERT INTO golden_state_revisions (
    revision_id,
    tournament_id,
    roster_id,
    group_id,
    group_revision_id,
    revision_number,
    previous_revision_id,
    plan_id,
    membership_revision_id,
    membership_revision,
    membership_previous_revision_id,
    membership_digest,
    payload_digest,
    created_at
)
VALUES (
    sqlc.arg(revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(group_id),
    sqlc.arg(group_revision_id),
    sqlc.arg(revision_number),
    sqlc.narg(previous_revision_id),
    sqlc.arg(plan_id),
    sqlc.arg(membership_revision_id),
    sqlc.arg(membership_revision),
    sqlc.narg(membership_previous_revision_id),
    sqlc.arg(membership_digest),
    sqlc.arg(payload_digest),
    sqlc.arg(created_at)
)
RETURNING revision_id;

-- name: CreateGoldenStateTransition :one
INSERT INTO golden_state_transitions (
    state_revision_id,
    tournament_id,
    roster_id,
    group_id,
    group_revision_id,
    transition_kind,
    command_id,
    previous_state_revision_id,
    occurred_at,
    created_at
)
VALUES (
    sqlc.arg(state_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(group_id),
    sqlc.arg(group_revision_id),
    sqlc.arg(transition_kind),
    sqlc.narg(command_id),
    sqlc.narg(previous_state_revision_id),
    sqlc.arg(occurred_at),
    sqlc.arg(created_at)
)
RETURNING state_revision_id;

-- name: CreateGoldenStateMember :one
INSERT INTO golden_state_members (
    state_revision_id,
    tournament_id,
    roster_id,
    participant_id,
    excluded,
    position,
    created_at
)
VALUES (
    sqlc.arg(state_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(participant_id),
    sqlc.arg(excluded),
    sqlc.arg(position),
    sqlc.arg(created_at)
)
RETURNING participant_id;

-- name: CreateGoldenStateAttempt :one
INSERT INTO golden_state_attempts (
    state_revision_id,
    tournament_id,
    roster_id,
    group_revision_id,
    attempt_id,
    attempt_number,
    previous_attempt_id,
    state,
    retained_at,
    started_at,
    finished_at,
    created_at
)
VALUES (
    sqlc.arg(state_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(group_revision_id),
    sqlc.arg(attempt_id),
    sqlc.arg(attempt_number),
    sqlc.narg(previous_attempt_id),
    sqlc.arg(state),
    sqlc.narg(retained_at),
    sqlc.narg(started_at),
    sqlc.narg(finished_at),
    sqlc.arg(created_at)
)
RETURNING attempt_id;

-- name: CreateGoldenStateAttemptMember :one
INSERT INTO golden_state_attempt_members (
    state_revision_id,
    attempt_id,
    tournament_id,
    roster_id,
    participant_id,
    position,
    created_at
)
VALUES (
    sqlc.arg(state_revision_id),
    sqlc.arg(attempt_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(participant_id),
    sqlc.arg(position),
    sqlc.arg(created_at)
)
RETURNING participant_id;

-- name: CreateGoldenStateReadyWindow :one
INSERT INTO golden_state_ready_windows (
    state_revision_id,
    tournament_id,
    roster_id,
    window_id,
    window_revision_id,
    window_revision,
    window_previous_revision_id,
    attempt_id,
    attempt_number,
    opened_at,
    deadline,
    state,
    readiness_revision_id,
    readiness_revision,
    readiness_previous_revision_id,
    readiness_digest,
    presence_revision_id,
    presence_revision,
    presence_previous_revision_id,
    presence_digest,
    created_at
)
VALUES (
    sqlc.arg(state_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(window_id),
    sqlc.arg(window_revision_id),
    sqlc.arg(window_revision),
    sqlc.narg(window_previous_revision_id),
    sqlc.arg(attempt_id),
    sqlc.arg(attempt_number),
    sqlc.arg(opened_at),
    sqlc.arg(deadline),
    sqlc.arg(state),
    sqlc.arg(readiness_revision_id),
    sqlc.arg(readiness_revision),
    sqlc.narg(readiness_previous_revision_id),
    sqlc.arg(readiness_digest),
    sqlc.arg(presence_revision_id),
    sqlc.arg(presence_revision),
    sqlc.narg(presence_previous_revision_id),
    sqlc.arg(presence_digest),
    sqlc.arg(created_at)
)
RETURNING window_id;

-- name: CreateGoldenStateReadyWindowParticipant :one
INSERT INTO golden_state_ready_window_participants (
    state_revision_id,
    window_id,
    tournament_id,
    roster_id,
    participant_id,
    membership_kind,
    position,
    created_at
)
VALUES (
    sqlc.arg(state_revision_id),
    sqlc.arg(window_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(participant_id),
    sqlc.arg(membership_kind),
    sqlc.arg(position),
    sqlc.arg(created_at)
)
RETURNING participant_id;

-- name: CreateGoldenStateReadyEvent :one
INSERT INTO golden_state_ready_events (
    command_id,
    state_revision_id,
    tournament_id,
    roster_id,
    group_id,
    group_revision_id,
    participant_id,
    event_type,
    attempt_id,
    window_id,
    expected_state_revision_id,
    expected_state_revision,
    expected_state_payload_digest,
    expected_window_revision_id,
    expected_window_revision,
    expected_readiness_revision_id,
    expected_readiness_revision,
    expected_readiness_digest,
    expected_presence_revision_id,
    expected_presence_revision,
    expected_presence_digest,
    command_digest,
    result_window_revision_id,
    result_readiness_revision_id,
    result_presence_revision_id,
    occurred_at,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(state_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(group_id),
    sqlc.arg(group_revision_id),
    sqlc.arg(participant_id),
    sqlc.arg(event_type),
    sqlc.arg(attempt_id),
    sqlc.arg(window_id),
    sqlc.arg(expected_state_revision_id),
    sqlc.arg(expected_state_revision),
    sqlc.arg(expected_state_payload_digest),
    sqlc.arg(expected_window_revision_id),
    sqlc.arg(expected_window_revision),
    sqlc.arg(expected_readiness_revision_id),
    sqlc.arg(expected_readiness_revision),
    sqlc.arg(expected_readiness_digest),
    sqlc.arg(expected_presence_revision_id),
    sqlc.arg(expected_presence_revision),
    sqlc.arg(expected_presence_digest),
    sqlc.arg(command_digest),
    sqlc.arg(result_window_revision_id),
    sqlc.arg(result_readiness_revision_id),
    sqlc.arg(result_presence_revision_id),
    sqlc.arg(occurred_at),
    sqlc.arg(created_at)
)
RETURNING command_id;

-- name: CreateGoldenStateNoShowResolution :one
INSERT INTO golden_state_no_show_resolutions (
    command_id,
    state_revision_id,
    tournament_id,
    roster_id,
    group_id,
    group_revision_id,
    attempt_id,
    attempt_number,
    window_id,
    expected_state_revision_id,
    expected_state_revision,
    expected_state_payload_digest,
    expected_window_revision_id,
    expected_window_revision,
    expected_readiness_revision_id,
    expected_readiness_revision,
    expected_readiness_digest,
    expected_presence_revision_id,
    expected_presence_revision,
    expected_presence_digest,
    result_window_revision_id,
    result_membership_revision_id,
    deadline,
    resolved_at,
    readiness_revision_id,
    readiness_revision,
    readiness_digest,
    presence_revision_id,
    presence_revision,
    presence_digest,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(state_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(group_id),
    sqlc.arg(group_revision_id),
    sqlc.arg(attempt_id),
    sqlc.arg(attempt_number),
    sqlc.arg(window_id),
    sqlc.arg(expected_state_revision_id),
    sqlc.arg(expected_state_revision),
    sqlc.arg(expected_state_payload_digest),
    sqlc.arg(expected_window_revision_id),
    sqlc.arg(expected_window_revision),
    sqlc.arg(expected_readiness_revision_id),
    sqlc.arg(expected_readiness_revision),
    sqlc.arg(expected_readiness_digest),
    sqlc.arg(expected_presence_revision_id),
    sqlc.arg(expected_presence_revision),
    sqlc.arg(expected_presence_digest),
    sqlc.arg(result_window_revision_id),
    sqlc.arg(result_membership_revision_id),
    sqlc.arg(deadline),
    sqlc.arg(resolved_at),
    sqlc.arg(readiness_revision_id),
    sqlc.arg(readiness_revision),
    sqlc.arg(readiness_digest),
    sqlc.arg(presence_revision_id),
    sqlc.arg(presence_revision),
    sqlc.arg(presence_digest),
    sqlc.arg(created_at)
)
RETURNING command_id;

-- name: CreateGoldenStateNoShowParticipant :one
INSERT INTO golden_state_no_show_participants (
    state_revision_id,
    command_id,
    tournament_id,
    roster_id,
    participant_id,
    membership_kind,
    position,
    created_at
)
VALUES (
    sqlc.arg(state_revision_id),
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(participant_id),
    sqlc.arg(membership_kind),
    sqlc.arg(position),
    sqlc.arg(created_at)
)
RETURNING participant_id;

-- name: CreateGoldenStateAllocation :one
INSERT INTO golden_state_allocations (
    allocation_id,
    command_id,
    state_revision_id,
    tournament_id,
    roster_id,
    group_id,
    group_revision_id,
    expected_state_revision_id,
    expected_state_revision,
    expected_state_payload_digest,
    allocated_at,
    payload_digest,
    created_at
)
VALUES (
    sqlc.arg(allocation_id),
    sqlc.arg(command_id),
    sqlc.arg(state_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(group_id),
    sqlc.arg(group_revision_id),
    sqlc.arg(expected_state_revision_id),
    sqlc.arg(expected_state_revision),
    sqlc.arg(expected_state_payload_digest),
    sqlc.arg(allocated_at),
    sqlc.arg(payload_digest),
    sqlc.arg(created_at)
)
RETURNING allocation_id;

-- name: CreateGoldenStateAllocationInput :one
INSERT INTO golden_state_allocation_inputs (
    allocation_id,
    state_revision_id,
    tournament_id,
    roster_id,
    participant_id,
    points,
    buchholz,
    head_to_head_points,
    head_to_head_applied,
    effective_time_milliseconds,
    accepted_solve_time_milliseconds,
    stable_seed,
    position,
    created_at
)
VALUES (
    sqlc.arg(allocation_id),
    sqlc.arg(state_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(participant_id),
    sqlc.arg(points),
    sqlc.arg(buchholz),
    sqlc.arg(head_to_head_points),
    sqlc.arg(head_to_head_applied),
    sqlc.arg(effective_time_milliseconds),
    sqlc.narg(accepted_solve_time_milliseconds),
    sqlc.arg(stable_seed),
    sqlc.arg(position),
    sqlc.arg(created_at)
)
RETURNING participant_id;

-- name: CreateGoldenStateAllocationPosition :one
INSERT INTO golden_state_allocation_positions (
    allocation_id,
    state_revision_id,
    tournament_id,
    roster_id,
    position,
    participant_id,
    position_kind,
    created_at
)
VALUES (
    sqlc.arg(allocation_id),
    sqlc.arg(state_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(position),
    sqlc.arg(participant_id),
    sqlc.arg(position_kind),
    sqlc.arg(created_at)
)
RETURNING position;

-- name: SealGoldenStateRevision :one
INSERT INTO golden_state_revision_seals (
    state_revision_id,
    tournament_id,
    roster_id,
    payload_digest,
    sealed_at
)
VALUES (
    sqlc.arg(state_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(payload_digest),
    sqlc.arg(sealed_at)
)
RETURNING state_revision_id;

-- name: CreateGoldenAttemptAuthority :one
INSERT INTO golden_attempt_authorities (
    attempt_id,
    tournament_id,
    roster_id,
    group_revision_id,
    wave_id,
    assignment_id,
    snapshot_id,
    task_id,
    task_version,
    source_digest,
    created_at
)
VALUES (
    sqlc.arg(attempt_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(group_revision_id),
    sqlc.arg(wave_id),
    sqlc.arg(assignment_id),
    sqlc.arg(snapshot_id),
    sqlc.arg(task_id),
    sqlc.arg(task_version),
    sqlc.arg(source_digest),
    sqlc.arg(created_at)
)
RETURNING attempt_id;

-- name: CreateGoldenAttemptSubmissionRevision :one
INSERT INTO golden_attempt_submission_revisions (
    revision_id,
    attempt_id,
    tournament_id,
    roster_id,
    membership_id,
    participant_id,
    revision_number,
    previous_revision_id,
    provisional_submission_id,
    payload_digest,
    committed_at,
    created_at
)
VALUES (
    sqlc.arg(revision_id),
    sqlc.arg(attempt_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(membership_id),
    sqlc.arg(participant_id),
    sqlc.arg(revision_number),
    sqlc.narg(previous_revision_id),
    sqlc.arg(provisional_submission_id),
    sqlc.arg(payload_digest),
    sqlc.arg(committed_at),
    sqlc.arg(created_at)
)
RETURNING revision_id;

-- name: CreateGoldenPositionLedgerRevision :one
INSERT INTO golden_position_ledger_revisions (
    revision_id,
    tournament_id,
    roster_id,
    group_revision_id,
    revision_number,
    previous_revision_id,
    payload_digest,
    finalized_at,
    created_at
)
VALUES (
    sqlc.arg(revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(group_revision_id),
    sqlc.arg(revision_number),
    sqlc.narg(previous_revision_id),
    sqlc.arg(payload_digest),
    sqlc.arg(finalized_at),
    sqlc.arg(created_at)
)
RETURNING revision_id;

-- name: CreateGoldenPositionLedgerAttempt :one
INSERT INTO golden_position_ledger_attempts (
    ledger_revision_id,
    attempt_id,
    submission_revision_id,
    tournament_id,
    roster_id,
    group_revision_id,
    attempt_number,
    order_count,
    created_at
)
VALUES (
    sqlc.arg(ledger_revision_id),
    sqlc.arg(attempt_id),
    sqlc.arg(submission_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(group_revision_id),
    sqlc.arg(attempt_number),
    sqlc.arg(order_count),
    sqlc.arg(created_at)
)
RETURNING attempt_id;

-- name: CreateGoldenPositionLedgerCommitBinding :one
INSERT INTO golden_position_ledger_commit_bindings (
    ledger_revision_id,
    attempt_id,
    position_commit_id,
    tournament_id,
    roster_id,
    participant_id,
    position,
    evidence_digest,
    created_at
)
VALUES (
    sqlc.arg(ledger_revision_id),
    sqlc.arg(attempt_id),
    sqlc.arg(position_commit_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(participant_id),
    sqlc.arg(position),
    sqlc.arg(evidence_digest),
    sqlc.arg(created_at)
)
RETURNING position_commit_id;

-- name: SealGoldenPositionLedgerRevision :one
INSERT INTO golden_position_ledger_revision_seals (
    ledger_revision_id,
    tournament_id,
    roster_id,
    payload_digest,
    sealed_at
)
VALUES (
    sqlc.arg(ledger_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(payload_digest),
    sqlc.arg(sealed_at)
)
RETURNING ledger_revision_id;

-- Production Golden runtime.

-- Runtime fencing and durable command evidence.

-- name: LockGoldenRuntimeHead :one
SELECT tournament_id,
    roster_id,
    revision,
    source_projection_revision_id,
    source_projection_revision,
    updated_at
FROM golden_runtime_heads
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
FOR UPDATE;

-- name: CreateGoldenRuntimeHead :one
INSERT INTO golden_runtime_heads (
    tournament_id,
    roster_id,
    revision,
    source_projection_revision_id,
    source_projection_revision,
    updated_at
)
VALUES (
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(revision),
    sqlc.arg(source_projection_revision_id),
    sqlc.arg(source_projection_revision),
    sqlc.arg(updated_at)
)
RETURNING tournament_id,
    roster_id,
    revision,
    source_projection_revision_id,
    source_projection_revision,
    updated_at;

-- name: AdvanceGoldenRuntimeHead :one
UPDATE golden_runtime_heads
SET revision = sqlc.arg(next_revision),
    updated_at = sqlc.arg(updated_at)
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND revision = sqlc.arg(expected_revision)
RETURNING tournament_id,
    roster_id,
    revision,
    source_projection_revision_id,
    source_projection_revision,
    updated_at;

-- name: GetGoldenRuntimeCommand :one
SELECT command_id,
    tournament_id,
    roster_id,
    actor_kind,
    actor_id,
    command_scope,
    command_kind,
    attempt_id,
    participant_id,
    expected_runtime_revision,
    expected_ready_window_id,
    command_digest,
    resulting_runtime_revision,
    result_kind,
    result_payload,
    occurred_at,
    created_at
FROM golden_runtime_commands
WHERE command_id = sqlc.arg(command_id);

-- name: CreateGoldenRuntimeCommand :one
INSERT INTO golden_runtime_commands (
    command_id,
    tournament_id,
    roster_id,
    actor_kind,
    actor_id,
    command_scope,
    command_kind,
    attempt_id,
    participant_id,
    expected_runtime_revision,
    expected_ready_window_id,
    command_digest,
    resulting_runtime_revision,
    result_kind,
    result_payload,
    occurred_at,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(actor_kind),
    sqlc.narg(actor_id),
    sqlc.arg(command_scope),
    sqlc.arg(command_kind),
    sqlc.narg(attempt_id),
    sqlc.narg(participant_id),
    sqlc.arg(expected_runtime_revision),
    sqlc.narg(expected_ready_window_id),
    sqlc.arg(command_digest),
    sqlc.arg(resulting_runtime_revision),
    sqlc.arg(result_kind),
    sqlc.arg(result_payload),
    sqlc.arg(occurred_at),
    sqlc.arg(created_at)
)
RETURNING command_id,
    tournament_id,
    roster_id,
    actor_kind,
    actor_id,
    command_scope,
    command_kind,
    attempt_id,
    participant_id,
    expected_runtime_revision,
    expected_ready_window_id,
    command_digest,
    resulting_runtime_revision,
    result_kind,
    result_payload,
    occurred_at,
    created_at;

-- name: CreateGoldenRuntimeAuditEvent :one
INSERT INTO audit_events (
    id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    actor_kind,
    actor_id,
    action,
    payload,
    occurred_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    NULL,
    NULL,
    sqlc.arg(actor_kind),
    sqlc.narg(actor_id),
    sqlc.arg(action),
    sqlc.arg(payload),
    sqlc.arg(occurred_at),
    sqlc.arg(created_at)
)
RETURNING id;

-- name: AllocateGoldenRuntimeOutboxSequence :one
INSERT INTO tournament_outbox_cursors (tournament_id, next_sequence, updated_at)
VALUES (sqlc.arg(tournament_id), 2, sqlc.arg(updated_at))
ON CONFLICT (tournament_id) DO UPDATE
SET next_sequence = tournament_outbox_cursors.next_sequence + 1,
    updated_at = EXCLUDED.updated_at
RETURNING next_sequence - 1 AS sequence;

-- name: AllocateGoldenRuntimeProjectionOrdinal :one
INSERT INTO projection_outbox_cursors (
    projection_revision_id,
    tournament_id,
    roster_id,
    next_ordinal,
    updated_at
)
VALUES (
    sqlc.arg(projection_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    2,
    sqlc.arg(updated_at)
)
ON CONFLICT (projection_revision_id) DO UPDATE
SET next_ordinal = projection_outbox_cursors.next_ordinal + 1,
    updated_at = EXCLUDED.updated_at
WHERE projection_outbox_cursors.tournament_id = EXCLUDED.tournament_id
    AND projection_outbox_cursors.roster_id = EXCLUDED.roster_id
    AND projection_outbox_cursors.next_ordinal < 32768
RETURNING next_ordinal - 1 AS projection_ordinal;

-- name: CreateGoldenRuntimeOutboxEvent :one
INSERT INTO outbox_events (
    id,
    tournament_id,
    roster_id,
    projection_revision_id,
    projection_revision,
    sequence,
    projection_ordinal,
    terminal,
    idempotency_key,
    audience,
    principal_id,
    topic,
    payload,
    created_at,
    available_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(projection_revision_id),
    sqlc.arg(projection_revision),
    sqlc.arg(sequence),
    sqlc.arg(projection_ordinal),
    false,
    sqlc.arg(command_id),
    'all',
    NULL,
    'golden.runtime',
    sqlc.arg(payload),
    sqlc.arg(created_at),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    projection_revision_id,
    projection_revision,
    sequence,
    projection_ordinal,
    terminal,
    idempotency_key,
    audience,
    principal_id,
    topic,
    payload,
    created_at,
    available_at;

-- name: CreateGoldenRuntimeOutboxSource :one
INSERT INTO outbox_golden_runtime_sources (
    outbox_event_id,
    tournament_id,
    roster_id,
    command_id,
    runtime_revision,
    projection_revision_id,
    projection_revision,
    projection_ordinal,
    created_at
)
VALUES (
    sqlc.arg(outbox_event_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(command_id),
    sqlc.arg(runtime_revision),
    sqlc.arg(projection_revision_id),
    sqlc.arg(projection_revision),
    sqlc.arg(projection_ordinal),
    sqlc.arg(created_at)
)
RETURNING outbox_event_id;

-- name: LockGoldenRuntimeTournament :one
SELECT id, state
FROM tournaments
WHERE id = sqlc.arg(tournament_id)
FOR UPDATE;

-- name: NextGoldenRuntimeAttempt :one
SELECT COALESCE(latest.attempt_number + 1, 1)::integer AS attempt_number,
    latest.id AS previous_attempt_id
FROM (VALUES (sqlc.arg(tournament_id)::uuid)) AS scope(id)
LEFT JOIN LATERAL (
    SELECT attempt.id, attempt.attempt_number
    FROM golden_attempts AS attempt
    WHERE attempt.tournament_id = scope.id
    ORDER BY attempt.attempt_number DESC
    LIMIT 1
) AS latest ON true;

-- name: ListGoldenRuntimeGroups :many
SELECT group_revision.revision_id AS group_revision_id,
    group_revision.group_id,
    group_revision.tournament_id,
    group_revision.roster_id,
    group_revision.definition_digest,
    group_revision.position_from,
    group_revision.position_to,
    group_revision.source_projection_revision,
    group_revision.source_projection_revision_id,
    member.participant_id,
    member.standing_position
FROM golden_group_revisions AS group_revision
INNER JOIN tournament_stage_tie_group_members AS member
    ON member.command_id = group_revision.stage_progression_command_id
    AND member.tournament_id = group_revision.tournament_id
    AND member.roster_id = group_revision.roster_id
    AND member.group_id = group_revision.group_id
WHERE group_revision.tournament_id = sqlc.arg(tournament_id)
ORDER BY group_revision.position_from, member.standing_position;

-- name: LoadGoldenRuntimePlanRoster :one
SELECT roster.id AS roster_id,
    roster.revision AS roster_revision
FROM rosters AS roster
WHERE roster.tournament_id = sqlc.arg(tournament_id)
    AND roster.locked_at IS NOT NULL
FOR KEY SHARE OF roster;

-- name: CreateGoldenRuntimeAssignmentPlan :exec
INSERT INTO assignment_plans (
    id,
    tournament_id,
    roster_id,
    kind,
    revision_id,
    source_roster_revision,
    source_pool_revision_id,
    reachable_branch_count,
    constraint_graph,
    proof_evidence,
    proof_hash,
    decision_evidence_id,
    decision_algorithm_version,
    decision_inputs,
    decision_seed,
    decision_result,
    decision_replay_digest,
    decision_owner_id,
    decided_at,
    state,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    'exact_golden',
    sqlc.arg(revision_id),
    sqlc.arg(source_roster_revision),
    sqlc.arg(source_pool_revision_id),
    sqlc.arg(reachable_branch_count),
    sqlc.arg(constraint_graph),
    sqlc.arg(proof_evidence),
    sqlc.arg(proof_hash),
    sqlc.arg(decision_evidence_id),
    sqlc.arg(decision_algorithm_version),
    sqlc.arg(decision_inputs),
    sqlc.arg(decision_seed),
    sqlc.arg(decision_result),
    sqlc.arg(decision_replay_digest),
    sqlc.arg(decision_owner_id),
    sqlc.arg(decided_at),
    'planned',
    sqlc.arg(created_at)
);

-- name: CreateGoldenRuntimeAssignmentBranch :exec
INSERT INTO assignment_branches (
    id,
    plan_id,
    draft_id,
    draft_revision_id,
    branch_key,
    category_sequence,
    state,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(plan_id),
    NULL,
    NULL,
    sqlc.arg(branch_key),
    sqlc.arg(category_sequence),
    'reserved',
    sqlc.arg(created_at)
);

-- name: ListGoldenRuntimePlanParticipantReservations :many
SELECT member.participant_id,
    participant.player_id,
    reservation.reservation_id,
    reservation.revision,
    reservation.acquired_at,
    reservation.updated_at
FROM golden_group_revisions AS group_revision
INNER JOIN tournament_stage_tie_group_members AS member
    ON member.command_id = group_revision.stage_progression_command_id
    AND member.tournament_id = group_revision.tournament_id
    AND member.roster_id = group_revision.roster_id
    AND member.group_id = group_revision.group_id
INNER JOIN participants AS participant
    ON participant.id = member.participant_id
    AND participant.roster_id = group_revision.roster_id
INNER JOIN participant_reservations AS reservation
    ON reservation.player_id = participant.player_id
    AND reservation.tournament_id = group_revision.tournament_id
WHERE group_revision.tournament_id = sqlc.arg(tournament_id)
ORDER BY member.participant_id
FOR KEY SHARE OF participant, reservation;

-- name: HasGoldenRuntimePlanSnapshot :one
SELECT EXISTS (
    SELECT 1
    FROM golden_exact_plan_snapshots AS plan
    INNER JOIN golden_exact_plan_snapshot_seals AS seal
        ON seal.plan_id = plan.plan_id
        AND seal.tournament_id = plan.tournament_id
        AND seal.roster_id = plan.roster_id
    WHERE plan.tournament_id = sqlc.arg(tournament_id)
        AND plan.roster_id = sqlc.arg(roster_id)
        AND plan.source_projection_revision_id = sqlc.arg(source_projection_revision_id)
        AND plan.source_projection_revision = sqlc.arg(source_projection_revision)
) AS plan_exists;

-- name: SelectGoldenRuntimeTask :one
SELECT plan.plan_id,
    edge.position,
    version.task_id,
    version.version,
    version.title,
    version.category,
    version.difficulty,
    version.flag,
    version.content_digest,
    edge.edge_id,
    edge.reservation_id,
    edge.snapshot_id
FROM golden_exact_plan_snapshots AS plan
INNER JOIN golden_exact_plan_snapshot_seals AS seal
    ON seal.plan_id = plan.plan_id
    AND seal.tournament_id = plan.tournament_id
    AND seal.roster_id = plan.roster_id
INNER JOIN golden_exact_plan_snapshot_edges AS edge
    ON edge.plan_id = plan.plan_id
    AND edge.tournament_id = plan.tournament_id
    AND edge.roster_id = plan.roster_id
INNER JOIN task_versions AS version
    ON version.task_id = edge.task_id
    AND version.version = edge.task_version
INNER JOIN task_snapshots AS snapshot
    ON snapshot.id = edge.snapshot_id
    AND snapshot.reservation_id = edge.reservation_id
    AND snapshot.task_id = edge.task_id
    AND snapshot.task_version = edge.task_version
INNER JOIN golden_exact_plan_snapshot_candidates AS candidate
    ON candidate.plan_id = plan.plan_id
    AND candidate.tournament_id = plan.tournament_id
    AND candidate.roster_id = plan.roster_id
    AND candidate.pool_revision_id = plan.pool_revision_id
    AND candidate.task_id = edge.task_id
    AND candidate.task_version = edge.task_version
INNER JOIN golden_exact_plan_snapshot_reservations AS reservation
    ON reservation.plan_id = plan.plan_id
    AND reservation.tournament_id = plan.tournament_id
    AND reservation.roster_id = plan.roster_id
    AND reservation.task_id = edge.task_id
    AND reservation.task_version = edge.task_version
    AND reservation.reservation_id = edge.reservation_id
INNER JOIN task_version_reservations AS live_reservation
    ON live_reservation.id = reservation.reservation_id
    AND live_reservation.tournament_id = plan.tournament_id
    AND live_reservation.task_id = reservation.task_id
    AND live_reservation.task_version = reservation.task_version
    AND live_reservation.plan_id = reservation.owner_plan_id
INNER JOIN assignment_plans AS owner_plan
    ON owner_plan.id = reservation.owner_plan_id
    AND owner_plan.revision_id = reservation.owner_plan_revision_id
    AND owner_plan.tournament_id = plan.tournament_id
    AND owner_plan.roster_id = plan.roster_id
INNER JOIN tournament_content_configurations AS content
    ON content.tournament_id = plan.tournament_id
    AND content.state = 'published'
    AND content.golden_pool_revision_id = plan.pool_revision_id
INNER JOIN tasks AS task
    ON task.id = version.task_id
    AND task.current_version = version.version
    AND task.enabled
    AND task.deleted_at IS NULL
    AND task.kind = 'golden'
WHERE plan.tournament_id = sqlc.arg(tournament_id)
    AND edge.group_revision_id = sqlc.arg(group_revision_id)
    AND edge.position = sqlc.arg(edge_position)
    AND version.time_limit = 180
    AND candidate.exists_in_source
    AND candidate.enabled
    AND candidate.healthy
    AND NOT candidate.mutation_locked
    AND NOT candidate.publicly_exposed
    AND NOT EXISTS (
        SELECT 1
        FROM task_public_exposures AS exposure
        WHERE exposure.task_id = version.task_id
            AND exposure.task_version = version.version
    )
    AND candidate.artifact_digest = edge.content_digest
    AND edge.content_digest = version.content_digest
    AND live_reservation.state = 'reserved'
    AND snapshot.kind = 'golden'
    AND snapshot.title = version.title
    AND snapshot.description = version.description
    AND snapshot.category = version.category
    AND snapshot.difficulty = version.difficulty
    AND snapshot.time_limit = 180
    AND snapshot.flag = version.flag
    AND snapshot.content_digest = edge.content_digest
    AND NOT EXISTS (
        SELECT 1
        FROM golden_runtime_assignments AS used
        WHERE used.plan_id = plan.plan_id
            AND (
                used.wave_id = edge.edge_id
                OR used.assignment_id = edge.reservation_id
                OR used.snapshot_id = edge.snapshot_id
                OR (
                    used.task_id = edge.task_id
                    AND used.task_version = edge.task_version
                )
            )
    )
    AND NOT EXISTS (
        SELECT 1
        FROM golden_exact_plan_snapshot_members AS member
        LEFT JOIN golden_exact_plan_snapshot_participant_reservations AS snap
            ON snap.plan_id = member.plan_id
            AND snap.tournament_id = member.tournament_id
            AND snap.roster_id = member.roster_id
            AND snap.participant_id = member.participant_id
        LEFT JOIN participant_reservations AS live
            ON live.player_id = snap.player_id
            AND live.tournament_id = snap.tournament_id
        WHERE member.plan_id = plan.plan_id
            AND member.group_revision_id = edge.group_revision_id
            AND (
                snap.participant_id IS NULL
                OR live.reservation_id IS DISTINCT FROM snap.reservation_id
                OR live.revision IS DISTINCT FROM snap.revision
                OR live.acquired_at IS DISTINCT FROM snap.acquired_at
                OR live.updated_at IS DISTINCT FROM snap.updated_at
            )
    )
ORDER BY plan.created_at DESC, plan.plan_id, edge.edge_id
LIMIT 1;

-- name: CreateGoldenRuntimeAssignment :one
INSERT INTO golden_runtime_assignments (
    attempt_id, tournament_id, roster_id, group_revision_id,
    plan_id, edge_position, wave_id, assignment_id, snapshot_id, task_id, task_version,
    title, category, difficulty, time_limit_seconds, source_digest,
    ready_window_id, ready_window_opened_at, ready_window_deadline, created_at
)
VALUES (
    sqlc.arg(attempt_id), sqlc.arg(tournament_id), sqlc.arg(roster_id),
    sqlc.arg(group_revision_id), sqlc.arg(plan_id), sqlc.arg(edge_position),
    sqlc.arg(wave_id), sqlc.arg(assignment_id),
    sqlc.arg(snapshot_id), sqlc.arg(task_id), sqlc.arg(task_version),
    sqlc.arg(title), sqlc.arg(category), sqlc.arg(difficulty), 180,
    sqlc.arg(source_digest), sqlc.arg(ready_window_id), sqlc.arg(created_at),
    sqlc.arg(created_at)::timestamptz + interval '30 seconds', sqlc.arg(created_at)
)
RETURNING attempt_id;

-- name: StartGoldenRuntimeAssignment :one
UPDATE golden_runtime_assignments
SET started_at = sqlc.arg(started_at)::timestamptz,
    deadline = sqlc.arg(started_at)::timestamptz + interval '180 seconds'
WHERE attempt_id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND started_at IS NULL
RETURNING attempt_id;

-- name: ResetGoldenRuntimeReadyWindow :one
UPDATE golden_runtime_assignments
SET ready_window_id = sqlc.arg(ready_window_id),
    ready_window_opened_at = sqlc.arg(opened_at),
    ready_window_deadline = sqlc.arg(opened_at)::timestamptz + interval '30 seconds'
WHERE attempt_id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND started_at IS NULL
    AND settlement_revision_id IS NULL
RETURNING attempt_id;

-- name: ListGoldenRuntimeView :many
SELECT runtime.tournament_id,
    runtime.roster_id,
    group_revision.group_id,
    runtime.group_revision_id,
    runtime.attempt_id,
    attempt.state,
    group_revision.position_from,
    group_revision.position_to,
    runtime.started_at,
    runtime.deadline,
    runtime.ready_window_id,
    runtime.ready_window_deadline,
    runtime_head.revision AS runtime_revision,
    membership.id AS membership_id,
    membership.participant_id,
    participant.player_id,
    membership.ready_at,
    COALESCE(membership.participation_established_at IS NOT NULL, false)::boolean AS participant_eligible,
    submission.id AS submission_id,
    position_commit.position,
    runtime.assignment_id,
    runtime.snapshot_id,
    snapshot.task_id,
    snapshot.task_version,
    snapshot.title,
    snapshot.description,
    snapshot.category,
    snapshot.difficulty,
    snapshot.time_limit,
    snapshot.task_url,
    COALESCE(
        snapshot.source_file_url IS NOT NULL
            AND btrim(snapshot.source_file_url) <> '',
        false
    )::boolean AS source_file_available
FROM golden_runtime_assignments AS runtime
INNER JOIN golden_runtime_heads AS runtime_head
    ON runtime_head.tournament_id = runtime.tournament_id
    AND runtime_head.roster_id = runtime.roster_id
INNER JOIN golden_group_revisions AS group_revision
    ON group_revision.revision_id = runtime.group_revision_id
INNER JOIN golden_attempts AS attempt ON attempt.id = runtime.attempt_id
INNER JOIN golden_memberships AS membership ON membership.attempt_id = runtime.attempt_id
INNER JOIN participants AS participant
    ON participant.id = membership.participant_id
    AND participant.roster_id = runtime.roster_id
INNER JOIN task_snapshots AS snapshot
    ON snapshot.id = runtime.snapshot_id
    AND snapshot.reservation_id = runtime.assignment_id
    AND snapshot.task_id = runtime.task_id
    AND snapshot.task_version = runtime.task_version
    AND snapshot.kind = 'golden'
    AND snapshot.content_digest = runtime.source_digest
LEFT JOIN golden_provisional_submissions AS submission
    ON submission.attempt_id = runtime.attempt_id
    AND submission.membership_id = membership.id
    AND submission.status = 'accepted'
LEFT JOIN golden_position_commits AS position_commit
    ON position_commit.provisional_submission_id = submission.id
WHERE runtime.tournament_id = sqlc.arg(tournament_id)
    AND NOT EXISTS (
        SELECT 1
        FROM golden_runtime_assignments AS newer
        WHERE newer.group_revision_id = runtime.group_revision_id
            AND newer.edge_position > runtime.edge_position
    )
ORDER BY group_revision.position_from, membership.participant_id;

-- name: LockGoldenRuntimeParticipant :one
SELECT runtime.tournament_id,
    runtime.roster_id,
    runtime.group_revision_id,
    runtime.attempt_id,
    runtime.started_at,
    runtime.deadline,
    runtime.ready_window_id,
    runtime.ready_window_deadline,
    runtime_head.revision AS runtime_revision,
    runtime.edge_position,
    runtime.task_id,
    runtime.task_version,
    version.flag,
    membership.id AS membership_id,
    membership.participant_id,
    membership.ready_at,
    attempt.state,
    group_revision.position_from,
    group_revision.position_to
FROM golden_runtime_assignments AS runtime
INNER JOIN golden_runtime_heads AS runtime_head
    ON runtime_head.tournament_id = runtime.tournament_id
    AND runtime_head.roster_id = runtime.roster_id
INNER JOIN golden_attempts AS attempt ON attempt.id = runtime.attempt_id
INNER JOIN golden_group_revisions AS group_revision
    ON group_revision.revision_id = runtime.group_revision_id
INNER JOIN golden_memberships AS membership ON membership.attempt_id = runtime.attempt_id
INNER JOIN participants AS participant
    ON participant.id = membership.participant_id
    AND participant.roster_id = runtime.roster_id
INNER JOIN task_versions AS version
    ON version.task_id = runtime.task_id
    AND version.version = runtime.task_version
WHERE runtime.tournament_id = sqlc.arg(tournament_id)
    AND participant.player_id = sqlc.arg(player_id)
ORDER BY runtime.edge_position DESC, group_revision.position_from
LIMIT 1
FOR UPDATE OF attempt, membership;

-- name: SelectGoldenRuntimeParticipantScope :one
SELECT runtime.tournament_id,
    runtime.roster_id,
    runtime.group_revision_id,
    runtime.attempt_id,
    runtime.ready_window_id,
    runtime.ready_window_deadline,
    runtime.started_at,
    runtime.deadline,
    runtime_head.revision AS runtime_revision,
    membership.participant_id
FROM golden_runtime_assignments AS runtime
INNER JOIN golden_runtime_heads AS runtime_head
    ON runtime_head.tournament_id = runtime.tournament_id
    AND runtime_head.roster_id = runtime.roster_id
INNER JOIN golden_attempts AS attempt ON attempt.id = runtime.attempt_id
INNER JOIN golden_memberships AS membership ON membership.attempt_id = runtime.attempt_id
INNER JOIN participants AS participant
    ON participant.id = membership.participant_id
    AND participant.roster_id = runtime.roster_id
WHERE runtime.tournament_id = sqlc.arg(tournament_id)
    AND participant.player_id = sqlc.arg(player_id)
ORDER BY runtime.edge_position DESC, runtime.group_revision_id
LIMIT 1;

-- name: ListGoldenRuntimeAttemptMembers :many
SELECT membership.id AS membership_id,
    membership.participant_id,
    membership.ready_at,
    membership.no_show_at,
    membership.excluded_at,
    membership.participation_established_at,
    submission.id AS submission_id,
    submission.server_sequence,
    submission.payload_digest,
    submission.received_at AS committed_at,
    position_commit.id AS position_commit_id,
    position_commit.position,
    revision.revision_id AS submission_revision_id,
    revision.revision_number AS submission_revision
FROM golden_memberships AS membership
LEFT JOIN golden_provisional_submissions AS submission
    ON submission.attempt_id = membership.attempt_id
    AND submission.membership_id = membership.id
    AND submission.status = 'accepted'
LEFT JOIN golden_position_commits AS position_commit
    ON position_commit.provisional_submission_id = submission.id
LEFT JOIN golden_attempt_submission_revisions AS revision
    ON revision.provisional_submission_id = submission.id
WHERE membership.attempt_id = sqlc.arg(attempt_id)
    AND membership.tournament_id = sqlc.arg(tournament_id)
ORDER BY submission.server_sequence NULLS LAST, membership.participant_id
FOR UPDATE OF membership;

-- name: ClearGoldenMembershipReady :one
UPDATE golden_memberships
SET ready_at = NULL
WHERE id = sqlc.arg(id)
    AND attempt_id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND participation_established_at IS NULL
    AND no_show_at IS NULL
    AND ready_at IS NOT NULL
RETURNING id;

-- name: NextGoldenReadyDisconnectSequence :one
SELECT COALESCE(MAX(sequence_number), 0)::integer + 1 AS sequence_number
FROM golden_ready_disconnects
WHERE membership_id = sqlc.arg(membership_id);

-- name: LockOpenGoldenReadyDisconnect :one
SELECT id, disconnected_at
FROM golden_ready_disconnects
WHERE membership_id = sqlc.arg(membership_id)
    AND attempt_id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND state = 'open'
ORDER BY sequence_number DESC
LIMIT 1
FOR UPDATE;

-- name: ListGoldenRuntimeGroupAttempts :many
SELECT runtime.attempt_id,
    attempt.attempt_number,
    attempt.state,
    runtime.tournament_id,
    runtime.roster_id,
    runtime.group_revision_id,
    runtime.wave_id,
    runtime.assignment_id,
    runtime.snapshot_id,
    runtime.task_id,
    runtime.edge_position,
    runtime.started_at,
    runtime.deadline
FROM golden_runtime_assignments AS runtime
INNER JOIN golden_attempts AS attempt ON attempt.id = runtime.attempt_id
WHERE runtime.tournament_id = sqlc.arg(tournament_id)
    AND runtime.group_revision_id = sqlc.arg(group_revision_id)
ORDER BY runtime.edge_position
FOR UPDATE OF attempt;

-- name: ListGoldenRuntimeGroupEvidence :many
SELECT runtime.attempt_id,
    attempt.attempt_number,
    runtime.roster_id,
    runtime.wave_id,
    runtime.assignment_id,
    runtime.snapshot_id,
    runtime.task_id,
    membership.id AS membership_id,
    membership.participant_id,
    source.position AS source_position,
    membership.no_show_at,
    submission.id AS submission_id,
    submission.server_sequence,
    submission.payload_digest,
    revision.revision_id AS submission_revision_id,
    revision.revision_number AS submission_revision,
    position_commit.id AS position_commit_id,
    position_commit.position
FROM golden_runtime_assignments AS runtime
INNER JOIN golden_attempts AS attempt ON attempt.id = runtime.attempt_id
INNER JOIN golden_memberships AS membership ON membership.attempt_id = runtime.attempt_id
INNER JOIN golden_exact_plan_snapshot_members AS source
    ON source.plan_id = runtime.plan_id
    AND source.group_revision_id = runtime.group_revision_id
    AND source.participant_id = membership.participant_id
LEFT JOIN golden_provisional_submissions AS submission
    ON submission.attempt_id = runtime.attempt_id
    AND submission.membership_id = membership.id
LEFT JOIN golden_attempt_submission_revisions AS revision
    ON revision.provisional_submission_id = submission.id
LEFT JOIN golden_position_commits AS position_commit
    ON position_commit.provisional_submission_id = submission.id
WHERE runtime.tournament_id = sqlc.arg(tournament_id)
    AND runtime.group_revision_id = sqlc.arg(group_revision_id)
ORDER BY attempt.attempt_number, submission.server_sequence NULLS LAST, membership.participant_id
FOR UPDATE OF membership;

-- name: CountGoldenRuntimeGroupCommits :one
SELECT COUNT(*)::integer
FROM golden_attempt_stage_groups AS attempt_group
INNER JOIN golden_position_commits AS position_commit
    ON position_commit.attempt_id = attempt_group.attempt_id
WHERE attempt_group.tournament_id = sqlc.arg(tournament_id)
    AND attempt_group.group_revision_id = sqlc.arg(group_revision_id);

-- name: ListGoldenRuntimeUnresolvedMembers :many
SELECT source.participant_id
FROM golden_exact_plan_snapshot_members AS source
INNER JOIN golden_exact_plan_snapshot_seals AS seal ON seal.plan_id = source.plan_id
WHERE source.tournament_id = sqlc.arg(tournament_id)
    AND source.group_revision_id = sqlc.arg(group_revision_id)
    AND NOT EXISTS (
        SELECT 1
        FROM golden_attempt_stage_groups AS attempt_group
        INNER JOIN golden_position_commits AS position_commit
            ON position_commit.attempt_id = attempt_group.attempt_id
            AND position_commit.participant_id = source.participant_id
        WHERE attempt_group.tournament_id = source.tournament_id
            AND attempt_group.group_revision_id = source.group_revision_id
    )
    AND NOT EXISTS (
        SELECT 1
        FROM golden_attempt_stage_groups AS attempt_group
        INNER JOIN golden_memberships AS membership
            ON membership.attempt_id = attempt_group.attempt_id
            AND membership.participant_id = source.participant_id
        WHERE attempt_group.tournament_id = source.tournament_id
            AND attempt_group.group_revision_id = source.group_revision_id
            AND membership.no_show_at IS NOT NULL
    )
ORDER BY source.position;

-- name: FinalizeGoldenRuntimeGroupAssignments :execrows
UPDATE golden_runtime_assignments
SET settlement_revision_id = sqlc.arg(settlement_revision_id),
    finalized_at = sqlc.arg(finalized_at)
WHERE tournament_id = sqlc.arg(tournament_id)
    AND group_revision_id = sqlc.arg(group_revision_id)
    AND settlement_revision_id IS NULL;

-- name: GetGoldenRuntimeAssignment :one
SELECT runtime.tournament_id,
    runtime.roster_id,
    runtime.group_revision_id,
    runtime.attempt_id,
    runtime.wave_id,
    runtime.assignment_id,
    runtime.snapshot_id,
    runtime.task_id,
    runtime.plan_id,
    runtime.edge_position,
    runtime.ready_window_id,
    runtime.ready_window_opened_at,
    runtime.ready_window_deadline,
    runtime.started_at,
    runtime.deadline,
    attempt.attempt_number,
    attempt.state,
    group_revision.group_id,
    group_revision.position_from,
    group_revision.position_to
FROM golden_runtime_assignments AS runtime
INNER JOIN golden_attempts AS attempt ON attempt.id = runtime.attempt_id
INNER JOIN golden_group_revisions AS group_revision
    ON group_revision.revision_id = runtime.group_revision_id
WHERE runtime.attempt_id = sqlc.arg(attempt_id)
    AND runtime.tournament_id = sqlc.arg(tournament_id);

-- name: ListGoldenRuntimeRecoveryAttempts :many
SELECT runtime.attempt_id,
    runtime.tournament_id,
    runtime.roster_id,
    attempt.state,
    runtime.group_revision_id,
    runtime.edge_position,
    runtime.ready_window_id,
    runtime.ready_window_deadline,
    runtime.started_at,
    runtime.deadline
FROM golden_runtime_assignments AS runtime
INNER JOIN golden_attempts AS attempt ON attempt.id = runtime.attempt_id
WHERE runtime.tournament_id = sqlc.arg(tournament_id)
    AND attempt.state IN ('prepared', 'ready', 'active', 'technical_pause')
ORDER BY runtime.group_revision_id, runtime.edge_position;

-- Golden repository durable store. Normalized Golden evidence remains the
-- authority; this store retains validated aggregate state and replay receipts
-- only where the normalized rows cannot reconstruct the application graph.

-- name: CreateGoldenRepositoryScope :one
INSERT INTO golden_repository_scopes (
    id,
    aggregate_kind,
    tournament_id,
    roster_id,
    plan_set_id,
    group_id,
    group_revision_id,
    attempt_id,
    wave_id,
    assignment_id,
    snapshot_id,
    task_id,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(aggregate_kind),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.narg(plan_set_id)::UUID,
    sqlc.narg(group_id)::UUID,
    sqlc.narg(group_revision_id)::UUID,
    sqlc.narg(attempt_id)::UUID,
    sqlc.narg(wave_id)::UUID,
    sqlc.narg(assignment_id)::UUID,
    sqlc.narg(snapshot_id)::UUID,
    sqlc.narg(task_id)::UUID,
    sqlc.arg(created_at)
)
ON CONFLICT DO NOTHING
RETURNING id;

-- name: LockGoldenRepositoryScope :one
SELECT id,
    aggregate_kind,
    tournament_id,
    roster_id,
    plan_set_id,
    group_id,
    group_revision_id,
    attempt_id,
    wave_id,
    assignment_id,
    snapshot_id,
    task_id,
    created_at
FROM golden_repository_scopes
WHERE aggregate_kind = sqlc.arg(aggregate_kind)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND plan_set_id IS NOT DISTINCT FROM sqlc.narg(plan_set_id)::UUID
    AND group_id IS NOT DISTINCT FROM sqlc.narg(group_id)::UUID
    AND group_revision_id IS NOT DISTINCT FROM sqlc.narg(group_revision_id)::UUID
    AND attempt_id IS NOT DISTINCT FROM sqlc.narg(attempt_id)::UUID
    AND wave_id IS NOT DISTINCT FROM sqlc.narg(wave_id)::UUID
    AND assignment_id IS NOT DISTINCT FROM sqlc.narg(assignment_id)::UUID
    AND snapshot_id IS NOT DISTINCT FROM sqlc.narg(snapshot_id)::UUID
    AND task_id IS NOT DISTINCT FROM sqlc.narg(task_id)::UUID
FOR UPDATE;

-- name: LoadGoldenRepositoryScope :one
SELECT id,
    aggregate_kind,
    tournament_id,
    roster_id,
    plan_set_id,
    group_id,
    group_revision_id,
    attempt_id,
    wave_id,
    assignment_id,
    snapshot_id,
    task_id,
    created_at
FROM golden_repository_scopes
WHERE id = sqlc.arg(id);

-- name: LockGoldenRepositoryHead :one
SELECT head.scope_id,
    head.revision_id,
    head.revision_number,
    head.payload_digest,
    head.updated_at,
    revision.aggregate_kind,
    revision.previous_revision_id,
    revision.payload,
    revision.created_at
FROM golden_repository_heads AS head
INNER JOIN golden_repository_revisions AS revision
    ON revision.scope_id = head.scope_id
    AND revision.revision_id = head.revision_id
WHERE head.scope_id = sqlc.arg(scope_id)
FOR UPDATE OF head;

-- name: LoadGoldenRepositoryHead :one
SELECT head.scope_id,
    head.revision_id,
    head.revision_number,
    head.payload_digest,
    head.updated_at,
    revision.aggregate_kind,
    revision.previous_revision_id,
    revision.payload,
    revision.created_at
FROM golden_repository_heads AS head
INNER JOIN golden_repository_revisions AS revision
    ON revision.scope_id = head.scope_id
    AND revision.revision_id = head.revision_id
WHERE head.scope_id = sqlc.arg(scope_id);

-- name: CreateGoldenRepositoryRevision :one
INSERT INTO golden_repository_revisions (
    scope_id,
    aggregate_kind,
    revision_id,
    revision_number,
    previous_revision_id,
    payload,
    payload_digest,
    created_at
)
VALUES (
    sqlc.arg(scope_id),
    sqlc.arg(aggregate_kind),
    sqlc.arg(revision_id),
    sqlc.arg(revision_number),
    sqlc.narg(previous_revision_id)::UUID,
    sqlc.arg(payload)::JSONB,
    sqlc.arg(payload_digest),
    sqlc.arg(created_at)
)
RETURNING scope_id,
    aggregate_kind,
    revision_id,
    revision_number,
    previous_revision_id,
    payload,
    payload_digest,
    created_at;

-- name: CreateGoldenRepositoryHead :one
INSERT INTO golden_repository_heads (
    scope_id,
    revision_id,
    revision_number,
    payload_digest,
    updated_at
)
VALUES (
    sqlc.arg(scope_id),
    sqlc.arg(revision_id),
    sqlc.arg(revision_number),
    sqlc.arg(payload_digest),
    sqlc.arg(updated_at)
)
RETURNING scope_id,
    revision_id,
    revision_number,
    payload_digest,
    updated_at;

-- name: AdvanceGoldenRepositoryHeadCAS :one
UPDATE golden_repository_heads
SET revision_id = sqlc.arg(next_revision_id),
    revision_number = sqlc.arg(next_revision_number),
    payload_digest = sqlc.arg(next_payload_digest),
    updated_at = sqlc.arg(updated_at)
WHERE scope_id = sqlc.arg(scope_id)
    AND revision_id = sqlc.arg(expected_revision_id)
    AND revision_number = sqlc.arg(expected_revision_number)
    AND payload_digest = sqlc.arg(expected_payload_digest)
RETURNING scope_id,
    revision_id,
    revision_number,
    payload_digest,
    updated_at;

-- name: FindGoldenRepositoryCommand :one
SELECT journal.scope_id,
    journal.tournament_id,
    journal.command_id,
    journal.command_kind,
    journal.command_digest,
    journal.result_revision_id,
    journal.occurred_at,
    journal.created_at,
    revision.aggregate_kind,
    revision.revision_number,
    revision.previous_revision_id,
    revision.payload,
    revision.payload_digest
FROM golden_repository_command_journal AS journal
INNER JOIN golden_repository_revisions AS revision
    ON revision.scope_id = journal.scope_id
    AND revision.revision_id = journal.result_revision_id
WHERE journal.tournament_id = sqlc.arg(tournament_id)
    AND journal.command_id = sqlc.arg(command_id);

-- name: CreateGoldenRepositoryCommand :one
INSERT INTO golden_repository_command_journal (
    scope_id,
    tournament_id,
    command_id,
    command_kind,
    command_digest,
    result_revision_id,
    occurred_at,
    created_at
)
VALUES (
    sqlc.arg(scope_id),
    sqlc.arg(tournament_id),
    sqlc.arg(command_id),
    sqlc.arg(command_kind),
    sqlc.arg(command_digest),
    sqlc.arg(result_revision_id),
    sqlc.arg(occurred_at),
    sqlc.arg(created_at)
)
RETURNING scope_id,
    tournament_id,
    command_id,
    command_kind,
    command_digest,
    result_revision_id,
    occurred_at,
    created_at;
