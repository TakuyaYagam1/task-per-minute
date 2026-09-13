-- name: CreateAutomaticSwissRound :one
INSERT INTO swiss_rounds (
    id,
    tournament_id,
    roster_id,
    round_number,
    revision,
    source_roster_revision,
    source_history_revision,
    generation_kind,
    pairing_inputs,
    decision_evidence_id,
    decision_algorithm_version,
    decision_seed,
    decision_result,
    decision_replay_digest,
    decision_owner_id,
    generated_at,
    lock_revision,
    locked_at,
    created_at,
    updated_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(round_number),
    1,
    sqlc.arg(source_roster_revision),
    sqlc.arg(source_history_revision),
    'automatic',
    sqlc.arg(pairing_inputs),
    sqlc.arg(decision_evidence_id),
    sqlc.arg(decision_algorithm_version),
    sqlc.arg(decision_seed),
    sqlc.arg(decision_result),
    sqlc.arg(decision_replay_digest),
    sqlc.arg(decision_owner_id),
    sqlc.arg(generated_at),
    sqlc.narg(lock_revision)::BIGINT,
    sqlc.narg(locked_at)::TIMESTAMPTZ,
    sqlc.arg(created_at),
    sqlc.arg(created_at)
)
RETURNING *;

-- name: CreateManualSwissRound :one
INSERT INTO swiss_rounds (
    id,
    tournament_id,
    roster_id,
    round_number,
    revision,
    source_roster_revision,
    source_history_revision,
    generation_kind,
    pairing_inputs,
    generated_at,
    lock_revision,
    locked_at,
    created_at,
    updated_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(round_number),
    1,
    sqlc.arg(source_roster_revision),
    sqlc.arg(source_history_revision),
    'manual',
    sqlc.arg(pairing_inputs),
    sqlc.arg(generated_at),
    sqlc.narg(lock_revision)::BIGINT,
    sqlc.narg(locked_at)::TIMESTAMPTZ,
    sqlc.arg(created_at),
    sqlc.arg(created_at)
)
RETURNING *;

-- name: LockSwissRoundForUpdate :one
SELECT id,
    roster_id,
    round_number,
    revision,
    source_roster_revision,
    source_history_revision,
    generation_kind,
    pairing_inputs,
    decision_evidence_id,
    decision_algorithm_version,
    decision_seed,
    decision_result,
    decision_replay_digest,
    decision_owner_id,
    generated_at,
    lock_revision,
    locked_at,
    created_at,
    updated_at,
    tournament_id,
    content_configuration_id,
    content_configuration_revision,
    category_mode,
    effective_categories
FROM swiss_rounds
WHERE id = sqlc.arg(id)
FOR UPDATE;

-- name: UpdateAutomaticSwissRoundCAS :one
UPDATE swiss_rounds
SET revision = revision + 1,
    source_roster_revision = sqlc.arg(source_roster_revision),
    source_history_revision = sqlc.arg(source_history_revision),
    generation_kind = 'automatic',
    pairing_inputs = sqlc.arg(pairing_inputs),
    decision_evidence_id = sqlc.arg(decision_evidence_id),
    decision_algorithm_version = sqlc.arg(decision_algorithm_version),
    decision_seed = sqlc.arg(decision_seed),
    decision_result = sqlc.arg(decision_result),
    decision_replay_digest = sqlc.arg(decision_replay_digest),
    decision_owner_id = sqlc.arg(decision_owner_id),
    generated_at = sqlc.arg(generated_at),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
    AND revision = sqlc.arg(expected_revision)
    AND locked_at IS NULL
RETURNING *;

-- name: UpdateManualSwissRoundCAS :one
UPDATE swiss_rounds
SET revision = revision + 1,
    source_roster_revision = sqlc.arg(source_roster_revision),
    source_history_revision = sqlc.arg(source_history_revision),
    generation_kind = 'manual',
    pairing_inputs = sqlc.arg(pairing_inputs),
    decision_evidence_id = NULL,
    decision_algorithm_version = NULL,
    decision_seed = NULL,
    decision_result = NULL,
    decision_replay_digest = NULL,
    decision_owner_id = NULL,
    generated_at = sqlc.arg(generated_at),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
    AND revision = sqlc.arg(expected_revision)
    AND locked_at IS NULL
RETURNING *;

-- name: CreateSwissRepeatOverride :exec
INSERT INTO swiss_repeat_overrides (
    id,
    round_id,
    roster_id,
    actor_id,
    reason,
    confirmed_at,
    roster_participant_ids,
    proposed_pairings,
    bye_participant_id,
    previous_meetings,
    repeated_pairings,
    alternative_algorithm_version,
    alternative_search_complete,
    alternative_pairings,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(round_id),
    sqlc.arg(roster_id),
    sqlc.arg(actor_id),
    sqlc.arg(reason),
    sqlc.arg(confirmed_at),
    sqlc.arg(roster_participant_ids),
    sqlc.arg(proposed_pairings),
    sqlc.narg(bye_participant_id)::UUID,
    sqlc.arg(previous_meetings),
    sqlc.arg(repeated_pairings),
    sqlc.arg(alternative_algorithm_version),
    sqlc.arg(alternative_search_complete),
    sqlc.arg(alternative_pairings),
    sqlc.arg(created_at)
);

-- name: CreateSwissPairing :exec
INSERT INTO swiss_pairings (
    id,
    round_id,
    roster_id,
    slot_number,
    repeat_override_id,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(round_id),
    sqlc.arg(roster_id),
    sqlc.arg(slot_number),
    sqlc.narg(repeat_override_id)::UUID,
    sqlc.arg(created_at)
);

-- name: CreateSwissPairingMember :exec
INSERT INTO swiss_pairing_members (
    pairing_id,
    round_id,
    roster_id,
    seat,
    participant_id,
    created_at
)
VALUES (
    sqlc.arg(pairing_id),
    sqlc.arg(round_id),
    sqlc.arg(roster_id),
    sqlc.arg(seat),
    sqlc.arg(participant_id),
    sqlc.arg(created_at)
);

-- name: CreateSwissOpponentHistory :exec
INSERT INTO swiss_opponent_history (
    pairing_id,
    round_id,
    roster_id,
    prior_meeting_count,
    repeat_override_id,
    recorded_at
)
VALUES (
    sqlc.arg(pairing_id),
    sqlc.arg(round_id),
    sqlc.arg(roster_id),
    sqlc.arg(prior_meeting_count),
    sqlc.narg(repeat_override_id)::UUID,
    sqlc.arg(recorded_at)
);

-- name: CreateSwissBye :exec
INSERT INTO swiss_byes (
    round_id,
    roster_id,
    participant_id,
    points_awarded,
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
    sqlc.arg(round_id),
    sqlc.arg(roster_id),
    sqlc.arg(participant_id),
    sqlc.arg(points_awarded),
    sqlc.arg(decision_evidence_id),
    sqlc.arg(decision_algorithm_version),
    sqlc.arg(decision_inputs),
    sqlc.arg(decision_seed),
    sqlc.arg(decision_result),
    sqlc.arg(decision_replay_digest),
    sqlc.arg(decision_owner_id),
    sqlc.arg(decided_at),
    sqlc.arg(created_at)
);

-- name: DeleteSwissOpponentHistory :exec
DELETE FROM swiss_opponent_history
WHERE round_id = sqlc.arg(round_id);

-- name: DeleteSwissPairingMembers :exec
DELETE FROM swiss_pairing_members
WHERE round_id = sqlc.arg(round_id);

-- name: DeleteSwissPairings :exec
DELETE FROM swiss_pairings
WHERE round_id = sqlc.arg(round_id);

-- name: DeleteSwissBye :exec
DELETE FROM swiss_byes
WHERE round_id = sqlc.arg(round_id);

-- name: DeleteSwissRepeatOverride :exec
DELETE FROM swiss_repeat_overrides
WHERE round_id = sqlc.arg(round_id);

-- name: LockSwissRoundCAS :one
UPDATE swiss_rounds
SET lock_revision = revision,
    locked_at = sqlc.arg(locked_at),
    updated_at = sqlc.arg(locked_at)
WHERE id = sqlc.arg(id)
    AND revision = sqlc.arg(expected_revision)
    AND locked_at IS NULL
RETURNING *;

-- name: CreateSwissRoundLockProof :exec
INSERT INTO swiss_round_lock_proofs (
    round_id,
    tournament_id,
    roster_id,
    preset,
    round_number,
    source_projection_revision_id,
    preflight_revision_id,
    normal_pool_revision_id,
    wave_id,
    wave_revision_id,
    round_revision,
    source_projection_revision,
    roster_revision,
    history_revision,
    normal_pool_revision,
    wave_revision,
    bye_participant_id,
    proof_hash,
    proof_mode,
    terminal_command_id,
    locked_at,
    created_at
)
VALUES (
    sqlc.arg(round_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(preset),
    sqlc.arg(round_number),
    sqlc.arg(source_projection_revision_id),
    sqlc.arg(preflight_revision_id),
    sqlc.arg(normal_pool_revision_id),
    sqlc.arg(wave_id),
    sqlc.arg(wave_revision_id),
    sqlc.arg(round_revision),
    sqlc.arg(source_projection_revision),
    sqlc.arg(roster_revision),
    sqlc.arg(history_revision),
    sqlc.arg(normal_pool_revision),
    sqlc.arg(wave_revision),
    sqlc.narg(bye_participant_id)::UUID,
    sqlc.arg(proof_hash),
    COALESCE(sqlc.narg(proof_mode)::text, 'wave_start'),
    sqlc.narg(terminal_command_id)::uuid,
    sqlc.arg(locked_at),
    sqlc.arg(created_at)
);

-- name: CreateSwissRoundLockProofMember :exec
INSERT INTO swiss_round_lock_proof_members (
    round_id,
    roster_id,
    participant_id,
    created_at
)
VALUES (
    sqlc.arg(round_id),
    sqlc.arg(roster_id),
    sqlc.arg(participant_id),
    sqlc.arg(created_at)
);

-- name: CreateSwissRoundLockProofSeries :exec
INSERT INTO swiss_round_lock_proof_series (
    round_id,
    roster_id,
    pairing_id,
    series_id,
    first_participant_id,
    second_participant_id,
    category_revision_id,
    category_revision,
    assignment_id,
    assignment_revision,
    assignment_plan_id,
    assignment_plan_revision_id,
    reservation_id,
    reservation_revision,
    created_at
)
VALUES (
    sqlc.arg(round_id),
    sqlc.arg(roster_id),
    sqlc.arg(pairing_id),
    sqlc.arg(series_id),
    sqlc.arg(first_participant_id),
    sqlc.arg(second_participant_id),
    sqlc.arg(category_revision_id),
    sqlc.arg(category_revision),
    sqlc.arg(assignment_id),
    sqlc.arg(assignment_revision),
    sqlc.arg(assignment_plan_id),
    sqlc.arg(assignment_plan_revision_id),
    sqlc.arg(reservation_id),
    sqlc.arg(reservation_revision),
    sqlc.arg(created_at)
);

-- name: GetSwissRound :one
SELECT id,
    roster_id,
    round_number,
    revision,
    source_roster_revision,
    source_history_revision,
    generation_kind,
    pairing_inputs,
    decision_evidence_id,
    decision_algorithm_version,
    decision_seed,
    decision_result,
    decision_replay_digest,
    decision_owner_id,
    generated_at,
    lock_revision,
    locked_at,
    created_at,
    updated_at,
    tournament_id,
    content_configuration_id,
    content_configuration_revision,
    category_mode,
    effective_categories
FROM swiss_rounds
WHERE id = sqlc.arg(id);

-- name: ListSwissRounds :many
SELECT id,
    roster_id,
    round_number,
    revision,
    source_roster_revision,
    source_history_revision,
    generation_kind,
    pairing_inputs,
    decision_evidence_id,
    decision_algorithm_version,
    decision_seed,
    decision_result,
    decision_replay_digest,
    decision_owner_id,
    generated_at,
    lock_revision,
    locked_at,
    created_at,
    updated_at,
    tournament_id,
    content_configuration_id,
    content_configuration_revision,
    category_mode,
    effective_categories
FROM swiss_rounds
WHERE roster_id = sqlc.arg(roster_id)
ORDER BY round_number;

-- name: ListSwissOpponentHistory :many
SELECT history.pairing_id,
    history.round_id,
    history.roster_id,
    pairing.slot_number,
    first_member.participant_id AS first_participant_id,
    second_member.participant_id AS second_participant_id,
    history.prior_meeting_count,
    history.repeat_override_id,
    history.recorded_at
FROM swiss_opponent_history AS history
JOIN swiss_pairings AS pairing ON pairing.id = history.pairing_id
JOIN swiss_rounds AS round ON round.id = history.round_id
JOIN swiss_pairing_members AS first_member
    ON first_member.pairing_id = history.pairing_id
    AND first_member.seat = 1
JOIN swiss_pairing_members AS second_member
    ON second_member.pairing_id = history.pairing_id
    AND second_member.seat = 2
WHERE history.roster_id = sqlc.arg(roster_id)
ORDER BY round.round_number,
    pairing.slot_number;

-- name: GetSwissRepeatOverride :one
SELECT id,
    round_id,
    roster_id,
    actor_id,
    reason,
    confirmed_at,
    roster_participant_ids,
    proposed_pairings,
    bye_participant_id,
    previous_meetings,
    repeated_pairings,
    alternative_algorithm_version,
    alternative_search_complete,
    alternative_pairings,
    created_at
FROM swiss_repeat_overrides
WHERE round_id = sqlc.arg(round_id);

-- name: GetSwissBye :one
SELECT round_id,
    roster_id,
    participant_id,
    points_awarded,
    decision_evidence_id,
    decision_algorithm_version,
    decision_inputs,
    decision_seed,
    decision_result,
    decision_replay_digest,
    decision_owner_id,
    decided_at,
    created_at
FROM swiss_byes
WHERE round_id = sqlc.arg(round_id);
