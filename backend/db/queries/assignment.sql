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

-- Standalone exact-normal planning locks the published playoff authority and
-- snapshots every source document before it attempts a reservation.

-- name: LockExactNormalAssignmentStage :one
SELECT stage.command_id,
    target.tournament_id,
    target.roster_id,
    target.id AS series_id,
    target.first_participant_id,
    target.second_participant_id,
    target.revision AS series_revision,
    roster.revision AS roster_revision,
    category.id AS category_revision_id,
    category.revision AS category_revision,
    category.source_pool_revision_id,
    pool.revision AS pool_revision,
    slot.category,
    evidence.published_projection_revision_id,
    evidence.published_projection_revision,
    evidence.proof_digest AS graph_digest,
    bracket.payload_digest AS artifact_digest,
    stage.created_at
FROM tournament_stage_playoff_semifinals AS stage
INNER JOIN tournament_stage_playoff_evidence AS evidence
    ON evidence.command_id = stage.command_id
    AND evidence.tournament_id = stage.tournament_id
    AND evidence.roster_id = stage.roster_id
    AND evidence.bracket_artifact_id = stage.bracket_artifact_id
INNER JOIN series AS target
    ON target.id = stage.series_id
    AND target.tournament_id = stage.tournament_id
    AND target.roster_id = stage.roster_id
INNER JOIN rosters AS roster
    ON roster.id = stage.roster_id
    AND roster.tournament_id = stage.tournament_id
INNER JOIN game_slots AS slot
    ON slot.id = sqlc.arg(slot_id)
    AND slot.series_id = target.id
    AND slot.roster_id = target.roster_id
INNER JOIN category_revisions AS category
    ON category.id = sqlc.arg(category_lock_id)
    AND category.series_id = target.id
    AND category.roster_id = target.roster_id
INNER JOIN task_pool_revisions AS pool
    ON pool.id = category.source_pool_revision_id
    AND pool.kind = 'normal'
INNER JOIN projection_revisions AS projection
    ON projection.id = evidence.published_projection_revision_id
    AND projection.tournament_id = stage.tournament_id
    AND projection.roster_id = stage.roster_id
    AND projection.revision_number = evidence.published_projection_revision
    AND projection.state IN ('published', 'superseded')
INNER JOIN projection_artifacts AS bracket
    ON bracket.id = evidence.bracket_artifact_id
    AND bracket.tournament_id = stage.tournament_id
    AND bracket.roster_id = stage.roster_id
    AND bracket.produced_by_revision_id = projection.id
    AND bracket.artifact_kind = 'bracket'
WHERE stage.tournament_id = sqlc.arg(tournament_id)
    AND stage.roster_id = sqlc.arg(roster_id)
    AND target.id = sqlc.arg(series_id)
    AND target.format = 'bo1'
    AND target.state = 'locked'
FOR UPDATE OF stage, evidence, target, roster, slot, category, pool, projection, bracket;

-- Swiss random materialization runs before the pairing command ledger and
-- wave-start proof are written.  The initial score revision carries the real
-- command and projection lineage, while the automatic round carries the
-- immutable pairing decision; later ledgers revalidate both.
-- name: LockExactNormalAssignmentSwissStage :one
SELECT score.command_id,
    target.tournament_id,
    target.roster_id,
    target.id AS series_id,
    first_member.participant_id AS first_participant_id,
    second_member.participant_id AS second_participant_id,
    target.revision AS series_revision,
    roster.revision AS roster_revision,
    category.id AS category_revision_id,
    category.revision AS category_revision,
    category.source_pool_revision_id,
    pool.revision AS pool_revision,
    slot.category,
    projection.id AS published_projection_revision_id,
    projection.revision_number AS published_projection_revision,
    round.decision_replay_digest AS graph_digest,
    standings.payload_digest AS artifact_digest,
    score.created_at
FROM series_score_revisions AS score
INNER JOIN wave_series AS wave_series
    ON wave_series.series_id = score.series_id
    AND wave_series.tournament_id = score.tournament_id
    AND wave_series.roster_id = score.roster_id
INNER JOIN waves AS wave
    ON wave.id = wave_series.wave_id
    AND wave.tournament_id = score.tournament_id
    AND wave.roster_id = score.roster_id
INNER JOIN rosters AS roster
    ON roster.id = score.roster_id
    AND roster.tournament_id = score.tournament_id
INNER JOIN swiss_wave_links AS wave_link
    ON wave_link.wave_id = wave.id
    AND wave_link.tournament_id = wave.tournament_id
    AND wave_link.roster_id = wave.roster_id
INNER JOIN swiss_rounds AS round
    ON round.id = wave_link.round_id
    AND round.roster_id = wave_link.roster_id
    AND round.source_roster_revision = roster.revision
    AND round.generation_kind = 'automatic'
    AND round.decision_replay_digest IS NOT NULL
    AND octet_length(round.decision_replay_digest) = 32
INNER JOIN swiss_pairings AS pairing
    ON pairing.round_id = round.id
    AND pairing.roster_id = round.roster_id
INNER JOIN swiss_pairing_members AS first_member
    ON first_member.pairing_id = pairing.id
    AND first_member.round_id = pairing.round_id
    AND first_member.roster_id = pairing.roster_id
    AND first_member.seat = 1
INNER JOIN swiss_pairing_members AS second_member
    ON second_member.pairing_id = pairing.id
    AND second_member.round_id = pairing.round_id
    AND second_member.roster_id = pairing.roster_id
    AND second_member.seat = 2
INNER JOIN series AS target
    ON target.tournament_id = score.tournament_id
    AND target.roster_id = score.roster_id
    AND target.first_participant_id = first_member.participant_id
    AND target.second_participant_id = second_member.participant_id
INNER JOIN game_slots AS slot
    ON slot.id = sqlc.arg(slot_id)
    AND slot.series_id = target.id
    AND slot.roster_id = target.roster_id
    AND slot.slot_number = 1
INNER JOIN category_revisions AS category
    ON category.id = sqlc.arg(category_lock_id)
    AND category.series_id = target.id
    AND category.roster_id = target.roster_id
    AND category.selected_categories @> jsonb_build_array(slot.category)
INNER JOIN task_pool_revisions AS pool
    ON pool.id = category.source_pool_revision_id
    AND pool.kind = 'normal'
INNER JOIN projection_revisions AS projection
    ON projection.id = score.source_projection_revision_id
    AND projection.tournament_id = score.tournament_id
    AND projection.roster_id = score.roster_id
    AND projection.revision_number = score.source_projection_revision
    AND projection.state IN ('published', 'superseded')
INNER JOIN projection_revision_artifacts AS revision_artifact
    ON revision_artifact.revision_id = projection.id
    AND revision_artifact.tournament_id = projection.tournament_id
    AND revision_artifact.roster_id = projection.roster_id
    AND revision_artifact.artifact_kind = 'standings'
INNER JOIN projection_artifacts AS standings
    ON standings.id = revision_artifact.artifact_id
    AND standings.tournament_id = projection.tournament_id
    AND standings.roster_id = projection.roster_id
    AND standings.artifact_kind = 'standings'
WHERE score.tournament_id = sqlc.arg(tournament_id)
    AND score.roster_id = sqlc.arg(roster_id)
    AND score.series_id = sqlc.arg(series_id)
    AND score.revision_number = 1
    AND score.operation = 'initialize'
    AND target.id = sqlc.arg(series_id)
    AND target.format = 'bo1'
    AND target.state = 'locked'
    AND category.mode = 'random'
FOR UPDATE OF score, wave_series, wave, wave_link, round, pairing, first_member, second_member,
    target, roster, slot, category, pool, projection, revision_artifact, standings;

-- name: EnsureExactNormalAssignmentHistoryHead :exec
INSERT INTO exact_normal_assignment_history_heads (
    tournament_id, roster_id, series_id, slot_id
)
VALUES (
    sqlc.arg(tournament_id), sqlc.arg(roster_id), sqlc.arg(series_id), sqlc.arg(slot_id)
)
ON CONFLICT (tournament_id, roster_id, series_id, slot_id) DO NOTHING;

-- name: LockExactNormalAssignmentHistoryHead :one
SELECT revision_id, revision, created_at, updated_at
FROM exact_normal_assignment_history_heads
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND series_id = sqlc.arg(series_id)
    AND slot_id = sqlc.arg(slot_id)
FOR UPDATE;

-- name: LockExactNormalAssignmentParticipants :many
SELECT participant.id AS participant_id,
    participant.player_id,
    reservation.reservation_id,
    reservation.tournament_id,
    reservation.revision AS reservation_revision,
    reservation.acquired_at,
    reservation.updated_at
FROM series AS series
INNER JOIN participants AS participant
    ON participant.id IN (series.first_participant_id, series.second_participant_id)
    AND participant.roster_id = series.roster_id
INNER JOIN participant_reservations AS reservation
    ON reservation.player_id = participant.player_id
    AND reservation.tournament_id = series.tournament_id
WHERE series.tournament_id = sqlc.arg(tournament_id)
    AND series.roster_id = sqlc.arg(roster_id)
    AND series.id = sqlc.arg(series_id)
ORDER BY participant.id
FOR UPDATE OF participant, reservation;

-- name: LockExactNormalAssignmentHistory :many
SELECT receipt.participant_id, receipt.task_id
FROM task_delivery_receipts AS receipt
INNER JOIN assignments AS assignment ON assignment.id = receipt.assignment_id
INNER JOIN series AS series
    ON series.id = assignment.series_id
    AND series.roster_id = assignment.roster_id
WHERE series.tournament_id = sqlc.arg(tournament_id)
    AND series.roster_id = sqlc.arg(roster_id)
    AND series.id = sqlc.arg(series_id)
    AND receipt.participant_id IN (series.first_participant_id, series.second_participant_id)
ORDER BY receipt.participant_id, receipt.task_id
FOR KEY SHARE OF receipt;

-- name: LockExactNormalAssignmentCandidates :many
SELECT membership.task_id,
    membership.task_version,
    pool.id AS pool_revision_id,
    pool.revision AS pool_revision,
    task_version.title,
    task_version.description,
    task_version.category,
    task_version.difficulty,
    task_version.time_limit,
    task_version.flag,
    task_version.hint_1,
    task_version.hint_2,
    task_version.hint_3,
    task_version.task_url,
    task_version.source_file_url,
    task.created_at AS task_created_at,
    EXISTS (
        SELECT 1
        FROM task_version_reservations AS reservation
        WHERE reservation.task_id = membership.task_id
            AND reservation.task_version = membership.task_version
            AND reservation.state IN ('reserved', 'committed')
    ) AS unavailable
FROM category_revisions AS category
INNER JOIN task_pool_revisions AS pool
    ON pool.id = category.source_pool_revision_id
    AND pool.kind = 'normal'
INNER JOIN task_pool_version_memberships AS membership
    ON membership.task_pool_revision_id = pool.id
INNER JOIN task_versions AS task_version
    ON task_version.task_id = membership.task_id
    AND task_version.version = membership.task_version
INNER JOIN tasks AS task
    ON task.id = membership.task_id
    AND task.kind = 'normal'
    AND task.enabled
    AND task.deleted_at IS NULL
LEFT JOIN LATERAL (
    SELECT attestation.healthy
    FROM task_version_health_attestations AS attestation
    WHERE attestation.task_id = membership.task_id
        AND attestation.task_version = membership.task_version
    ORDER BY attestation.revision DESC
    LIMIT 1
) AS health ON true
WHERE category.id = sqlc.arg(category_lock_id)
    AND category.series_id = sqlc.arg(series_id)
    AND category.roster_id = sqlc.arg(roster_id)
    AND COALESCE(health.healthy, false)
    AND NOT EXISTS (
        SELECT 1
        FROM task_version_reservations AS live_reservation
        WHERE live_reservation.task_id = membership.task_id
            AND live_reservation.task_version = membership.task_version
            AND live_reservation.state IN ('reserved', 'committed')
    )
ORDER BY membership.task_id, membership.task_version
FOR UPDATE OF membership, task_version, task;

-- name: CreateExactNormalAssignmentPlan :exec
INSERT INTO assignment_plans (
    id, tournament_id, roster_id, kind, parent_plan_id, revision_id,
    source_roster_revision, source_pool_revision_id, reachable_branch_count,
    constraint_graph, proof_evidence, proof_hash, decision_evidence_id,
    decision_algorithm_version, decision_inputs, decision_seed,
    decision_result, decision_replay_digest, decision_owner_id, decided_at,
    state, created_at
)
VALUES (
    sqlc.arg(id), sqlc.arg(tournament_id), sqlc.arg(roster_id), 'exact_normal',
    NULL, sqlc.arg(revision_id), sqlc.arg(source_roster_revision),
    sqlc.arg(source_pool_revision_id), 1, sqlc.arg(constraint_graph),
    sqlc.arg(proof_evidence), sqlc.arg(proof_hash), sqlc.arg(decision_evidence_id),
    sqlc.arg(decision_algorithm_version), sqlc.arg(decision_inputs),
    sqlc.arg(decision_seed), sqlc.arg(decision_result),
    sqlc.arg(decision_replay_digest), sqlc.arg(decision_owner_id),
    sqlc.arg(decided_at), 'planned', sqlc.arg(created_at)
);

-- name: CreateExactNormalAssignmentBranch :exec
INSERT INTO assignment_branches (
    id, plan_id, draft_id, draft_revision_id, branch_key, category_sequence,
    state, created_at
)
VALUES (
    sqlc.arg(id), sqlc.arg(plan_id), sqlc.narg(draft_id)::uuid,
    sqlc.narg(draft_revision_id)::uuid, sqlc.arg(branch_key),
    sqlc.arg(category_sequence), 'reserved', sqlc.arg(created_at)
);

-- name: CreateExactNormalAssignmentSource :exec
INSERT INTO exact_normal_assignment_sources (
    plan_id, tournament_id, roster_id, series_id, slot_id, category_lock_id,
    category, series_revision, pool_revision_id, pool_revision,
    history_revision_id, history_revision, roster_revision,
    artifact_revision_id, artifact_revision, category_revision_id,
    category_revision, pool, participant_ids, participant_reservations,
    history, candidates, graph_digest, artifact_digest, proof_hash, created_at
)
VALUES (
    sqlc.arg(plan_id), sqlc.arg(tournament_id), sqlc.arg(roster_id),
    sqlc.arg(series_id), sqlc.arg(slot_id), sqlc.arg(category_lock_id),
    sqlc.arg(category), sqlc.arg(series_revision), sqlc.arg(pool_revision_id),
    sqlc.arg(pool_revision), sqlc.arg(history_revision_id),
    sqlc.arg(history_revision), sqlc.arg(roster_revision),
    sqlc.arg(artifact_revision_id), sqlc.arg(artifact_revision),
    sqlc.arg(category_revision_id), sqlc.arg(category_revision),
    sqlc.arg(pool), sqlc.arg(participant_ids), sqlc.arg(participant_reservations),
    sqlc.arg(history), sqlc.arg(candidates), sqlc.arg(graph_digest),
    sqlc.arg(artifact_digest), sqlc.arg(proof_hash), sqlc.arg(created_at)
);

-- name: LockExactNormalAssignmentSource :one
SELECT source.plan_id,
    plan.revision_id AS plan_revision_id,
    plan.state,
    source.tournament_id,
    source.roster_id,
    source.series_id,
    source.slot_id,
    source.category_lock_id,
    source.category,
    source.series_revision,
    source.pool_revision_id,
    source.pool_revision,
    source.history_revision_id,
    source.history_revision,
    source.roster_revision,
    source.artifact_revision_id,
    source.artifact_revision,
    source.category_revision_id,
    source.category_revision,
    source.pool,
    source.participant_ids,
    source.participant_reservations,
    source.history,
    source.candidates,
    source.graph_digest,
    source.artifact_digest,
    source.proof_hash,
    source.created_at
FROM exact_normal_assignment_sources AS source
INNER JOIN assignment_plans AS plan ON plan.id = source.plan_id
WHERE source.plan_id = sqlc.arg(plan_id)
FOR UPDATE OF source, plan;

-- name: FindExactNormalAssignmentSourceByScope :one
SELECT source.plan_id,
    plan.revision_id AS plan_revision_id,
    plan.state,
    source.proof_hash,
    source.created_at
FROM exact_normal_assignment_sources AS source
INNER JOIN assignment_plans AS plan ON plan.id = source.plan_id
WHERE source.tournament_id = sqlc.arg(tournament_id)
    AND source.roster_id = sqlc.arg(roster_id)
    AND source.series_id = sqlc.arg(series_id)
    AND source.slot_id = sqlc.arg(slot_id)
    AND source.category_lock_id = sqlc.arg(category_lock_id)
FOR UPDATE OF source, plan;

-- name: CommitExactNormalAssignmentPlanCAS :one
UPDATE assignment_plans
SET state = 'committed',
    active_branch_id = sqlc.arg(active_branch_id),
    committed_at = sqlc.arg(committed_at)
WHERE id = sqlc.arg(id)
    AND kind = 'exact_normal'
    AND state = 'planned'
    AND revision_id = sqlc.arg(expected_revision_id)
    AND source_roster_revision = sqlc.arg(expected_roster_revision)
RETURNING id, revision_id, state, active_branch_id, committed_at;
