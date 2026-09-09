-- name: CreateConservativeAssignmentPlan :one
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

-- name: CreateExactAssignmentPlan :one
INSERT INTO assignment_plans (
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

-- name: CreateAssignmentBranch :one
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

-- name: CreateAssignmentPlanEdge :one
-- Assignment plans are pool-scoped. The normal pool is intentionally shared
-- by Swiss, semifinal, and final workflows; stage authority is resolved before
-- a plan is persisted and is not a plan field.
WITH locked_task_version AS (
    SELECT task.id
    FROM assignment_plans AS plan
    JOIN tournament_content_configurations AS configuration
        ON configuration.tournament_id = plan.tournament_id
        AND configuration.state = 'published'
        AND plan.source_pool_revision_id IN (
            configuration.normal_pool_revision_id,
            configuration.golden_pool_revision_id
        )
    JOIN task_pool_version_memberships AS membership
        ON membership.task_pool_revision_id = plan.source_pool_revision_id
        AND membership.task_id = sqlc.arg(task_id)
        AND membership.task_version = sqlc.arg(task_version)
    JOIN tasks AS task ON task.id = membership.task_id
    LEFT JOIN LATERAL (
        SELECT attestation.healthy
        FROM task_version_health_attestations AS attestation
        WHERE attestation.task_id = membership.task_id
            AND attestation.task_version = membership.task_version
        ORDER BY attestation.revision DESC
        LIMIT 1
    ) AS health ON true
    WHERE plan.id = sqlc.arg(plan_id)
        AND task.enabled
        AND task.deleted_at IS NULL
        AND COALESCE(health.healthy, false)
    FOR NO KEY UPDATE OF task
)
INSERT INTO assignment_plan_edges (
    id,
    plan_id,
    branch_id,
    position,
    task_id,
    task_version,
    selection_evidence,
    created_at
)
SELECT
    sqlc.arg(id),
    sqlc.arg(plan_id),
    sqlc.arg(branch_id),
    sqlc.arg(position),
    sqlc.arg(task_id),
    sqlc.arg(task_version),
    sqlc.arg(selection_evidence),
    sqlc.arg(created_at)
FROM locked_task_version
RETURNING id,
    plan_id,
    branch_id,
    position,
    task_id,
    task_version,
    operator_reserve_command_id,
    selection_evidence,
    created_at;

-- name: CreateAssignmentTaskVersionReservation :one
INSERT INTO task_version_reservations (
    id,
    edge_id,
    plan_id,
    branch_id,
    contingency_draft_branch_id,
    task_id,
    task_version,
    state,
    created_at
)
SELECT
    sqlc.arg(id),
    sqlc.arg(edge_id),
    sqlc.arg(plan_id),
    sqlc.arg(branch_id),
    branch.exact_draft_branch_id,
    sqlc.arg(task_id),
    sqlc.arg(task_version),
    'reserved',
    sqlc.arg(created_at)
FROM assignment_branches AS branch
WHERE branch.id = sqlc.arg(branch_id)
    AND branch.plan_id = sqlc.arg(plan_id)
RETURNING id,
    edge_id,
    plan_id,
    branch_id,
    contingency_draft_branch_id,
    task_id,
    task_version,
    revision,
    state,
    disclosed_at,
    committed_at,
    released_at,
    release_reason,
    superseded_at,
    supersession_reason,
    created_at;

-- name: CreateAssignmentTaskSnapshot :one
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

-- name: LockAssignmentPlan :one
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
    active_draft_branch_id,
    committed_at,
    superseded_at,
    supersession_reason,
    created_at
FROM assignment_plans
WHERE id = sqlc.arg(id)
FOR UPDATE;

-- name: CommitAssignmentBranchReservations :many
UPDATE task_version_reservations
SET state = 'committed',
    committed_at = sqlc.arg(committed_at),
    revision = revision + 1
WHERE plan_id = sqlc.arg(plan_id)
    AND branch_id = sqlc.arg(branch_id)
    AND state = 'reserved'
RETURNING id,
    edge_id,
    plan_id,
    branch_id,
    task_id,
    task_version,
    revision,
    state,
    disclosed_at,
    committed_at,
    released_at,
    release_reason,
    superseded_at,
    supersession_reason,
    created_at;

-- name: ReleaseOtherAssignmentBranchReservations :many
UPDATE task_version_reservations
SET state = 'released',
    released_at = sqlc.arg(released_at),
    release_reason = sqlc.arg(release_reason),
    revision = revision + 1
WHERE plan_id = sqlc.arg(plan_id)
    AND branch_id <> sqlc.arg(active_branch_id)
    AND state = 'reserved'
RETURNING id;

-- name: ActivateAssignmentBranch :one
UPDATE assignment_branches
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

-- name: ReleaseOtherAssignmentBranches :many
UPDATE assignment_branches
SET state = 'released',
    released_at = sqlc.arg(released_at),
    release_reason = sqlc.arg(release_reason)
WHERE plan_id = sqlc.arg(plan_id)
    AND id <> sqlc.arg(active_branch_id)
    AND state = 'reserved'
RETURNING id;

-- name: CommitAssignmentPlanCAS :one
UPDATE assignment_plans
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

-- name: GetAssignmentPlan :one
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
FROM assignment_plans
WHERE id = sqlc.arg(id);

-- name: ListAssignmentBranches :many
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
FROM assignment_branches
WHERE plan_id = sqlc.arg(plan_id)
ORDER BY branch_key,
    id;

-- name: LockAssignmentDraftChildScope :many
SELECT child.id,
    child.plan_id,
    source.roster_id,
    source.series_id,
    draft_branch.id AS group_id,
    child.state AS child_state,
    draft_branch.state AS group_state
FROM assignment_branches AS child
JOIN exact_draft_assignment_branches AS draft_branch
    ON draft_branch.id = child.exact_draft_branch_id
    AND draft_branch.plan_id = child.plan_id
JOIN exact_draft_assignment_child_sources AS source
    ON source.child_branch_id = child.id
    AND source.plan_id = child.plan_id
JOIN assignment_plans AS plan
    ON plan.id = child.plan_id
    AND plan.roster_id = source.roster_id
    AND plan.tournament_id = source.tournament_id
WHERE child.id = sqlc.arg(branch_id)
    AND child.plan_id = sqlc.arg(plan_id)
    AND source.roster_id = sqlc.arg(roster_id)
    AND source.series_id = sqlc.arg(series_id)
FOR UPDATE OF child, draft_branch;

-- name: ListAssignmentPlanEdges :many
SELECT id,
    plan_id,
    branch_id,
    position,
    task_id,
    task_version,
    operator_reserve_command_id,
    selection_evidence,
    created_at
FROM assignment_plan_edges
WHERE plan_id = sqlc.arg(plan_id)
ORDER BY branch_id,
    position;

-- name: ListAssignmentTaskVersionReservations :many
SELECT id,
    edge_id,
    plan_id,
    branch_id,
    task_id,
    task_version,
    revision,
    state,
    disclosed_at,
    committed_at,
    released_at,
    release_reason,
    superseded_at,
    supersession_reason,
    created_at
FROM task_version_reservations
WHERE plan_id = sqlc.arg(plan_id)
ORDER BY branch_id,
    task_id,
    task_version;

-- name: ListAssignmentTaskSnapshotsForPlan :many
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
FROM task_snapshots AS snapshot
JOIN task_version_reservations AS reservation
    ON reservation.id = snapshot.reservation_id
WHERE reservation.plan_id = sqlc.arg(plan_id)
ORDER BY reservation.branch_id,
    snapshot.task_id,
    snapshot.task_version;

-- name: CreateAssignment :one
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

-- name: LockAssignment :one
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
FROM assignments
WHERE id = sqlc.arg(id)
FOR UPDATE;

-- name: GetAssignment :one
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
FROM assignments
WHERE id = sqlc.arg(id);

-- name: DiscloseAssignmentTaskReservationCAS :one
UPDATE task_version_reservations
SET disclosed_at = sqlc.arg(disclosed_at),
    revision = revision + 1
WHERE id = sqlc.arg(id)
    AND state = 'committed'
    AND disclosed_at IS NULL
RETURNING id,
    edge_id,
    plan_id,
    branch_id,
    task_id,
    task_version,
    revision,
    state,
    disclosed_at,
    committed_at,
    released_at,
    release_reason,
    superseded_at,
    supersession_reason,
    created_at;

-- name: CreateAssignmentTaskDeliveryReceipt :one
INSERT INTO task_delivery_receipts (
    id,
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
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(assignment_id),
    sqlc.arg(attempt_id),
    sqlc.arg(roster_id),
    sqlc.arg(participant_id),
    sqlc.arg(instance_id),
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
    instance_id,
    snapshot_id,
    task_id,
    task_version,
    delivered_at,
    created_at;

-- name: ListAssignmentTaskDeliveryReceipts :many
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
ORDER BY participant_id,
    delivered_at,
    id;

-- name: GetAssignmentTaskSnapshot :one
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
FROM task_snapshots
WHERE id = sqlc.arg(id);

-- name: SupersedeAssignmentCAS :one
UPDATE assignments
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
