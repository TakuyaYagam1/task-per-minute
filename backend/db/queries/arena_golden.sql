-- name: LockArenaGoldenAttempt :one
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
FROM arena_golden_attempts
WHERE id = sqlc.arg(id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
FOR UPDATE;

-- name: CreateArenaGoldenAttempt :one
INSERT INTO arena_golden_attempts (
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

-- name: CreateArenaGoldenMembership :one
INSERT INTO arena_golden_memberships (
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

-- name: MarkArenaGoldenMembershipReady :one
UPDATE arena_golden_memberships
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

-- name: MarkArenaGoldenMembershipNoShow :one
UPDATE arena_golden_memberships
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

-- name: EstablishArenaGoldenParticipation :one
UPDATE arena_golden_memberships
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

-- name: CreateArenaGoldenReservePromotion :one
INSERT INTO arena_golden_reserve_promotions (
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

-- name: UpdateArenaGoldenAttemptCAS :one
UPDATE arena_golden_attempts
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

-- name: CreateArenaGoldenReadyDisconnect :one
INSERT INTO arena_golden_ready_disconnects (
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

-- name: CloseArenaGoldenReadyDisconnectCAS :one
UPDATE arena_golden_ready_disconnects
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

-- name: CreateArenaGoldenProvisionalSubmission :one
INSERT INTO arena_golden_provisional_submissions (
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

-- name: GetArenaGoldenSubmissionByIdempotencyKey :one
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
FROM arena_golden_provisional_submissions
WHERE idempotency_key = sqlc.arg(idempotency_key);

-- name: CreateArenaGoldenPositionCommit :one
INSERT INTO arena_golden_position_commits (
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

-- name: CreateArenaGoldenRecoveryRevision :one
INSERT INTO arena_golden_recovery_revisions (
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

-- name: GetLatestArenaGoldenRecoveryRevision :one
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
FROM arena_golden_recovery_revisions
WHERE attempt_id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY revision_number DESC
LIMIT 1;

-- name: GetArenaGoldenAttemptScoped :one
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
FROM arena_golden_attempts
WHERE id = sqlc.arg(id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id);

-- name: ListArenaGoldenAttempts :many
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
FROM arena_golden_attempts
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY attempt_number;

-- name: ListArenaGoldenMemberships :many
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
FROM arena_golden_memberships
WHERE attempt_id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY selection_kind, reserve_position, participant_id;

-- name: ListArenaGoldenReadyDisconnects :many
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
FROM arena_golden_ready_disconnects
WHERE attempt_id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY membership_id, sequence_number;

-- name: ListArenaGoldenProvisionalSubmissions :many
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
FROM arena_golden_provisional_submissions
WHERE attempt_id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY server_sequence;

-- name: ListArenaGoldenPositionCommits :many
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
FROM arena_golden_position_commits
WHERE attempt_id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY position;

-- name: ListArenaGoldenReservePromotions :many
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
FROM arena_golden_reserve_promotions
WHERE attempt_id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY promoted_at, id;

-- name: ListArenaGoldenRecoveryRevisions :many
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
FROM arena_golden_recovery_revisions
WHERE attempt_id = sqlc.arg(attempt_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY revision_number;
