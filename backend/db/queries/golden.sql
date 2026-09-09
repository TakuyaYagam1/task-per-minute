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
    AND selection_kind = 'direct'
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
