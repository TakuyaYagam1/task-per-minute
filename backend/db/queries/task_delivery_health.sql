-- The private task delivery backlog is a durable availability invariant, not
-- a public realtime queue. Every active Wave, Series, Game, and assignment
-- graph must retain one
-- immutable receipt for each participant before task material is considered
-- available through the private participant snapshot.
-- name: GetPrivateTaskDeliveryBacklog :one
WITH expected_receipts AS (
    SELECT wave.started_at,
        assignment.id AS assignment_id,
        assignment.attempt_id,
        assignment.roster_id,
        assignment.snapshot_id,
        assignment.task_id,
        assignment.task_version,
        public.participant_task_instance_id(assignment.id, participant.participant_id) AS instance_id,
        participant.participant_id
    FROM tournaments AS tournament
    JOIN rosters AS roster ON roster.tournament_id = tournament.id
    JOIN waves AS wave
        ON wave.tournament_id = tournament.id
        AND wave.roster_id = roster.id
    JOIN wave_series AS membership
        ON membership.wave_id = wave.id
        AND membership.tournament_id = tournament.id
        AND membership.roster_id = wave.roster_id
    JOIN series
        ON series.id = membership.series_id
        AND series.tournament_id = tournament.id
        AND series.roster_id = wave.roster_id
    JOIN game_slots AS slot
        ON slot.series_id = series.id
        AND slot.roster_id = series.roster_id
    JOIN LATERAL (
        SELECT game.id
        FROM game_attempts AS game
        WHERE game.slot_id = slot.id
            AND game.series_id = series.id
            AND game.roster_id = series.roster_id
        ORDER BY game.attempt_number DESC, game.id DESC
        LIMIT 1
    ) AS current_attempt ON TRUE
    JOIN game_attempts AS attempt
        ON attempt.id = current_attempt.id
        AND attempt.series_id = series.id
        AND attempt.roster_id = series.roster_id
    JOIN assignments AS assignment
        ON assignment.attempt_id = attempt.id
        AND assignment.series_id = series.id
        AND assignment.roster_id = series.roster_id
    CROSS JOIN LATERAL unnest(
        ARRAY[series.first_participant_id, series.second_participant_id]
    ) AS participant(participant_id)
    WHERE wave.state = 'active'
        AND series.state = 'active'
        AND attempt.state = 'active'
        AND assignment.state = 'active'
), missing_receipts AS (
    SELECT expected.started_at
    FROM expected_receipts AS expected
    LEFT JOIN task_delivery_receipts AS receipt
        ON receipt.assignment_id = expected.assignment_id
        AND receipt.attempt_id = expected.attempt_id
        AND receipt.roster_id = expected.roster_id
        AND receipt.participant_id = expected.participant_id
        AND receipt.snapshot_id = expected.snapshot_id
        AND receipt.task_id = expected.task_id
        AND receipt.task_version = expected.task_version
        AND receipt.instance_id = expected.instance_id
    WHERE receipt.id IS NULL
)
SELECT COUNT(*)::BIGINT AS pending_count,
    MIN(started_at)::TIMESTAMPTZ AS oldest_pending_at
FROM missing_receipts;
