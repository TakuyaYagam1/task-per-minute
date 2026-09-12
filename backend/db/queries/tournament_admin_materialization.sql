-- Materialized Swiss categories are immutable executable authority.  The
-- selected categories and their lock evidence live on the category revision so
-- exact-normal assignment planning can bind to the same row in one tx.
-- name: CreateSeriesPresence :one
INSERT INTO presence_states (
    id,
    tournament_id,
    roster_id,
    series_id,
    participant_id,
    state,
    connected_at,
    updated_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(participant_id),
    'connected',
    sqlc.arg(connected_at),
    sqlc.arg(connected_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    series_id,
    participant_id,
    state,
    presence_epoch,
    revision,
    connected_at,
    disconnected_at,
    updated_at;

-- name: CreateSwissCategoryRevision :one
INSERT INTO category_revisions (
    id,
    series_id,
    roster_id,
    revision,
    source_pool_revision_id,
    mode,
    category_pool,
    selected_categories,
    selector_actor_id,
    selection_reason,
    decision_evidence_id,
    decision_algorithm_version,
    decision_inputs,
    decision_seed,
    decision_result,
    decision_replay_digest,
    decision_owner_id,
    decided_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(series_id),
    sqlc.arg(roster_id),
    sqlc.arg(revision),
    sqlc.arg(source_pool_revision_id),
    sqlc.arg(mode),
    sqlc.arg(category_pool),
    sqlc.arg(selected_categories),
    sqlc.narg(selector_actor_id)::UUID,
    sqlc.narg(selection_reason)::TEXT,
    sqlc.narg(decision_evidence_id)::UUID,
    sqlc.narg(decision_algorithm_version)::VARCHAR,
    sqlc.narg(decision_inputs)::JSONB,
    sqlc.narg(decision_seed)::BYTEA,
    sqlc.narg(decision_result)::JSONB,
    sqlc.narg(decision_replay_digest)::BYTEA,
    sqlc.narg(decision_owner_id)::UUID,
    sqlc.narg(decided_at)::TIMESTAMPTZ,
    sqlc.arg(created_at)
)
RETURNING id,
    series_id,
    roster_id,
    revision,
    source_pool_revision_id,
    mode,
    category_pool,
    selected_categories,
    selector_actor_id,
    selection_reason,
    decision_evidence_id,
    decision_algorithm_version,
    decision_inputs,
    decision_seed,
    decision_result,
    decision_replay_digest,
    decision_owner_id,
    decided_at,
    created_at;

-- A Swiss Series is locked only after its category revision is persisted. The
-- transition is kept separate from Wave creation so an incomplete graph can
-- never become executable.
-- name: LockSwissSeriesForMaterialization :one
UPDATE series
SET state = 'locked',
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(series_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND state = 'planned'
    AND started_at IS NULL
    AND finished_at IS NULL
RETURNING id,
    revision,
    state;

-- Exact-normal planning and delivery complete the authority needed by the
-- start workflow. Only that same locked Series may become ready.
-- name: ReadySwissSeriesForMaterialization :one
UPDATE series
SET state = 'ready',
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(series_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND state = 'locked'
    AND started_at IS NULL
    AND finished_at IS NULL
    AND current_result_revision_id IS NULL
RETURNING id,
    revision,
    state;

-- The initial executable attempt follows the Series lock and is made ready
-- only after its assignment and participant deliveries are committed.
-- name: ReadySwissGameForMaterialization :one
UPDATE game_attempts
SET state = 'ready',
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(game_id)
    AND slot_id = sqlc.arg(slot_id)
    AND series_id = sqlc.arg(series_id)
    AND roster_id = sqlc.arg(roster_id)
    AND state = 'planned'
    AND started_at IS NULL
    AND finished_at IS NULL
RETURNING id,
    slot_id,
    series_id,
    roster_id,
    attempt_number,
    revision,
    state;
