-- Exact draft assignment persistence keeps every reachable draft branch and
-- every per-category normal assignment reservation. It is intentionally
-- separate from assignment.sql because one draft branch owns three ordinary
-- assignment branches, not one collapsed JSON payload.

-- name: LockExactDraftPlanningStage :one
SELECT stage.command_id,
    stage.tournament_id,
    stage.roster_id,
    stage.final_series_id,
    stage.category_revision_id,
    stage.draft_id,
    stage.draft_initial_revision_id,
    stage.first_participant_id,
    stage.second_participant_id,
    final_series.format AS series_format,
    final_series.revision AS series_revision,
    roster.revision AS roster_revision,
    category.revision AS category_revision,
    category.source_pool_revision_id,
    pool.revision AS pool_revision,
    evidence.published_projection_revision_id,
    evidence.published_projection_revision,
    evidence.proof_digest AS graph_digest,
    bracket.payload_digest AS artifact_digest,
    current_draft_revision.id AS current_draft_revision_id,
    current_draft_revision.revision AS current_draft_revision,
    current_draft_revision.state AS current_draft_state,
    stage.created_at
FROM tournament_stage_playoff_finals AS stage
INNER JOIN tournament_stage_playoff_evidence AS evidence
    ON evidence.command_id = stage.command_id
    AND evidence.tournament_id = stage.tournament_id
    AND evidence.roster_id = stage.roster_id
INNER JOIN series AS final_series
    ON final_series.id = stage.final_series_id
    AND final_series.tournament_id = stage.tournament_id
    AND final_series.roster_id = stage.roster_id
INNER JOIN rosters AS roster
    ON roster.id = stage.roster_id
    AND roster.tournament_id = stage.tournament_id
INNER JOIN category_revisions AS category
    ON category.id = stage.category_revision_id
    AND category.series_id = final_series.id
    AND category.roster_id = stage.roster_id
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
INNER JOIN projection_revision_artifacts AS source_membership
    ON source_membership.revision_id = projection.id
    AND source_membership.tournament_id = stage.tournament_id
    AND source_membership.roster_id = stage.roster_id
    AND source_membership.artifact_kind = 'bracket'
    AND source_membership.artifact_id = bracket.id
INNER JOIN projection_revisions AS current_projection
    ON current_projection.tournament_id = stage.tournament_id
    AND current_projection.roster_id = stage.roster_id
    AND current_projection.state = 'published'
    AND current_projection.revision_number >= projection.revision_number
INNER JOIN projection_revision_artifacts AS current_membership
    ON current_membership.revision_id = current_projection.id
    AND current_membership.tournament_id = stage.tournament_id
    AND current_membership.roster_id = stage.roster_id
    AND current_membership.artifact_kind = 'bracket'
    AND current_membership.artifact_id = bracket.id
INNER JOIN LATERAL (
    SELECT revision.id,
        revision.revision,
        revision.state
    FROM draft_revisions AS revision
    WHERE revision.draft_id = stage.draft_id
    ORDER BY revision.revision DESC
    LIMIT 1
    FOR UPDATE
) AS current_draft_revision ON true
WHERE stage.draft_id = sqlc.arg(draft_id)
    AND final_series.state = 'planned'
    AND final_series.format = 'bo3'
    AND current_draft_revision.state = 'active'
FOR UPDATE OF stage, evidence, final_series, roster, category, pool, projection, bracket,
    source_membership, current_projection, current_membership;

-- name: LockExactDraftPlanningParticipants :many
SELECT participant.id AS participant_id,
    participant.player_id,
    reservation.reservation_id,
    reservation.tournament_id,
    reservation.revision AS reservation_revision,
    reservation.acquired_at,
    reservation.updated_at
FROM tournament_stage_playoff_finals AS stage
INNER JOIN participants AS participant
    ON participant.id IN (stage.first_participant_id, stage.second_participant_id)
    AND participant.roster_id = stage.roster_id
INNER JOIN participant_reservations AS reservation
    ON reservation.player_id = participant.player_id
    AND reservation.tournament_id = stage.tournament_id
WHERE stage.draft_id = sqlc.arg(draft_id)
ORDER BY participant.id
FOR UPDATE OF participant, reservation;

-- name: LockExactDraftPlanningHistory :many
SELECT receipt.participant_id,
    receipt.task_id,
    receipt.task_version
FROM tournament_stage_playoff_finals AS stage
INNER JOIN task_delivery_receipts AS receipt
    ON receipt.roster_id = stage.roster_id
    AND receipt.participant_id IN (stage.first_participant_id, stage.second_participant_id)
WHERE stage.draft_id = sqlc.arg(draft_id)
ORDER BY receipt.participant_id, receipt.task_id, receipt.task_version
FOR KEY SHARE OF receipt;

-- name: EnsureExactDraftPlanningHistoryHead :exec
INSERT INTO final_draft_delivery_history_heads (
    draft_id,
    tournament_id,
    roster_id,
    first_participant_id,
    second_participant_id,
    revision_id,
    revision,
    created_at,
    updated_at
)
SELECT stage.draft_id,
    stage.tournament_id,
    stage.roster_id,
    stage.first_participant_id,
    stage.second_participant_id,
    gen_random_uuid(),
    1,
    clock_timestamp(),
    clock_timestamp()
FROM tournament_stage_playoff_finals AS stage
WHERE stage.draft_id = sqlc.arg(draft_id)
ON CONFLICT (draft_id) DO NOTHING;

-- name: LockExactDraftPlanningHistoryHead :one
SELECT head.revision_id,
    head.revision
FROM final_draft_delivery_history_heads AS head
INNER JOIN tournament_stage_playoff_finals AS stage
    ON stage.draft_id = head.draft_id
    AND stage.tournament_id = head.tournament_id
    AND stage.roster_id = head.roster_id
    AND stage.first_participant_id = head.first_participant_id
    AND stage.second_participant_id = head.second_participant_id
WHERE head.draft_id = sqlc.arg(draft_id)
FOR UPDATE OF head;

-- name: LockExactDraftPlanningReservationKeys :exec
SELECT pg_advisory_xact_lock(hashtextextended(
    stage.tournament_id::TEXT || ':' || membership.task_id::TEXT || ':' || membership.task_version::TEXT,
    0
))
FROM tournament_stage_playoff_finals AS stage
JOIN category_revisions AS category ON category.id = stage.category_revision_id
    AND category.series_id = stage.final_series_id AND category.roster_id = stage.roster_id
JOIN task_pool_version_memberships AS membership ON membership.task_pool_revision_id = category.source_pool_revision_id
WHERE stage.draft_id = sqlc.arg(draft_id)
ORDER BY membership.task_id, membership.task_version;

-- name: LockExactDraftPlanningCandidates :many
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
        SELECT 1 FROM task_version_reservations AS reservation
        WHERE reservation.tournament_id = stage.tournament_id
            AND reservation.task_id = membership.task_id
            AND reservation.task_version = membership.task_version
            AND reservation.state IN ('reserved', 'committed')
            AND reservation.plan_id <> sqlc.arg(plan_id)
    ) AS unavailable
FROM tournament_stage_playoff_finals AS stage
INNER JOIN category_revisions AS category
    ON category.id = stage.category_revision_id
    AND category.series_id = stage.final_series_id
    AND category.roster_id = stage.roster_id
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
WHERE stage.draft_id = sqlc.arg(draft_id)
    AND COALESCE(health.healthy, false)
    AND NOT EXISTS (
        SELECT 1
        FROM task_public_exposures AS exposure
        WHERE exposure.task_id = membership.task_id
            AND exposure.task_version = membership.task_version
    )
ORDER BY membership.task_id, membership.task_version
-- The health-attestation guard takes FOR UPDATE on task_version. This exact
-- planning lock therefore serializes the candidate snapshot with every
-- health transition until the plan has been persisted or rejected.
FOR UPDATE OF membership, task_version, task;

-- name: CreateExactDraftAssignmentPlan :exec
INSERT INTO assignment_plans (
    id,
    tournament_id,
    roster_id,
    kind,
    reserve_count,
    parent_plan_id,
    revision_id,
    source_roster_revision,
    source_pool_revision_id,
    source_draft_revision_id,
    reachable_branch_count,
    constraint_graph,
    proof_evidence,
    proof_hash,
    state,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    'exact_draft',
    sqlc.arg(reserve_count),
    NULL,
    sqlc.arg(revision_id),
    sqlc.arg(source_roster_revision),
    sqlc.arg(source_pool_revision_id),
    sqlc.arg(source_draft_revision_id),
    sqlc.arg(reachable_branch_count),
    sqlc.arg(constraint_graph),
    sqlc.arg(proof_evidence),
    sqlc.arg(proof_hash),
    'planned',
    sqlc.arg(created_at)
);

-- name: CreateExactDraftAssignmentBranch :exec
INSERT INTO exact_draft_assignment_branches (
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
);

-- name: CreateExactDraftAssignmentChild :exec
INSERT INTO assignment_branches (
    id,
    plan_id,
    draft_id,
    draft_revision_id,
    branch_key,
    category_sequence,
    exact_draft_branch_id,
    exact_draft_position,
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
    sqlc.arg(plan_id),
    sqlc.arg(draft_id),
    sqlc.arg(draft_revision_id),
    sqlc.arg(branch_key),
    sqlc.arg(category_sequence),
    sqlc.arg(exact_draft_branch_id),
    sqlc.arg(exact_draft_position),
    sqlc.arg(decision_evidence_id),
    sqlc.arg(decision_algorithm_version),
    sqlc.arg(decision_inputs),
    sqlc.arg(decision_seed),
    sqlc.arg(decision_result),
    sqlc.arg(decision_replay_digest),
    sqlc.arg(decision_owner_id),
    sqlc.arg(decided_at),
    'reserved',
    sqlc.arg(created_at)
);

-- name: CreateExactDraftAssignmentChildSource :exec
INSERT INTO exact_draft_assignment_child_sources (
    child_branch_id,
    plan_id,
    tournament_id,
    roster_id,
    series_id,
    slot_id,
    category_lock_id,
    series_revision,
    pool_revision_id,
    pool_revision,
    history_revision_id,
    history_revision,
    roster_revision,
    artifact_revision_id,
    artifact_revision,
    category_revision_id,
    category_revision,
    graph_digest,
    artifact_digest,
    proof_hash,
    created_at
)
VALUES (
    sqlc.arg(child_branch_id),
    sqlc.arg(plan_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(slot_id),
    sqlc.arg(category_lock_id),
    sqlc.arg(series_revision),
    sqlc.arg(pool_revision_id),
    sqlc.arg(pool_revision),
    sqlc.arg(history_revision_id),
    sqlc.arg(history_revision),
    sqlc.arg(roster_revision),
    sqlc.arg(artifact_revision_id),
    sqlc.arg(artifact_revision),
    sqlc.arg(category_revision_id),
    sqlc.arg(category_revision),
    sqlc.arg(graph_digest),
    sqlc.arg(artifact_digest),
    sqlc.arg(proof_hash),
    sqlc.arg(created_at)
);

-- name: CreateExactDraftAssignmentChildParticipant :exec
INSERT INTO exact_draft_assignment_child_participants (
    child_branch_id,
    plan_id,
    participant_id,
    player_id,
    reservation_id,
    tournament_id,
    reservation_revision,
    acquired_at,
    updated_at,
    created_at
)
VALUES (
    sqlc.arg(child_branch_id),
    sqlc.arg(plan_id),
    sqlc.arg(participant_id),
    sqlc.arg(player_id),
    sqlc.arg(reservation_id),
    sqlc.arg(tournament_id),
    sqlc.arg(reservation_revision),
    sqlc.arg(acquired_at),
    sqlc.arg(updated_at),
    sqlc.arg(created_at)
);

-- name: CreateExactDraftAssignmentChildHistory :exec
INSERT INTO exact_draft_assignment_child_history (
    child_branch_id,
    plan_id,
    participant_id,
    task_id,
    task_version,
    created_at
)
VALUES (
    sqlc.arg(child_branch_id),
    sqlc.arg(plan_id),
    sqlc.arg(participant_id),
    sqlc.arg(task_id),
    sqlc.arg(task_version),
    sqlc.arg(created_at)
);

-- name: CreateExactDraftAssignmentChildCandidate :exec
INSERT INTO exact_draft_assignment_child_candidates (
    child_branch_id,
    plan_id,
    task_id,
    task_version,
    pool_revision_id,
    created_at
)
VALUES (
    sqlc.arg(child_branch_id),
    sqlc.arg(plan_id),
    sqlc.arg(task_id),
    sqlc.arg(task_version),
    sqlc.arg(pool_revision_id),
    sqlc.arg(created_at)
);

-- name: LockExactDraftAssignmentPlan :one
SELECT plan.id,
    plan.tournament_id,
    plan.roster_id,
    plan.revision_id,
    plan.source_roster_revision,
    plan.source_pool_revision_id,
    plan.source_draft_revision_id,
    plan.reachable_branch_count,
    plan.constraint_graph,
    plan.proof_evidence,
    plan.proof_hash,
    plan.state,
    plan.active_branch_id,
    plan.active_draft_branch_id,
    plan.completion_draft_revision_id,
    plan.completion_draft_revision,
    plan.activation_command_id,
    plan.completed_categories,
    plan.committed_at,
    plan.created_at
FROM assignment_plans AS plan
WHERE plan.id = sqlc.arg(plan_id)
    AND plan.kind = 'exact_draft'
FOR UPDATE;

-- name: LockExactDraftAssignmentSource :one
SELECT plan.id AS plan_id,
    plan.revision_id AS plan_revision_id,
    plan.tournament_id,
    plan.roster_id,
    plan.source_roster_revision,
    plan.source_pool_revision_id,
    plan.source_draft_revision_id,
    plan.proof_hash,
    plan.state AS plan_state,
    draft.id AS draft_id,
    stage.command_id AS stage_command_id,
    stage.final_series_id,
    final_series.format AS series_format,
    final_series.revision AS final_series_revision,
    roster.revision AS roster_revision,
    current_revision.id AS current_draft_revision_id,
    current_revision.revision AS current_draft_revision,
    current_revision.state AS current_draft_state,
    current_revision.selected_categories
FROM assignment_plans AS plan
INNER JOIN drafts AS draft
    ON draft.id = (
        SELECT source.draft_id
        FROM draft_revisions AS source
        WHERE source.id = plan.source_draft_revision_id
    )
INNER JOIN tournament_stage_playoff_finals AS stage
    ON stage.draft_id = draft.id
    AND stage.tournament_id = plan.tournament_id
    AND stage.roster_id = plan.roster_id
INNER JOIN series AS final_series
    ON final_series.id = stage.final_series_id
    AND final_series.tournament_id = stage.tournament_id
    AND final_series.roster_id = stage.roster_id
INNER JOIN rosters AS roster
    ON roster.id = stage.roster_id
    AND roster.tournament_id = stage.tournament_id
INNER JOIN LATERAL (
    SELECT revision.id,
        revision.revision,
        revision.state,
        revision.selected_categories
    FROM draft_revisions AS revision
    WHERE revision.draft_id = draft.id
    ORDER BY revision.revision DESC
    LIMIT 1
    FOR UPDATE
) AS current_revision ON true
WHERE plan.id = sqlc.arg(plan_id)
    AND plan.kind = 'exact_draft'
FOR UPDATE OF plan, draft, stage, final_series, roster;

-- name: LockExactDraftAssignmentBranches :many
SELECT draft_branch.id,
    draft_branch.plan_id,
    draft_branch.draft_id,
    draft_branch.draft_revision_id,
    draft_branch.branch_key,
    draft_branch.category_sequence,
    draft_branch.state,
    draft_branch.activated_at,
    draft_branch.released_at,
    draft_branch.release_reason,
    draft_branch.created_at
FROM exact_draft_assignment_branches AS draft_branch
WHERE draft_branch.plan_id = sqlc.arg(plan_id)
ORDER BY draft_branch.branch_key
FOR UPDATE;

-- name: LockExactDraftAssignmentChildren :many
SELECT child.id,
    child.plan_id,
    child.draft_id,
    child.draft_revision_id,
    child.branch_key,
    child.category_sequence,
    child.exact_draft_branch_id,
    child.exact_draft_position,
    child.decision_evidence_id,
    child.decision_algorithm_version,
    child.decision_inputs,
    child.decision_seed,
    child.decision_result,
    child.decision_replay_digest,
    child.decision_owner_id,
    child.decided_at,
    child.state,
    child.activated_at,
    child.released_at,
    child.release_reason,
    child.created_at
FROM assignment_branches AS child
WHERE child.plan_id = sqlc.arg(plan_id)
    AND child.exact_draft_branch_id IS NOT NULL
ORDER BY child.exact_draft_branch_id, child.exact_draft_position
FOR UPDATE;

-- name: LockExactDraftAssignmentChildSources :many
SELECT source.child_branch_id,
    source.plan_id,
    source.tournament_id,
    source.roster_id,
    source.series_id,
    source.slot_id,
    source.category_lock_id,
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
    source.graph_digest,
    source.artifact_digest,
    source.proof_hash,
    source.created_at
FROM exact_draft_assignment_child_sources AS source
WHERE source.plan_id = sqlc.arg(plan_id)
ORDER BY source.child_branch_id
FOR UPDATE;

-- name: LockExactDraftAssignmentChildParticipants :many
SELECT participant.child_branch_id,
    participant.plan_id,
    participant.participant_id,
    participant.player_id,
    participant.reservation_id,
    participant.tournament_id,
    participant.reservation_revision,
    participant.acquired_at,
    participant.updated_at,
    participant.created_at
FROM exact_draft_assignment_child_participants AS participant
WHERE participant.plan_id = sqlc.arg(plan_id)
ORDER BY participant.child_branch_id, participant.participant_id
FOR UPDATE;

-- name: LockExactDraftAssignmentChildHistory :many
SELECT history.child_branch_id,
    history.plan_id,
    history.participant_id,
    history.task_id,
    history.created_at,
    history.task_version
FROM exact_draft_assignment_child_history AS history
WHERE history.plan_id = sqlc.arg(plan_id)
ORDER BY history.child_branch_id, history.participant_id, history.task_id, history.task_version
FOR UPDATE;

-- name: LockExactDraftAssignmentChildCandidates :many
SELECT candidate.child_branch_id,
    candidate.plan_id,
    candidate.task_id,
    candidate.task_version,
    candidate.pool_revision_id,
    candidate.created_at
FROM exact_draft_assignment_child_candidates AS candidate
WHERE candidate.plan_id = sqlc.arg(plan_id)
ORDER BY candidate.child_branch_id, candidate.task_id, candidate.task_version
FOR UPDATE;

-- name: CommitExactDraftChildReservations :many
UPDATE task_version_reservations AS reservation
SET state = 'committed',
    committed_at = sqlc.arg(committed_at),
    revision = reservation.revision + 1
FROM assignment_branches AS child
WHERE child.id = reservation.branch_id
    AND child.plan_id = reservation.plan_id
    AND child.plan_id = sqlc.arg(plan_id)
    AND child.exact_draft_branch_id = sqlc.arg(exact_draft_branch_id)
    AND reservation.state = 'reserved'
RETURNING reservation.id;

-- name: ReleaseLosingExactDraftChildReservations :many
UPDATE task_version_reservations AS reservation
SET state = 'released',
    released_at = sqlc.arg(released_at),
    release_reason = sqlc.arg(release_reason),
    revision = reservation.revision + 1
FROM assignment_branches AS child
WHERE child.id = reservation.branch_id
    AND child.plan_id = reservation.plan_id
    AND child.plan_id = sqlc.arg(plan_id)
    AND child.exact_draft_branch_id <> sqlc.arg(exact_draft_branch_id)
    AND reservation.state = 'reserved'
RETURNING reservation.id;

-- name: ActivateExactDraftChildren :many
UPDATE assignment_branches
SET state = 'active',
    activated_at = sqlc.arg(activated_at)
WHERE plan_id = sqlc.arg(plan_id)
    AND exact_draft_branch_id = sqlc.arg(exact_draft_branch_id)
    AND state = 'reserved'
RETURNING id;

-- name: ReleaseLosingExactDraftChildren :many
UPDATE assignment_branches
SET state = 'released',
    released_at = sqlc.arg(released_at),
    release_reason = sqlc.arg(release_reason)
WHERE plan_id = sqlc.arg(plan_id)
    AND exact_draft_branch_id <> sqlc.arg(exact_draft_branch_id)
    AND state = 'reserved'
RETURNING id;

-- name: ActivateExactDraftBranch :execrows
UPDATE exact_draft_assignment_branches
SET state = 'active',
    activated_at = sqlc.arg(activated_at)
WHERE id = sqlc.arg(exact_draft_branch_id)
    AND plan_id = sqlc.arg(plan_id)
    AND state = 'reserved';

-- name: ReleaseLosingExactDraftBranches :execrows
UPDATE exact_draft_assignment_branches
SET state = 'released',
    released_at = sqlc.arg(released_at),
    release_reason = sqlc.arg(release_reason)
WHERE plan_id = sqlc.arg(plan_id)
    AND id <> sqlc.arg(exact_draft_branch_id)
    AND state = 'reserved';

-- name: CommitExactDraftAssignmentPlan :execrows
UPDATE assignment_plans AS plan
SET state = 'committed',
    active_branch_id = sqlc.arg(active_child_branch_id),
    active_draft_branch_id = sqlc.arg(active_draft_branch_id),
    completion_draft_revision_id = sqlc.arg(completion_draft_revision_id),
    completion_draft_revision = sqlc.arg(completion_draft_revision),
    activation_command_id = sqlc.arg(activation_command_id),
    completed_categories = sqlc.arg(completed_categories),
    committed_at = sqlc.arg(committed_at)
WHERE plan.id = sqlc.arg(plan_id)
    AND plan.kind = 'exact_draft'
    AND plan.revision_id = sqlc.arg(expected_plan_revision_id)
    AND plan.source_draft_revision_id = sqlc.arg(expected_draft_revision_id)
    AND plan.state = 'planned'
    AND EXISTS (
        SELECT 1
        FROM draft_revisions AS source
        INNER JOIN draft_revisions AS completion
            ON completion.draft_id = source.draft_id
        WHERE source.id = plan.source_draft_revision_id
            AND completion.id = sqlc.arg(completion_draft_revision_id)
            AND completion.revision = sqlc.arg(completion_draft_revision)
            AND completion.state = 'completed'
            AND completion.selected_categories = sqlc.arg(completed_categories)
            AND NOT EXISTS (
                SELECT 1
                FROM draft_revisions AS later
                WHERE later.draft_id = completion.draft_id
                    AND later.revision > completion.revision
            )
    );
