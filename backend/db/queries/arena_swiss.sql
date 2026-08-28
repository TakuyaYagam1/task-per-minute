-- name: CreateAutomaticArenaSwissRound :one
INSERT INTO arena_swiss_rounds (
    id,
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
RETURNING id,
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
    updated_at;

-- name: CreateManualArenaSwissRound :one
INSERT INTO arena_swiss_rounds (
    id,
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
RETURNING id,
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
    updated_at;

-- name: LockArenaSwissRoundForUpdate :one
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
    updated_at
FROM arena_swiss_rounds
WHERE id = sqlc.arg(id)
FOR UPDATE;

-- name: UpdateAutomaticArenaSwissRoundCAS :one
UPDATE arena_swiss_rounds
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
RETURNING id,
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
    updated_at;

-- name: UpdateManualArenaSwissRoundCAS :one
UPDATE arena_swiss_rounds
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
RETURNING id,
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
    updated_at;

-- name: CreateArenaSwissRepeatOverride :exec
INSERT INTO arena_swiss_repeat_overrides (
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

-- name: CreateArenaSwissPairing :exec
INSERT INTO arena_swiss_pairings (
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

-- name: CreateArenaSwissPairingMember :exec
INSERT INTO arena_swiss_pairing_members (
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

-- name: CreateArenaSwissOpponentHistory :exec
INSERT INTO arena_swiss_opponent_history (
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

-- name: CreateArenaSwissBye :exec
INSERT INTO arena_swiss_byes (
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

-- name: DeleteArenaSwissOpponentHistory :exec
DELETE FROM arena_swiss_opponent_history
WHERE round_id = sqlc.arg(round_id);

-- name: DeleteArenaSwissPairingMembers :exec
DELETE FROM arena_swiss_pairing_members
WHERE round_id = sqlc.arg(round_id);

-- name: DeleteArenaSwissPairings :exec
DELETE FROM arena_swiss_pairings
WHERE round_id = sqlc.arg(round_id);

-- name: DeleteArenaSwissBye :exec
DELETE FROM arena_swiss_byes
WHERE round_id = sqlc.arg(round_id);

-- name: DeleteArenaSwissRepeatOverride :exec
DELETE FROM arena_swiss_repeat_overrides
WHERE round_id = sqlc.arg(round_id);

-- name: LockArenaSwissRoundCAS :one
UPDATE arena_swiss_rounds
SET lock_revision = revision,
    locked_at = sqlc.arg(locked_at),
    updated_at = sqlc.arg(locked_at)
WHERE id = sqlc.arg(id)
    AND revision = sqlc.arg(expected_revision)
    AND locked_at IS NULL
RETURNING id,
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
    updated_at;

-- name: GetArenaSwissRound :one
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
    updated_at
FROM arena_swiss_rounds
WHERE id = sqlc.arg(id);

-- name: ListArenaSwissRounds :many
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
    updated_at
FROM arena_swiss_rounds
WHERE roster_id = sqlc.arg(roster_id)
ORDER BY round_number;

-- name: ListArenaSwissOpponentHistory :many
SELECT history.pairing_id,
    history.round_id,
    history.roster_id,
    pairing.slot_number,
    first_member.participant_id AS first_participant_id,
    second_member.participant_id AS second_participant_id,
    history.prior_meeting_count,
    history.repeat_override_id,
    history.recorded_at
FROM arena_swiss_opponent_history AS history
JOIN arena_swiss_pairings AS pairing ON pairing.id = history.pairing_id
JOIN arena_swiss_rounds AS round ON round.id = history.round_id
JOIN arena_swiss_pairing_members AS first_member
    ON first_member.pairing_id = history.pairing_id
    AND first_member.seat = 1
JOIN arena_swiss_pairing_members AS second_member
    ON second_member.pairing_id = history.pairing_id
    AND second_member.seat = 2
WHERE history.roster_id = sqlc.arg(roster_id)
ORDER BY round.round_number,
    pairing.slot_number;

-- name: GetArenaSwissRepeatOverride :one
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
FROM arena_swiss_repeat_overrides
WHERE round_id = sqlc.arg(round_id);

-- name: GetArenaSwissBye :one
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
FROM arena_swiss_byes
WHERE round_id = sqlc.arg(round_id);
