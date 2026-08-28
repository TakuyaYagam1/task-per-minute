-- name: CreateConservativeArenaAssignmentPlan :one
INSERT INTO arena_assignment_plans (
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
    state,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    'conservative',
    sqlc.arg(revision_id),
    sqlc.arg(source_roster_revision),
    sqlc.arg(source_pool_revision_id),
    0,
    sqlc.arg(constraint_graph),
    sqlc.arg(proof_evidence),
    'planned',
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    kind,
    parent_plan_id,
    revision_id,
    source_roster_revision,
    source_pool_revision_id,
    source_draft_revision_id,
    reachable_branch_count,
    constraint_graph,
    proof_evidence,
    decision_evidence_id,
    decision_algorithm_version,
    decision_inputs,
    decision_seed,
    decision_result,
    decision_replay_digest,
    decision_owner_id,
    decided_at,
    state,
    active_branch_id,
    committed_at,
    superseded_at,
    supersession_reason,
    created_at;

-- name: CreateExactArenaAssignmentPlan :one
INSERT INTO arena_assignment_plans (
    id,
    tournament_id,
    roster_id,
    kind,
    parent_plan_id,
    revision_id,
    source_roster_revision,
    source_pool_revision_id,
    source_draft_revision_id,
    reachable_branch_count,
    constraint_graph,
    proof_evidence,
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
    'exact',
    sqlc.arg(parent_plan_id),
    sqlc.arg(revision_id),
    sqlc.arg(source_roster_revision),
    sqlc.arg(source_pool_revision_id),
    sqlc.arg(source_draft_revision_id),
    sqlc.arg(reachable_branch_count),
    sqlc.arg(constraint_graph),
    sqlc.arg(proof_evidence),
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
)
RETURNING id,
    tournament_id,
    roster_id,
    kind,
    parent_plan_id,
    revision_id,
    source_roster_revision,
    source_pool_revision_id,
    source_draft_revision_id,
    reachable_branch_count,
    constraint_graph,
    proof_evidence,
    decision_evidence_id,
    decision_algorithm_version,
    decision_inputs,
    decision_seed,
    decision_result,
    decision_replay_digest,
    decision_owner_id,
    decided_at,
    state,
    active_branch_id,
    committed_at,
    superseded_at,
    supersession_reason,
    created_at;

-- name: CreateArenaAssignmentBranch :one
INSERT INTO arena_assignment_branches (
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
    sqlc.arg(draft_id),
    sqlc.arg(draft_revision_id),
    sqlc.arg(branch_key),
    sqlc.arg(category_sequence),
    'reserved',
    sqlc.arg(created_at)
)
RETURNING id,
    plan_id,
    draft_id,
    draft_revision_id,
    branch_key,
    category_sequence,
    state,
    disclosed_at,
    activated_at,
    released_at,
    release_reason,
    superseded_at,
    supersession_reason,
    created_at;

-- name: CreateArenaAssignmentPlanEdge :one
INSERT INTO arena_assignment_plan_edges (
    id,
    plan_id,
    branch_id,
    position,
    task_id,
    task_version,
    selection_evidence,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(plan_id),
    sqlc.arg(branch_id),
    sqlc.arg(position),
    sqlc.arg(task_id),
    sqlc.arg(task_version),
    sqlc.arg(selection_evidence),
    sqlc.arg(created_at)
)
RETURNING id,
    plan_id,
    branch_id,
    position,
    task_id,
    task_version,
    selection_evidence,
    created_at;

-- name: CreateArenaTaskVersionReservation :one
INSERT INTO arena_task_version_reservations (
    id,
    edge_id,
    plan_id,
    branch_id,
    task_id,
    task_version,
    state,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(edge_id),
    sqlc.arg(plan_id),
    sqlc.arg(branch_id),
    sqlc.arg(task_id),
    sqlc.arg(task_version),
    'reserved',
    sqlc.arg(created_at)
)
RETURNING id,
    edge_id,
    plan_id,
    branch_id,
    task_id,
    task_version,
    state,
    disclosed_at,
    committed_at,
    released_at,
    release_reason,
    superseded_at,
    supersession_reason,
    created_at;

-- name: CreateArenaTaskSnapshot :one
INSERT INTO arena_task_snapshots (
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
    sqlc.arg(kind),
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
RETURNING id,
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
    created_at;

-- name: LockArenaAssignmentPlan :one
SELECT id,
    tournament_id,
    roster_id,
    kind,
    parent_plan_id,
    revision_id,
    source_roster_revision,
    source_pool_revision_id,
    source_draft_revision_id,
    reachable_branch_count,
    constraint_graph,
    proof_evidence,
    decision_evidence_id,
    decision_algorithm_version,
    decision_inputs,
    decision_seed,
    decision_result,
    decision_replay_digest,
    decision_owner_id,
    decided_at,
    state,
    active_branch_id,
    committed_at,
    superseded_at,
    supersession_reason,
    created_at
FROM arena_assignment_plans
WHERE id = sqlc.arg(id)
FOR UPDATE;

-- name: CommitArenaBranchReservations :many
UPDATE arena_task_version_reservations
SET state = 'committed',
    committed_at = sqlc.arg(committed_at)
WHERE plan_id = sqlc.arg(plan_id)
    AND branch_id = sqlc.arg(branch_id)
    AND state = 'reserved'
RETURNING id,
    edge_id,
    plan_id,
    branch_id,
    task_id,
    task_version,
    state,
    disclosed_at,
    committed_at,
    released_at,
    release_reason,
    superseded_at,
    supersession_reason,
    created_at;

-- name: ReleaseOtherArenaBranchReservations :many
UPDATE arena_task_version_reservations
SET state = 'released',
    released_at = sqlc.arg(released_at),
    release_reason = sqlc.arg(release_reason)
WHERE plan_id = sqlc.arg(plan_id)
    AND branch_id <> sqlc.arg(active_branch_id)
    AND state = 'reserved'
RETURNING id;

-- name: ActivateArenaAssignmentBranch :one
UPDATE arena_assignment_branches
SET state = 'active',
    activated_at = sqlc.arg(activated_at)
WHERE id = sqlc.arg(id)
    AND plan_id = sqlc.arg(plan_id)
    AND state = 'reserved'
RETURNING id,
    plan_id,
    draft_id,
    draft_revision_id,
    branch_key,
    category_sequence,
    state,
    disclosed_at,
    activated_at,
    released_at,
    release_reason,
    superseded_at,
    supersession_reason,
    created_at;

-- name: ReleaseOtherArenaAssignmentBranches :many
UPDATE arena_assignment_branches
SET state = 'released',
    released_at = sqlc.arg(released_at),
    release_reason = sqlc.arg(release_reason)
WHERE plan_id = sqlc.arg(plan_id)
    AND id <> sqlc.arg(active_branch_id)
    AND state = 'reserved'
RETURNING id;

-- name: CommitArenaAssignmentPlanCAS :one
UPDATE arena_assignment_plans
SET state = 'committed',
    active_branch_id = sqlc.arg(active_branch_id),
    committed_at = sqlc.arg(committed_at)
WHERE id = sqlc.arg(id)
    AND state = 'planned'
    AND kind = 'exact'
    AND revision_id = sqlc.arg(expected_revision_id)
    AND source_roster_revision = sqlc.arg(expected_roster_revision)
    AND source_draft_revision_id = sqlc.arg(expected_draft_revision_id)
RETURNING id,
    tournament_id,
    roster_id,
    kind,
    parent_plan_id,
    revision_id,
    source_roster_revision,
    source_pool_revision_id,
    source_draft_revision_id,
    reachable_branch_count,
    constraint_graph,
    proof_evidence,
    decision_evidence_id,
    decision_algorithm_version,
    decision_inputs,
    decision_seed,
    decision_result,
    decision_replay_digest,
    decision_owner_id,
    decided_at,
    state,
    active_branch_id,
    committed_at,
    superseded_at,
    supersession_reason,
    created_at;

-- name: GetArenaAssignmentPlan :one
SELECT id,
    tournament_id,
    roster_id,
    kind,
    parent_plan_id,
    revision_id,
    source_roster_revision,
    source_pool_revision_id,
    source_draft_revision_id,
    reachable_branch_count,
    constraint_graph,
    proof_evidence,
    decision_evidence_id,
    decision_algorithm_version,
    decision_inputs,
    decision_seed,
    decision_result,
    decision_replay_digest,
    decision_owner_id,
    decided_at,
    state,
    active_branch_id,
    committed_at,
    superseded_at,
    supersession_reason,
    created_at
FROM arena_assignment_plans
WHERE id = sqlc.arg(id);

-- name: ListArenaAssignmentBranches :many
SELECT id,
    plan_id,
    draft_id,
    draft_revision_id,
    branch_key,
    category_sequence,
    state,
    disclosed_at,
    activated_at,
    released_at,
    release_reason,
    superseded_at,
    supersession_reason,
    created_at
FROM arena_assignment_branches
WHERE plan_id = sqlc.arg(plan_id)
ORDER BY branch_key,
    id;

-- name: ListArenaAssignmentPlanEdges :many
SELECT id,
    plan_id,
    branch_id,
    position,
    task_id,
    task_version,
    selection_evidence,
    created_at
FROM arena_assignment_plan_edges
WHERE plan_id = sqlc.arg(plan_id)
ORDER BY branch_id,
    position;

-- name: ListArenaTaskVersionReservations :many
SELECT id,
    edge_id,
    plan_id,
    branch_id,
    task_id,
    task_version,
    state,
    disclosed_at,
    committed_at,
    released_at,
    release_reason,
    superseded_at,
    supersession_reason,
    created_at
FROM arena_task_version_reservations
WHERE plan_id = sqlc.arg(plan_id)
ORDER BY branch_id,
    task_id,
    task_version;

-- name: ListArenaTaskSnapshotsForPlan :many
SELECT snapshot.id,
    snapshot.reservation_id,
    snapshot.task_id,
    snapshot.task_version,
    snapshot.kind,
    snapshot.title,
    snapshot.description,
    snapshot.category,
    snapshot.difficulty,
    snapshot.time_limit,
    snapshot.flag,
    snapshot.hints,
    snapshot.task_url,
    snapshot.source_file_url,
    snapshot.content_digest,
    snapshot.created_at
FROM arena_task_snapshots AS snapshot
JOIN arena_task_version_reservations AS reservation
    ON reservation.id = snapshot.reservation_id
WHERE reservation.plan_id = sqlc.arg(plan_id)
ORDER BY reservation.branch_id,
    snapshot.task_id,
    snapshot.task_version;

-- name: CreateArenaAssignment :one
INSERT INTO arena_assignments (
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
    supersedes_assignment_id,
    state,
    revision,
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
    sqlc.arg(supersedes_assignment_id),
    'active',
    1,
    sqlc.arg(created_at),
    sqlc.arg(created_at)
)
RETURNING id,
    attempt_id,
    series_id,
    roster_id,
    plan_id,
    branch_id,
    reservation_id,
    snapshot_id,
    task_id,
    task_version,
    supersedes_assignment_id,
    state,
    revision,
    created_at,
    updated_at,
    completed_at,
    superseded_at,
    supersession_reason;

-- name: LockArenaAssignment :one
SELECT id,
    attempt_id,
    series_id,
    roster_id,
    plan_id,
    branch_id,
    reservation_id,
    snapshot_id,
    task_id,
    task_version,
    supersedes_assignment_id,
    state,
    revision,
    created_at,
    updated_at,
    completed_at,
    superseded_at,
    supersession_reason
FROM arena_assignments
WHERE id = sqlc.arg(id)
FOR UPDATE;

-- name: GetArenaAssignment :one
SELECT id,
    attempt_id,
    series_id,
    roster_id,
    plan_id,
    branch_id,
    reservation_id,
    snapshot_id,
    task_id,
    task_version,
    supersedes_assignment_id,
    state,
    revision,
    created_at,
    updated_at,
    completed_at,
    superseded_at,
    supersession_reason
FROM arena_assignments
WHERE id = sqlc.arg(id);

-- name: DiscloseArenaTaskReservationCAS :one
UPDATE arena_task_version_reservations
SET disclosed_at = sqlc.arg(disclosed_at)
WHERE id = sqlc.arg(id)
    AND state = 'committed'
    AND disclosed_at IS NULL
RETURNING id,
    edge_id,
    plan_id,
    branch_id,
    task_id,
    task_version,
    state,
    disclosed_at,
    committed_at,
    released_at,
    release_reason,
    superseded_at,
    supersession_reason,
    created_at;

-- name: CreateArenaTaskDeliveryReceipt :one
INSERT INTO arena_task_delivery_receipts (
    id,
    assignment_id,
    attempt_id,
    roster_id,
    participant_id,
    snapshot_id,
    task_id,
    task_version,
    delivered_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(assignment_id),
    sqlc.arg(attempt_id),
    sqlc.arg(roster_id),
    sqlc.arg(participant_id),
    sqlc.arg(snapshot_id),
    sqlc.arg(task_id),
    sqlc.arg(task_version),
    sqlc.arg(delivered_at),
    sqlc.arg(delivered_at)
)
RETURNING id,
    assignment_id,
    attempt_id,
    roster_id,
    participant_id,
    snapshot_id,
    task_id,
    task_version,
    delivered_at,
    created_at;

-- name: ListArenaTaskDeliveryReceipts :many
SELECT id,
    assignment_id,
    attempt_id,
    roster_id,
    participant_id,
    snapshot_id,
    task_id,
    task_version,
    delivered_at,
    created_at
FROM arena_task_delivery_receipts
WHERE assignment_id = sqlc.arg(assignment_id)
ORDER BY participant_id,
    delivered_at,
    id;

-- name: GetArenaTaskSnapshot :one
SELECT id,
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
FROM arena_task_snapshots
WHERE id = sqlc.arg(id);

-- name: SupersedeArenaAssignmentCAS :one
UPDATE arena_assignments
SET state = 'superseded',
    revision = revision + 1,
    updated_at = sqlc.arg(superseded_at),
    superseded_at = sqlc.arg(superseded_at),
    supersession_reason = sqlc.arg(supersession_reason)
WHERE id = sqlc.arg(id)
    AND revision = sqlc.arg(expected_revision)
    AND state = 'active'
RETURNING id,
    attempt_id,
    series_id,
    roster_id,
    plan_id,
    branch_id,
    reservation_id,
    snapshot_id,
    task_id,
    task_version,
    supersedes_assignment_id,
    state,
    revision,
    created_at,
    updated_at,
    completed_at,
    superseded_at,
    supersession_reason;
