-- name: CreateGameSlot :one
INSERT INTO game_slots (
    id,
    series_id,
    roster_id,
    slot_number,
    category,
    first_participant_wins_before,
    second_participant_wins_before,
    revision,
    created_at,
    updated_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(series_id),
    sqlc.arg(roster_id),
    sqlc.arg(slot_number),
    sqlc.arg(category),
    sqlc.arg(first_participant_wins_before),
    sqlc.arg(second_participant_wins_before),
    1,
    sqlc.arg(created_at),
    sqlc.arg(created_at)
)
RETURNING id,
    series_id,
    roster_id,
    slot_number,
    category,
    first_participant_wins_before,
    second_participant_wins_before,
    revision,
    created_at,
    updated_at;

-- name: CreateGameAttempt :one
INSERT INTO game_attempts (
    id,
    slot_id,
    series_id,
    roster_id,
    attempt_number,
    state,
    result_reason,
    winner_id,
    result_revision_id,
    revision,
    created_at,
    updated_at,
    started_at,
    finished_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(slot_id),
    sqlc.arg(series_id),
    sqlc.arg(roster_id),
    sqlc.arg(attempt_number),
    sqlc.arg(state),
    sqlc.narg(result_reason)::VARCHAR,
    sqlc.arg(winner_id),
    sqlc.arg(result_revision_id),
    1,
    sqlc.arg(created_at),
    sqlc.arg(created_at),
    sqlc.narg(started_at)::TIMESTAMPTZ,
    sqlc.narg(finished_at)::TIMESTAMPTZ
)
RETURNING id,
    slot_id,
    series_id,
    roster_id,
    attempt_number,
    state,
    result_reason,
    winner_id,
    result_revision_id,
    revision,
    created_at,
    updated_at,
    started_at,
    finished_at;

-- name: GetGameAttemptScoped :one
SELECT attempt.id,
    attempt.slot_id,
    attempt.series_id,
    attempt.roster_id,
    attempt.attempt_number,
    attempt.state,
    attempt.result_reason,
    attempt.winner_id,
    attempt.result_revision_id,
    attempt.revision,
    attempt.created_at,
    attempt.updated_at,
    attempt.started_at,
    attempt.finished_at
FROM game_attempts AS attempt
JOIN series AS series ON series.id = attempt.series_id
WHERE attempt.id = sqlc.arg(id)
    AND attempt.slot_id = sqlc.arg(slot_id)
    AND attempt.series_id = sqlc.arg(series_id)
    AND series.tournament_id = sqlc.arg(tournament_id);

-- name: UpdateGameAttemptCAS :one
UPDATE game_attempts AS attempt
SET state = sqlc.arg(next_state),
    result_reason = sqlc.narg(result_reason)::VARCHAR,
    winner_id = sqlc.arg(winner_id),
    result_revision_id = sqlc.arg(result_revision_id),
    revision = attempt.revision + 1,
    updated_at = sqlc.arg(updated_at),
    started_at = CASE
        WHEN sqlc.arg(next_state)::VARCHAR IN ('active', 'paused')
            THEN COALESCE(attempt.started_at, sqlc.arg(updated_at))
        ELSE attempt.started_at
    END,
    finished_at = CASE
        WHEN sqlc.arg(next_state)::VARCHAR IN (
            'completed',
            'void',
            'cancelled',
            'superseded'
        ) THEN sqlc.arg(updated_at)
        ELSE NULL
    END
FROM series AS series
WHERE attempt.id = sqlc.arg(id)
    AND attempt.slot_id = sqlc.arg(slot_id)
    AND attempt.series_id = sqlc.arg(series_id)
    AND series.id = attempt.series_id
    AND series.tournament_id = sqlc.arg(tournament_id)
    AND attempt.revision = sqlc.arg(expected_revision)
    AND attempt.state = sqlc.arg(expected_state)
RETURNING attempt.id,
    attempt.slot_id,
    attempt.series_id,
    attempt.roster_id,
    attempt.attempt_number,
    attempt.state,
    attempt.result_reason,
    attempt.winner_id,
    attempt.result_revision_id,
    attempt.revision,
    attempt.created_at,
    attempt.updated_at,
    attempt.started_at,
    attempt.finished_at;

-- name: GetGameSlot :one
SELECT slot.id,
    slot.series_id,
    slot.roster_id,
    slot.slot_number,
    slot.category,
    slot.first_participant_wins_before,
    slot.second_participant_wins_before,
    slot.revision,
    slot.created_at,
    slot.updated_at
FROM game_slots AS slot
JOIN series AS series ON series.id = slot.series_id
WHERE slot.id = sqlc.arg(id)
    AND slot.series_id = sqlc.arg(series_id)
    AND series.tournament_id = sqlc.arg(tournament_id);

-- name: ListGameAttempts :many
SELECT id,
    slot_id,
    series_id,
    roster_id,
    attempt_number,
    state,
    result_reason,
    winner_id,
    result_revision_id,
    revision,
    created_at,
    updated_at,
    started_at,
    finished_at
FROM game_attempts
WHERE slot_id = sqlc.arg(slot_id)
ORDER BY attempt_number;
