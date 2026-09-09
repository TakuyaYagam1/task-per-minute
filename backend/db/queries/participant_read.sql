-- name: GetParticipantStateRoot :one
SELECT tournament.id AS tournament_id,
    tournament.state AS tournament_state,
    roster.id AS roster_id,
    (roster.locked_at IS NOT NULL)::BOOLEAN AS roster_locked,
    participant.id AS participant_id,
    participant.player_id,
    projection.id AS projection_revision_id,
    projection.revision_number AS projection_revision,
    GREATEST(
        1,
        tournament.revision
        + roster.revision
        + projection.revision_number
        + COALESCE((
            SELECT SUM(participant_series.revision)
            FROM series AS participant_series
            WHERE participant_series.roster_id = roster.id
                AND participant.id IN (
                    participant_series.first_participant_id,
                    participant_series.second_participant_id
                )
        ), 0)
        + COALESCE((
            SELECT SUM(slot.revision)
            FROM game_slots AS slot
            JOIN series AS slot_series ON slot_series.id = slot.series_id
            WHERE slot.roster_id = roster.id
                AND participant.id IN (
                    slot_series.first_participant_id,
                    slot_series.second_participant_id
                )
        ), 0)
        + COALESCE((
            SELECT SUM(attempt.revision)
            FROM game_attempts AS attempt
            JOIN series AS attempt_series ON attempt_series.id = attempt.series_id
            WHERE attempt.roster_id = roster.id
                AND participant.id IN (
                    attempt_series.first_participant_id,
                    attempt_series.second_participant_id
                )
        ), 0)
        + COALESCE((
            SELECT SUM(wave.revision)
            FROM wave_members AS member
            JOIN waves AS wave ON wave.id = member.wave_id
            WHERE member.roster_id = roster.id
                AND member.participant_id = participant.id
        ), 0)
        + COALESCE((
            SELECT SUM(readiness.revision)
            FROM wave_readiness AS readiness
            WHERE readiness.roster_id = roster.id
                AND readiness.participant_id = participant.id
        ), 0)
        + COALESCE((
            SELECT SUM(assignment.revision)
            FROM assignments AS assignment
            WHERE assignment.roster_id = roster.id
                AND EXISTS (
                    SELECT 1
                    FROM task_delivery_receipts AS receipt
                    WHERE receipt.assignment_id = assignment.id
                        AND receipt.participant_id = participant.id
                )
        ), 0)
        + COALESCE((
            SELECT SUM(revision.revision)
            FROM draft_revisions AS revision
            JOIN series AS draft_series ON draft_series.id = revision.series_id
            WHERE revision.roster_id = roster.id
                AND participant.id IN (
                    draft_series.first_participant_id,
                    draft_series.second_participant_id
                )
        ), 0)
        + (
            SELECT COUNT(*)
            FROM readiness_events AS event
            WHERE event.roster_id = roster.id
                AND event.participant_id = participant.id
        )
        + (
            SELECT COUNT(*)
            FROM task_delivery_receipts AS receipt
            WHERE receipt.roster_id = roster.id
                AND receipt.participant_id = participant.id
        )
        + (
            SELECT COUNT(*)
            FROM submission_events AS submission
            WHERE submission.roster_id = roster.id
                AND submission.participant_id = participant.id
        )
        + (
            SELECT COUNT(*)
            FROM participant_post_series_actions AS action
            WHERE action.roster_id = roster.id
                AND action.participant_id = participant.id
        )
    )::BIGINT AS participant_view_revision,
    COALESCE((
        SELECT MAX(event.sequence)
        FROM outbox_events AS event
        WHERE event.tournament_id = tournament.id
            AND (
                event.audience IN ('all', 'public')
                OR (
                    event.audience = 'participant'
                    AND event.principal_id = participant.player_id
                )
            )
    ), 0)::BIGINT AS event_sequence,
    transaction_timestamp()::TIMESTAMPTZ AS observed_at
FROM tournaments AS tournament
JOIN rosters AS roster ON roster.tournament_id = tournament.id
JOIN participants AS participant
    ON participant.roster_id = roster.id
    AND participant.player_id = sqlc.arg(player_id)
JOIN projection_revisions AS projection
    ON projection.tournament_id = tournament.id
    AND projection.roster_id = roster.id
    AND projection.state = 'published'
WHERE tournament.id = sqlc.arg(tournament_id);

-- name: ListParticipantLobbySeries :many
SELECT series.id AS series_id,
    COALESCE(current_wave.wave_id::TEXT, '')::TEXT AS wave_id,
    participant.id AS participant_id,
    opponent.id AS opponent_id,
    opponent_player.username AS opponent_display_name,
    series.format,
    series.state
FROM participants AS participant
JOIN rosters AS roster ON roster.id = participant.roster_id
JOIN series
    ON series.roster_id = roster.id
    AND participant.id IN (
        series.first_participant_id,
        series.second_participant_id
    )
JOIN participants AS opponent
    ON opponent.roster_id = roster.id
    AND opponent.id = CASE
        WHEN series.first_participant_id = participant.id
            THEN series.second_participant_id
        ELSE series.first_participant_id
    END
JOIN players AS opponent_player ON opponent_player.id = opponent.player_id
LEFT JOIN LATERAL (
    SELECT wave_series.wave_id
    FROM wave_series
    JOIN waves AS wave ON wave.id = wave_series.wave_id
    WHERE wave_series.series_id = series.id
    ORDER BY (wave.state <> 'superseded') DESC,
        wave.updated_at DESC,
        wave.id DESC
    LIMIT 1
) AS current_wave ON TRUE
WHERE roster.tournament_id = sqlc.arg(tournament_id)
    AND participant.player_id = sqlc.arg(player_id)
ORDER BY series.created_at,
    series.id;

-- name: GetParticipantStateAssignment :one
SELECT assignment.id AS assignment_id,
    assignment.attempt_id,
    assignment.series_id,
    attempt.id AS game_id,
    COALESCE(current_wave.wave_id::TEXT, '')::TEXT AS wave_id,
    participant.id AS participant_id,
    snapshot.id AS snapshot_id,
    snapshot.task_id,
    snapshot.task_version,
    snapshot.kind,
    snapshot.title,
    snapshot.description,
    snapshot.category,
    snapshot.difficulty,
    snapshot.time_limit,
    snapshot.hints,
    snapshot.task_url,
    snapshot.source_file_url,
    receipt.id AS receipt_id,
    receipt.instance_id,
    receipt.delivered_at,
    COALESCE(reserve_count.value, 0)::BIGINT AS undisclosed_reserve_count
FROM participants AS participant
JOIN rosters AS roster ON roster.id = participant.roster_id
JOIN task_delivery_receipts AS receipt
    ON receipt.roster_id = roster.id
    AND receipt.participant_id = participant.id
JOIN assignments AS assignment
    ON assignment.id = receipt.assignment_id
    AND assignment.attempt_id = receipt.attempt_id
    AND assignment.roster_id = receipt.roster_id
    AND assignment.state = 'active'
JOIN game_attempts AS attempt
    ON attempt.id = assignment.attempt_id
    AND attempt.series_id = assignment.series_id
    AND attempt.roster_id = assignment.roster_id
JOIN series AS assignment_series
    ON assignment_series.id = assignment.series_id
    AND assignment_series.roster_id = assignment.roster_id
    AND participant.id IN (
        assignment_series.first_participant_id,
        assignment_series.second_participant_id
    )
JOIN task_snapshots AS snapshot
    ON snapshot.id = receipt.snapshot_id
    AND snapshot.id = assignment.snapshot_id
    AND snapshot.task_id = receipt.task_id
    AND snapshot.task_id = assignment.task_id
    AND snapshot.task_version = receipt.task_version
    AND snapshot.task_version = assignment.task_version
JOIN task_version_reservations AS active_reservation
    ON active_reservation.id = assignment.reservation_id
JOIN assignment_plan_edges AS active_edge
    ON active_edge.id = active_reservation.edge_id
LEFT JOIN LATERAL (
    SELECT wave_series.wave_id
    FROM wave_series
    JOIN waves AS wave ON wave.id = wave_series.wave_id
    WHERE wave_series.series_id = assignment.series_id
    ORDER BY (wave.state <> 'superseded') DESC,
        wave.updated_at DESC,
        wave.id DESC
    LIMIT 1
) AS current_wave ON TRUE
LEFT JOIN LATERAL (
    SELECT COUNT(*) AS value
    FROM task_version_reservations AS reserve
    JOIN assignment_plan_edges AS reserve_edge
        ON reserve_edge.id = reserve.edge_id
    WHERE reserve.plan_id = assignment.plan_id
        AND reserve.branch_id = assignment.branch_id
        AND reserve.state = 'committed'
        AND reserve.disclosed_at IS NULL
        AND reserve_edge.position > active_edge.position
) AS reserve_count ON TRUE
WHERE roster.tournament_id = sqlc.arg(tournament_id)
    AND participant.player_id = sqlc.arg(player_id)
ORDER BY receipt.delivered_at DESC,
    receipt.id DESC
LIMIT 1;

-- name: GetParticipantStateSeries :one
SELECT sqlc.embed(series),
    COALESCE(current_draft.id::TEXT, '')::TEXT AS draft_id
FROM participants AS participant
JOIN rosters AS roster ON roster.id = participant.roster_id
JOIN series
    ON series.roster_id = roster.id
    AND participant.id IN (
        series.first_participant_id,
        series.second_participant_id
    )
LEFT JOIN LATERAL (
    SELECT draft.id
    FROM drafts AS draft
    JOIN draft_revisions AS revision ON revision.draft_id = draft.id
    WHERE draft.series_id = series.id
        AND revision.state <> 'superseded'
    ORDER BY revision.revision DESC,
        draft.id DESC
    LIMIT 1
) AS current_draft ON TRUE
WHERE roster.tournament_id = sqlc.arg(tournament_id)
    AND participant.player_id = sqlc.arg(player_id)
ORDER BY (
        series.id IS NOT DISTINCT FROM sqlc.narg(preferred_series_id)::UUID
    ) DESC,
    (series.state NOT IN ('completed', 'cancelled')) DESC,
    series.updated_at DESC,
    series.id DESC
LIMIT 1;

-- name: ListParticipantStateSeriesGraph :many
SELECT sqlc.embed(game_slot),
    sqlc.embed(game_attempt)
FROM game_slots AS game_slot
JOIN game_attempts AS game_attempt
    ON game_attempt.slot_id = game_slot.id
    AND game_attempt.series_id = game_slot.series_id
    AND game_attempt.roster_id = game_slot.roster_id
WHERE game_slot.series_id = sqlc.arg(series_id)
    AND game_slot.roster_id = sqlc.arg(roster_id)
ORDER BY game_slot.slot_number,
    game_attempt.attempt_number;

-- name: GetParticipantStateWave :one
SELECT wave.id AS wave_id,
    wave.tournament_id,
    wave.revision_id AS wave_revision_id,
    wave.revision AS wave_revision,
    wave.state AS wave_state,
    wave.started_at,
    wave.paused_at,
    ready_window.id AS ready_window_id,
    ready_window.revision_id AS ready_window_revision_id,
    ready_window.state AS ready_window_state,
    ready_window.opened_at,
    ready_window.deadline,
    ready_window.consumed_at
FROM participants AS participant
JOIN rosters AS roster ON roster.id = participant.roster_id
JOIN wave_members AS member
    ON member.roster_id = roster.id
    AND member.participant_id = participant.id
JOIN waves AS wave
    ON wave.id = member.wave_id
    AND wave.roster_id = roster.id
LEFT JOIN ready_windows AS ready_window
    ON ready_window.wave_id = wave.id
    AND ready_window.roster_id = wave.roster_id
WHERE roster.tournament_id = sqlc.arg(tournament_id)
    AND participant.player_id = sqlc.arg(player_id)
    AND EXISTS (
        SELECT 1
        FROM wave_series
        WHERE wave_series.wave_id = wave.id
            AND wave_series.series_id = COALESCE(
                sqlc.narg(series_id)::UUID,
                wave_series.series_id
            )
    )
ORDER BY (wave.state <> 'superseded') DESC,
    wave.updated_at DESC,
    wave.id DESC
LIMIT 1;

-- name: ListParticipantStateWaveMembers :many
SELECT member.participant_id,
    COALESCE(readiness.ready, FALSE)::BOOLEAN AS ready,
    COALESCE(readiness.revision, 0)::BIGINT AS readiness_revision,
    COALESCE(member_series.series_id::TEXT, '')::TEXT AS series_id,
    COALESCE(member_series.series_count, 0)::BIGINT AS series_count
FROM wave_members AS member
LEFT JOIN wave_readiness AS readiness
    ON readiness.wave_id = member.wave_id
    AND readiness.roster_id = member.roster_id
    AND readiness.participant_id = member.participant_id
LEFT JOIN LATERAL (
    SELECT wave_series.series_id,
        COUNT(*) OVER ()::BIGINT AS series_count
    FROM wave_series
    JOIN series ON series.id = wave_series.series_id
    WHERE wave_series.wave_id = member.wave_id
        AND member.participant_id IN (
            series.first_participant_id,
            series.second_participant_id
        )
    ORDER BY wave_series.series_id
    LIMIT 1
) AS member_series ON TRUE
WHERE member.wave_id = sqlc.arg(wave_id)
ORDER BY member.participant_id;
