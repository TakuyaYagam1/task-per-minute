-- name: GetParticipantStateRoot :one
SELECT tournament.id AS tournament_id,
    tournament.state AS tournament_state,
    roster.id AS roster_id,
    (roster.locked_at IS NOT NULL)::BOOLEAN AS roster_locked,
    participant.id AS participant_id,
    participant.player_id,
    participant.attendance,
    COALESCE((
        SELECT swiss_round.round_number
        FROM wave_members AS participant_wave_member
        JOIN swiss_wave_links AS swiss_link
            ON swiss_link.wave_id = participant_wave_member.wave_id
            AND swiss_link.tournament_id = tournament.id
            AND swiss_link.roster_id = roster.id
        JOIN swiss_rounds AS swiss_round
            ON swiss_round.id = swiss_link.round_id
            AND swiss_round.roster_id = roster.id
        WHERE participant_wave_member.roster_id = roster.id
            AND participant_wave_member.participant_id = participant.id
        ORDER BY swiss_round.round_number DESC,
            swiss_round.updated_at DESC,
            swiss_round.id DESC
        LIMIT 1
    ), 0)::SMALLINT AS current_swiss_round,
    COALESCE((
        SELECT SUM(ledger.points)
        FROM swiss_point_ledger_entries AS ledger
        WHERE ledger.tournament_id = tournament.id
            AND ledger.roster_id = roster.id
            AND ledger.participant_id = participant.id
    ), 0)::BIGINT AS swiss_points,
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
    AND current_wave.wave_id IS NOT NULL
ORDER BY series.created_at,
    series.id;

-- name: GetParticipantStateAssignment :one
SELECT assignment.id AS assignment_id,
    assignment.attempt_id,
    assignment.series_id,
    attempt.id AS game_id,
    attempt.state AS attempt_state,
    attempt.started_at AS attempt_started_at,
    COALESCE(current_wave.wave_id::TEXT, '')::TEXT AS wave_id,
    game_slot.id AS slot_id,
    game_slot.slot_number AS game_number,
    assignment_stage.stage,
    COALESCE(assignment_stage.round_number, 0)::SMALLINT AS swiss_round,
    assignment_series.first_participant_wins AS series_first_participant_wins,
    assignment_series.second_participant_wins AS series_second_participant_wins,
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
    COALESCE(reserve_count.value, 0)::BIGINT AS undisclosed_reserve_count,
    COALESCE(game_pause.pause_id, '00000000-0000-0000-0000-000000000000'::UUID) AS game_pause_id,
    game_pause.game_attempt_id AS game_pause_game_attempt_id,
    COALESCE(game_pause.state, '')::TEXT AS game_pause_state,
    game_pause.started_at AS game_pause_started_at,
    game_pause.original_deadline AS game_pause_original_deadline,
    game_pause.frozen_at AS game_pause_frozen_at,
    game_pause.frozen_remaining_ms AS game_pause_frozen_remaining_ms,
    game_pause.resumed_at AS game_pause_resumed_at,
    game_pause.resumed_deadline AS game_pause_resumed_deadline,
    CASE
        WHEN attempt.state <> 'active' THEN NULL::TIMESTAMPTZ
        WHEN game_pause.resumed_deadline IS NOT NULL THEN game_pause.resumed_deadline
        WHEN game_pause.state = 'active' THEN NULL::TIMESTAMPTZ
        WHEN attempt.started_at IS NOT NULL
            THEN attempt.started_at + (snapshot.time_limit * INTERVAL '1 second')
        ELSE NULL::TIMESTAMPTZ
    END AS effective_deadline,
    transaction_timestamp()::TIMESTAMPTZ AS observed_at
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
JOIN game_slots AS game_slot
    ON game_slot.id = attempt.slot_id
    AND game_slot.series_id = attempt.series_id
    AND game_slot.roster_id = attempt.roster_id
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
JOIN LATERAL (
    SELECT wave_series.wave_id
    FROM wave_series
    JOIN waves AS wave ON wave.id = wave_series.wave_id
    WHERE wave_series.series_id = assignment.series_id
    ORDER BY (wave.state <> 'superseded') DESC,
        wave.updated_at DESC,
        wave.id DESC
    LIMIT 1
) AS current_wave ON TRUE
JOIN LATERAL (
    SELECT stage.stage,
        stage.round_number
    FROM (
        SELECT 'swiss'::TEXT AS stage,
            swiss_round.round_number::SMALLINT AS round_number,
            2 AS stage_rank,
            swiss_round.updated_at AS evidence_at,
            swiss_round.id AS evidence_id
        FROM wave_series AS linked_series
        JOIN swiss_wave_links AS swiss_link
            ON swiss_link.wave_id = linked_series.wave_id
            AND swiss_link.tournament_id = linked_series.tournament_id
            AND swiss_link.roster_id = linked_series.roster_id
        JOIN swiss_rounds AS swiss_round
            ON swiss_round.id = swiss_link.round_id
            AND swiss_round.roster_id = swiss_link.roster_id
        WHERE linked_series.series_id = assignment.series_id
            AND linked_series.tournament_id = assignment_series.tournament_id
            AND linked_series.roster_id = assignment.roster_id

        UNION ALL

        SELECT 'semifinal'::TEXT,
            NULL::SMALLINT,
            1,
            semifinal.created_at,
            semifinal.command_id
        FROM tournament_stage_playoff_semifinals AS semifinal
        WHERE semifinal.tournament_id = assignment_series.tournament_id
            AND semifinal.roster_id = assignment.roster_id
            AND semifinal.series_id = assignment.series_id

        UNION ALL

        SELECT 'final'::TEXT,
            NULL::SMALLINT,
            0,
            final_stage.created_at,
            final_stage.command_id
        FROM tournament_stage_playoff_finals AS final_stage
        WHERE final_stage.tournament_id = assignment_series.tournament_id
            AND final_stage.roster_id = assignment.roster_id
            AND final_stage.final_series_id = assignment.series_id
    ) AS stage
    ORDER BY stage.stage_rank,
        stage.round_number DESC NULLS LAST,
        stage.evidence_at DESC,
        stage.evidence_id DESC
    LIMIT 1
) AS assignment_stage ON TRUE
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
LEFT JOIN LATERAL (
    SELECT pause.id AS pause_id,
        pause.game_attempt_id,
        pause.state,
        pause.started_at,
        clock.original_deadline,
        clock.frozen_at,
        clock.frozen_remaining_ms,
        clock.resumed_at,
        clock.resumed_deadline
    FROM pauses AS pause
    LEFT JOIN pause_clocks AS clock
        ON clock.pause_id = pause.id
        AND clock.game_attempt_id = attempt.id
    WHERE pause.scope_kind = 'game_attempt'
        AND pause.game_attempt_id = attempt.id
    ORDER BY pause.created_at DESC,
        pause.id DESC
    LIMIT 1
) AS game_pause ON TRUE
WHERE roster.tournament_id = sqlc.arg(tournament_id)
    AND participant.player_id = sqlc.arg(player_id)
    AND attempt.state NOT IN ('void', 'cancelled', 'superseded')
    AND assignment_series.state NOT IN ('completed', 'cancelled')
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
        current_draft.id IS NOT NULL
        AND series.state NOT IN ('completed', 'cancelled', 'superseded')
    ) DESC,
    (
        series.id IS NOT DISTINCT FROM sqlc.narg(preferred_series_id)::UUID
    ) DESC,
    (series.state NOT IN ('completed', 'cancelled', 'superseded')) DESC,
    series.updated_at DESC,
    series.id DESC
LIMIT 1;

-- name: GetParticipantArchiveSource :one
WITH authorized_source AS (
    SELECT snapshot.task_id,
        snapshot.source_file_url,
        0 AS source_rank
    FROM participants AS participant
    INNER JOIN rosters AS roster ON roster.id = participant.roster_id
    INNER JOIN task_delivery_receipts AS receipt
        ON receipt.roster_id = roster.id
        AND receipt.participant_id = participant.id
    INNER JOIN assignments AS assignment
        ON assignment.id = receipt.assignment_id
        AND assignment.attempt_id = receipt.attempt_id
        AND assignment.roster_id = receipt.roster_id
    INNER JOIN task_snapshots AS snapshot
        ON snapshot.id = assignment.snapshot_id
        AND snapshot.id = receipt.snapshot_id
        AND snapshot.task_id = assignment.task_id
        AND snapshot.task_id = receipt.task_id
        AND snapshot.task_version = assignment.task_version
        AND snapshot.task_version = receipt.task_version
    WHERE roster.tournament_id = sqlc.arg(tournament_id)
        AND participant.player_id = sqlc.arg(player_id)
        AND assignment.id = sqlc.arg(assignment_id)

    UNION ALL

    SELECT snapshot.task_id,
        snapshot.source_file_url,
        1 AS source_rank
    FROM golden_runtime_assignments AS runtime
    INNER JOIN golden_memberships AS membership
        ON membership.attempt_id = runtime.attempt_id
    INNER JOIN participants AS participant
        ON participant.id = membership.participant_id
        AND participant.roster_id = runtime.roster_id
    INNER JOIN task_snapshots AS snapshot
        ON snapshot.id = runtime.snapshot_id
        AND snapshot.reservation_id = runtime.assignment_id
        AND snapshot.task_id = runtime.task_id
        AND snapshot.task_version = runtime.task_version
        AND snapshot.kind = 'golden'
        AND snapshot.content_digest = runtime.source_digest
    WHERE runtime.tournament_id = sqlc.arg(tournament_id)
        AND participant.player_id = sqlc.arg(player_id)
        AND runtime.assignment_id = sqlc.arg(assignment_id)
        AND runtime.started_at IS NOT NULL
        AND membership.participation_established_at IS NOT NULL
)
SELECT task_id,
    source_file_url
FROM authorized_source
ORDER BY source_rank
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
    swiss_link.bye_participant_id,
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
LEFT JOIN swiss_wave_links AS swiss_link
    ON swiss_link.wave_id = wave.id
    AND swiss_link.tournament_id = wave.tournament_id
    AND swiss_link.roster_id = wave.roster_id
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
