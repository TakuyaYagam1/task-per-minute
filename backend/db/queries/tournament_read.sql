-- name: GetTournamentReadCursor :one
SELECT revision.revision_number AS projection_revision,
    COALESCE(outbox_cursor.next_sequence - 1, 0)::BIGINT AS event_sequence,
    transaction_timestamp()::TIMESTAMPTZ AS observed_at
FROM projection_revisions AS revision
LEFT JOIN tournament_outbox_cursors AS outbox_cursor
    ON outbox_cursor.tournament_id = revision.tournament_id
WHERE revision.tournament_id = sqlc.arg(tournament_id)
    AND revision.state = 'published';

-- name: GetParticipantReadIdentity :one
SELECT participant.id
FROM participants AS participant
JOIN rosters AS roster ON roster.id = participant.roster_id
WHERE roster.tournament_id = sqlc.arg(tournament_id)
    AND participant.player_id = sqlc.arg(player_id);

-- name: GetParticipantReadAssignment :one
SELECT assignment.id AS assignment_id,
    assignment.attempt_id,
    assignment.series_id,
    attempt.id AS game_id,
    COALESCE(current_wave.wave_id::TEXT, '')::TEXT AS wave_id,
    snapshot.id AS snapshot_id,
    snapshot.task_id,
    snapshot.title,
    snapshot.category,
    snapshot.difficulty,
    snapshot.time_limit AS time_limit_seconds
FROM participants AS participant
JOIN rosters AS roster ON roster.id = participant.roster_id
JOIN task_delivery_receipts AS receipt
    ON receipt.roster_id = participant.roster_id
    AND receipt.participant_id = participant.id
JOIN assignments AS assignment
    ON assignment.id = receipt.assignment_id
    AND assignment.attempt_id = receipt.attempt_id
    AND assignment.roster_id = receipt.roster_id
JOIN task_snapshots AS snapshot
    ON snapshot.id = receipt.snapshot_id
    AND snapshot.task_id = receipt.task_id
    AND snapshot.task_version = receipt.task_version
JOIN game_attempts AS attempt ON attempt.id = assignment.attempt_id
JOIN series AS assignment_series
    ON assignment_series.id = assignment.series_id
    AND assignment_series.roster_id = assignment.roster_id
LEFT JOIN LATERAL (
    SELECT wave_series.wave_id
    FROM wave_series
    JOIN waves AS wave ON wave.id = wave_series.wave_id
    WHERE wave_series.series_id = assignment.series_id
        AND wave.state <> 'superseded'
    ORDER BY wave.created_at DESC,
        wave.id DESC
    LIMIT 1
) AS current_wave ON TRUE
WHERE roster.tournament_id = sqlc.arg(tournament_id)
    AND participant.player_id = sqlc.arg(player_id)
    AND assignment.state = 'active'
    AND assignment_series.state NOT IN ('completed', 'cancelled')
ORDER BY receipt.delivered_at DESC,
    receipt.id DESC
LIMIT 1;

-- Read the current assignment's game together with the latest participant-safe
-- pause clock and official outcome. The pause remains visible after resume so
-- clients can reconcile the frozen and resumed deadlines; reconnect lineage
-- and presence are loaded by the scoped companion queries below.
-- name: GetParticipantReadGame :one
SELECT attempt.id AS game_id,
    attempt.state,
    attempt.revision,
    attempt.result_reason,
    attempt.winner_id,
    attempt.result_revision_id,
    COALESCE(latest_pause.pause_id, '00000000-0000-0000-0000-000000000000'::UUID) AS pause_id,
    COALESCE(latest_pause.pause_state, '')::TEXT AS pause_state,
    COALESCE(latest_pause.pause_reason, '')::TEXT AS pause_reason,
    latest_pause.frozen_at,
    latest_pause.frozen_remaining_ms,
    latest_pause.resumed_at,
    latest_pause.resumed_deadline,
    open_reconnect.deadline_at AS reconnect_deadline
FROM game_attempts AS attempt
JOIN series
    ON series.id = attempt.series_id
    AND series.roster_id = attempt.roster_id
LEFT JOIN LATERAL (
    SELECT pause.id AS pause_id,
        pause.state AS pause_state,
        pause.reason AS pause_reason,
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
        AND pause.series_id = attempt.series_id
        AND pause.roster_id = attempt.roster_id
    ORDER BY pause.started_at DESC,
        pause.id DESC
    LIMIT 1
) AS latest_pause ON TRUE
LEFT JOIN LATERAL (
    SELECT reconnect.deadline_at
    FROM reconnect_intervals AS reconnect
    WHERE reconnect.game_attempt_id = attempt.id
        AND reconnect.series_id = attempt.series_id
        AND reconnect.roster_id = attempt.roster_id
        AND reconnect.state = 'open'
    ORDER BY reconnect.opened_at DESC,
        reconnect.id DESC
    LIMIT 1
) AS open_reconnect ON TRUE
WHERE series.tournament_id = sqlc.arg(tournament_id)
    AND attempt.series_id = sqlc.arg(series_id)
    AND attempt.id = sqlc.arg(game_id);

-- name: ListParticipantReadPresence :many
SELECT presence.participant_id,
    presence.state,
    presence.presence_epoch,
    presence.revision,
    presence.connected_at,
    presence.disconnected_at,
    presence.updated_at
FROM presence_states AS presence
JOIN series
    ON series.id = presence.series_id
    AND series.roster_id = presence.roster_id
WHERE series.tournament_id = sqlc.arg(tournament_id)
    AND presence.series_id = sqlc.arg(series_id)
ORDER BY presence.participant_id;

-- name: ListParticipantReadReconnect :many
SELECT DISTINCT ON (reconnect.participant_id)
    reconnect.id,
    reconnect.pause_id,
    reconnect.participant_id,
    reconnect.presence_epoch,
    reconnect.interval_number,
    reconnect.continuation_number,
    reconnect.continued_from_id,
    reconnect.suspended_by_pause_id,
    reconnect.state,
    reconnect.opened_at,
    reconnect.deadline_at,
    reconnect.closed_at,
    reconnect.revision,
    reconnect.updated_at
FROM reconnect_intervals AS reconnect
JOIN series
    ON series.id = reconnect.series_id
    AND series.roster_id = reconnect.roster_id
WHERE series.tournament_id = sqlc.arg(tournament_id)
    AND reconnect.series_id = sqlc.arg(series_id)
    AND reconnect.game_attempt_id = sqlc.arg(game_id)
ORDER BY reconnect.participant_id,
    reconnect.interval_number DESC,
    reconnect.continuation_number DESC,
    reconnect.updated_at DESC,
    reconnect.id DESC;

-- name: GetParticipantReadOpponent :one
SELECT opponent_player.id AS player_id,
    opponent_player.username AS display_name,
    series.id AS series_id,
    COALESCE(readiness.ready, FALSE)::BOOLEAN AS ready,
    series.state AS series_state,
    CASE
        WHEN series.first_participant_id = participant.id
            THEN series.second_participant_wins
        ELSE series.first_participant_wins
    END::INTEGER AS score
FROM participants AS participant
JOIN rosters AS roster ON roster.id = participant.roster_id
JOIN series
    ON series.roster_id = participant.roster_id
    AND participant.id IN (
        series.first_participant_id,
        series.second_participant_id
    )
JOIN participants AS opponent
    ON opponent.roster_id = series.roster_id
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
        wave.created_at DESC,
        wave.id DESC
    LIMIT 1
) AS current_wave ON TRUE
LEFT JOIN wave_readiness AS readiness
    ON readiness.wave_id = current_wave.wave_id
    AND readiness.participant_id = opponent.id
WHERE roster.tournament_id = sqlc.arg(tournament_id)
    AND participant.player_id = sqlc.arg(player_id)
    AND (
        sqlc.narg(series_id)::UUID IS NULL
        OR series.id = sqlc.narg(series_id)::UUID
    )
    AND series.state NOT IN ('completed', 'cancelled', 'superseded')
ORDER BY series.updated_at DESC,
    series.id DESC
LIMIT 1;

-- name: GetPublicTournamentReadSummary :one
SELECT tournament.id AS tournament_id,
    tournament.preset,
    tournament.state,
    COUNT(participant.id)::INTEGER AS roster_size,
    tournament.started_at,
    tournament.finished_at
FROM tournaments AS tournament
JOIN rosters AS roster ON roster.tournament_id = tournament.id
LEFT JOIN participants AS participant ON participant.roster_id = roster.id
WHERE tournament.id = sqlc.arg(tournament_id)
GROUP BY tournament.id;

-- name: GetTournamentReadProjectionPayloads :one
SELECT standings.payload AS standings_payload,
    bracket.payload AS bracket_payload,
    top_four.payload AS top_four_payload
FROM projection_revisions AS revision
JOIN projection_revision_artifacts AS standings_link
    ON standings_link.revision_id = revision.id
    AND standings_link.artifact_kind = 'standings'
JOIN projection_artifacts AS standings
    ON standings.id = standings_link.artifact_id
    AND standings.artifact_kind = standings_link.artifact_kind
JOIN projection_revision_artifacts AS bracket_link
    ON bracket_link.revision_id = revision.id
    AND bracket_link.artifact_kind = 'bracket'
JOIN projection_artifacts AS bracket
    ON bracket.id = bracket_link.artifact_id
    AND bracket.artifact_kind = bracket_link.artifact_kind
LEFT JOIN projection_revision_artifacts AS top_four_link
    ON top_four_link.revision_id = revision.id
    AND top_four_link.artifact_kind = 'top_four'
LEFT JOIN projection_artifacts AS top_four
    ON top_four.id = top_four_link.artifact_id
    AND top_four.artifact_kind = top_four_link.artifact_kind
WHERE revision.tournament_id = sqlc.arg(tournament_id)
    AND revision.state = 'published';

-- name: ListTournamentReadParticipants :many
SELECT participant.id AS participant_id,
    player.username AS display_name,
    participant.seed
FROM participants AS participant
JOIN rosters AS roster ON roster.id = participant.roster_id
JOIN players AS player ON player.id = participant.player_id
WHERE roster.tournament_id = sqlc.arg(tournament_id)
ORDER BY participant.seed,
    participant.id;

-- name: ListPublicTournamentReadSwissRounds :many
SELECT swiss_round.round_number,
    COALESCE(current_wave.state, 'planned')::TEXT AS state,
    COALESCE(bye_player.username, '')::TEXT AS bye_display_name,
    swiss_bye.points_awarded AS bye_points_awarded
FROM swiss_rounds AS swiss_round
JOIN rosters AS roster
    ON roster.id = swiss_round.roster_id
LEFT JOIN LATERAL (
    SELECT wave.state,
        link.bye_participant_id
    FROM swiss_wave_links AS link
    JOIN waves AS wave
        ON wave.id = link.wave_id
        AND wave.tournament_id = link.tournament_id
        AND wave.roster_id = link.roster_id
    WHERE link.round_id = swiss_round.id
        AND link.tournament_id = roster.tournament_id
        AND link.roster_id = swiss_round.roster_id
    ORDER BY (wave.state <> 'superseded') DESC,
        wave.created_at DESC,
        wave.id DESC
    LIMIT 1
) AS current_wave ON TRUE
LEFT JOIN swiss_byes AS swiss_bye
    ON swiss_bye.round_id = swiss_round.id
    AND swiss_bye.roster_id = swiss_round.roster_id
LEFT JOIN participants AS bye_participant
    ON bye_participant.id = COALESCE(current_wave.bye_participant_id, swiss_bye.participant_id)
    AND bye_participant.roster_id = swiss_round.roster_id
LEFT JOIN players AS bye_player ON bye_player.id = bye_participant.player_id
WHERE roster.tournament_id = sqlc.arg(tournament_id)
ORDER BY swiss_round.round_number;

-- name: ListPublicTournamentReadSeries :many
SELECT series.id AS series_id,
    COALESCE(series_stage.stage, 'golden')::TEXT AS stage,
    COALESCE(series_stage.round_number, 0)::SMALLINT AS round_number,
    series.format,
    series.state,
    first_player.username AS first_display_name,
    second_player.username AS second_display_name,
    series.first_participant_wins,
    series.second_participant_wins,
    COALESCE(current_slot.slot_number, 0)::INTEGER AS current_game_position,
    NULL::TIMESTAMPTZ AS scheduled_at
FROM series
JOIN participants AS first_participant
    ON first_participant.id = series.first_participant_id
JOIN players AS first_player ON first_player.id = first_participant.player_id
JOIN participants AS second_participant
    ON second_participant.id = series.second_participant_id
JOIN players AS second_player ON second_player.id = second_participant.player_id
LEFT JOIN LATERAL (
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
        WHERE linked_series.series_id = series.id
            AND linked_series.tournament_id = series.tournament_id
            AND linked_series.roster_id = series.roster_id

        UNION ALL

        SELECT 'semifinal'::TEXT,
            NULL::SMALLINT,
            1,
            semifinal.created_at,
            semifinal.command_id
        FROM tournament_stage_playoff_semifinals AS semifinal
        WHERE semifinal.tournament_id = series.tournament_id
            AND semifinal.roster_id = series.roster_id
            AND semifinal.series_id = series.id

        UNION ALL

        SELECT 'final'::TEXT,
            NULL::SMALLINT,
            0,
            final_stage.created_at,
            final_stage.command_id
        FROM tournament_stage_playoff_finals AS final_stage
        WHERE final_stage.tournament_id = series.tournament_id
            AND final_stage.roster_id = series.roster_id
            AND final_stage.final_series_id = series.id
    ) AS stage
    ORDER BY stage.stage_rank,
        stage.round_number DESC NULLS LAST,
        stage.evidence_at DESC,
        stage.evidence_id DESC
    LIMIT 1
) AS series_stage ON TRUE
LEFT JOIN LATERAL (
    SELECT slot.slot_number
    FROM game_slots AS slot
    JOIN game_attempts AS attempt ON attempt.slot_id = slot.id
    WHERE slot.series_id = series.id
    ORDER BY attempt.created_at DESC,
        attempt.attempt_number DESC
    LIMIT 1
) AS current_slot ON TRUE
WHERE series.tournament_id = sqlc.arg(tournament_id)
    AND series.state <> 'superseded'
ORDER BY series.created_at,
    series.id;

-- name: ListPublicTournamentReadResults :many
SELECT revision.id AS revision_id,
    series.id AS series_id,
    revision.result_state AS state,
    COALESCE(winner_player.username, '')::TEXT AS winner_display_name,
    series.first_participant_wins,
    series.second_participant_wins,
    revision.created_at AS recorded_at
FROM official_result_heads AS head
JOIN official_result_revisions AS revision
    ON revision.id = head.current_revision_id
JOIN series ON series.id = head.series_id
LEFT JOIN participants AS winner ON winner.id = revision.winner_id
LEFT JOIN players AS winner_player ON winner_player.id = winner.player_id
WHERE revision.tournament_id = sqlc.arg(tournament_id)
    AND head.entity_kind = 'series'
ORDER BY revision.created_at,
    revision.id;

-- name: GetPublicTournamentReadDraft :one
SELECT draft.id AS draft_id,
    draft.series_id,
    draft.format,
    revision.state,
    category.category_pool,
    revision.selected_categories
FROM drafts AS draft
JOIN series ON series.id = draft.series_id
JOIN category_revisions AS category ON category.id = draft.category_revision_id
JOIN LATERAL (
    SELECT draft_revision.state,
        draft_revision.selected_categories
    FROM draft_revisions AS draft_revision
    WHERE draft_revision.draft_id = draft.id
    ORDER BY draft_revision.revision DESC
    LIMIT 1
) AS revision ON TRUE
WHERE series.tournament_id = sqlc.arg(tournament_id)
    AND revision.state <> 'superseded'
ORDER BY draft.created_at DESC,
    draft.id DESC
LIMIT 1;

-- name: ListPublicTournamentReadDraftActions :many
SELECT action.turn_number AS turn,
    action.action,
    action.category,
    COALESCE(player.username, '')::TEXT AS actor_display_name,
    action.occurred_at
FROM draft_actions AS action
JOIN drafts AS draft ON draft.id = action.draft_id
LEFT JOIN participants AS participant
    ON participant.roster_id = draft.roster_id
    AND participant.id = action.actor_id
LEFT JOIN players AS player ON player.id = participant.player_id
WHERE action.draft_id = sqlc.arg(draft_id)
ORDER BY action.turn_number,
    action.id;

-- name: ListOperatorTournamentReadWaves :many
SELECT wave.id AS wave_id,
    wave.state,
    ready_window.deadline AS window_deadline
FROM waves AS wave
LEFT JOIN ready_windows AS ready_window ON ready_window.wave_id = wave.id
WHERE wave.tournament_id = sqlc.arg(tournament_id)
ORDER BY wave.created_at,
    wave.id;

-- name: ListOperatorTournamentReadWaveMembers :many
SELECT member.wave_id,
    member.participant_id,
    COALESCE(member_series.series_id::TEXT, '')::TEXT AS series_id,
    COALESCE(member_series.series_count, 0)::BIGINT AS series_count,
    COALESCE(readiness.ready, FALSE)::BOOLEAN AS ready,
    COALESCE(readiness.revision, 0)::BIGINT AS readiness_revision
FROM wave_members AS member
JOIN waves AS wave ON wave.id = member.wave_id
LEFT JOIN wave_readiness AS readiness
    ON readiness.wave_id = member.wave_id
    AND readiness.participant_id = member.participant_id
LEFT JOIN LATERAL (
    SELECT wave_series.series_id,
        COUNT(*) OVER ()::BIGINT AS series_count
    FROM wave_series
    JOIN series ON series.id = wave_series.series_id
    WHERE wave_series.wave_id = member.wave_id
        AND series.state <> 'superseded'
        AND member.participant_id IN (
            series.first_participant_id,
            series.second_participant_id
        )
    ORDER BY wave_series.series_id
    LIMIT 1
) AS member_series ON TRUE
WHERE wave.tournament_id = sqlc.arg(tournament_id)
ORDER BY member.wave_id,
    member.participant_id;

-- name: ListOperatorTournamentReadPresence :many
SELECT presence.participant_id,
    presence.series_id,
    presence.state,
    presence.presence_epoch,
    presence.updated_at
FROM presence_states AS presence
WHERE presence.tournament_id = sqlc.arg(tournament_id)
ORDER BY presence.participant_id,
    presence.series_id;

-- name: ListOperatorTournamentReadReplays :many
SELECT replacement_link.series_id,
    slot.id AS slot_id,
    failed.id AS failed_game_id,
    replacement.id AS replacement_game_id,
    replacement_wave.id AS replacement_wave_id,
    replacement.state,
    replacement.revision
FROM waves AS replacement_wave
JOIN waves AS failed_wave ON failed_wave.id = replacement_wave.replaces_wave_id
JOIN wave_series AS replacement_link
    ON replacement_link.wave_id = replacement_wave.id
JOIN wave_series AS failed_link
    ON failed_link.wave_id = failed_wave.id
    AND failed_link.series_id = replacement_link.series_id
JOIN game_slots AS slot ON slot.series_id = replacement_link.series_id
JOIN game_attempts AS replacement
    ON replacement.slot_id = slot.id
    AND replacement.created_at = replacement_wave.created_at
JOIN game_attempts AS failed
    ON failed.slot_id = replacement.slot_id
    AND failed.attempt_number = replacement.attempt_number - 1
    AND failed.state = 'void'
WHERE replacement_wave.tournament_id = sqlc.arg(tournament_id)
ORDER BY replacement_wave.created_at,
    replacement_wave.id,
    slot.slot_number;

-- name: GetOperatorTournamentReadPause :one
SELECT pause.id AS pause_id,
    pause.state,
    pause.reason,
    pause.started_at AS paused_at,
    pause.revision AS graph_revision
FROM pauses AS pause
WHERE pause.tournament_id = sqlc.arg(tournament_id)
    AND pause.state = 'active'
ORDER BY pause.depth DESC,
    pause.started_at DESC,
    pause.id DESC
LIMIT 1;

-- Nullable game fields keep operator snapshots backward compatible for
-- tournament, wave, and series pauses.  The adapter validates that a game
-- pause always has a frozen clock before exposing the optional values.
-- name: GetOperatorTournamentReadPauseGame :one
SELECT pause.game_attempt_id AS game_id,
    clock.frozen_remaining_ms,
    open_reconnect.deadline_at AS reconnect_deadline
FROM pauses AS pause
LEFT JOIN pause_clocks AS clock
    ON clock.pause_id = pause.id
    AND clock.game_attempt_id = pause.game_attempt_id
LEFT JOIN LATERAL (
    SELECT reconnect.deadline_at
    FROM reconnect_intervals AS reconnect
    WHERE reconnect.pause_id = pause.id
        AND reconnect.game_attempt_id = pause.game_attempt_id
        AND reconnect.state = 'open'
    ORDER BY reconnect.opened_at DESC,
        reconnect.id DESC
    LIMIT 1
) AS open_reconnect ON TRUE
WHERE pause.id = sqlc.arg(pause_id);

-- name: ListOperatorTournamentReadAuditLinks :many
SELECT event.id AS audit_event_id,
    revision.entity_kind,
    revision.entity_id,
    revision.id AS official_result_revision_id
FROM official_result_heads AS head
JOIN official_result_revisions AS revision
    ON revision.id = head.current_revision_id
JOIN audit_events AS event
    ON event.result_event_id = revision.result_event_id
    AND event.series_id = revision.series_id
    AND event.roster_id = revision.roster_id
WHERE revision.tournament_id = sqlc.arg(tournament_id)
ORDER BY event.created_at,
    event.id;

-- Recovery controls are projected only from durable replay authority and
-- failure/closure evidence. A replay control is present only while the
-- persisted operator reserve still has its exact committed replacement edge,
-- reservation, and snapshot.
-- name: ListTournamentAdminRecoveryControls :many
WITH replay_controls AS (
    SELECT 'replay'::TEXT AS control_kind,
        assignment.id AS assignment_id,
        slot.series_id,
        slot.id AS slot_id,
        slot.category::TEXT AS category,
        operator_reserve.resulting_series_revision AS expected_authority_revision,
        old_wave.id AS old_wave_id,
        NULL::TEXT AS pause_reason,
        failed.result_reason::TEXT AS reason,
        old_wave.revision_id AS replay_expected_closure_revision_id,
        NULL::UUID AS exhaustion_command_id,
        NULL::UUID AS current_snapshot_id,
        NULL::BIGINT AS expected_assignment_revision,
        NULL::BIGINT AS expected_pool_revision,
        NULL::UUID AS expected_pool_revision_id,
        NULL::BIGINT AS expected_history_revision,
        NULL::UUID AS expected_history_revision_id,
        NULL::BIGINT AS expected_artifact_revision,
        NULL::UUID AS expected_artifact_revision_id,
        NULL::BIGINT AS expected_reservation_revision,
        NULL::UUID AS expected_reservation_revision_id,
        NULL::BIGINT AS expected_category_revision,
        NULL::UUID AS expected_category_revision_id,
        NULL::UUID AS expected_snapshot_id
    FROM game_attempts AS failed
    JOIN game_slots AS slot
        ON slot.id = failed.slot_id
        AND slot.series_id = failed.series_id
        AND slot.roster_id = failed.roster_id
    JOIN series
        ON series.id = failed.series_id
        AND series.roster_id = failed.roster_id
    JOIN assignments AS assignment
        ON assignment.attempt_id = failed.id
        AND assignment.series_id = failed.series_id
        AND assignment.roster_id = failed.roster_id
        AND assignment.state = 'active'
    JOIN assignment_plans AS plan
        ON plan.id = assignment.plan_id
    JOIN replay_reserve_authorities AS authority
        ON authority.assignment_id = assignment.id
        AND authority.tournament_id = series.tournament_id
        AND authority.roster_id = series.roster_id
        AND authority.series_id = series.id
        AND authority.slot_id = slot.id
        AND authority.assignment_attempt_id = failed.id
        AND authority.active_snapshot_id = assignment.snapshot_id
        AND authority.assignment_revision = assignment.revision
    JOIN task_version_reservations AS current_reservation
        ON current_reservation.id = assignment.reservation_id
        AND current_reservation.plan_id = assignment.plan_id
        AND current_reservation.branch_id = assignment.branch_id
        AND current_reservation.state = 'committed'
    JOIN assignment_plan_edges AS current_edge
        ON current_edge.id = current_reservation.edge_id
        AND current_edge.plan_id = assignment.plan_id
        AND current_edge.branch_id = assignment.branch_id
    JOIN wave_member_routes AS route
        ON route.game_attempt_id = failed.id
        AND route.tournament_id = series.tournament_id
        AND route.roster_id = series.roster_id
        AND route.series_id = series.id
        AND route.slot_id = slot.id
    JOIN waves AS old_wave
        ON old_wave.id = route.wave_id
        AND old_wave.tournament_id = series.tournament_id
        AND old_wave.roster_id = series.roster_id
        AND old_wave.state = 'completed'
    JOIN operator_replay_reserves AS operator_reserve
        ON operator_reserve.tournament_id = series.tournament_id
        AND operator_reserve.roster_id = series.roster_id
        AND operator_reserve.old_wave_id = old_wave.id
        AND operator_reserve.series_id = series.id
        AND operator_reserve.slot_id = slot.id
        AND operator_reserve.assignment_id = assignment.id
        AND operator_reserve.assignment_attempt_id = failed.id
        AND operator_reserve.failed_game_id = failed.id
        AND operator_reserve.closure_revision_id = old_wave.revision_id
        AND operator_reserve.reserve_position > COALESCE(plan.reserve_count::integer, 2) + 1
        AND operator_reserve.from_snapshot_id = authority.active_snapshot_id
        AND operator_reserve.resulting_series_revision = series.revision
    JOIN replay_reserve_exhaustions AS exhaustion
        ON exhaustion.command_id = operator_reserve.exhaustion_command_id
        AND exhaustion.tournament_id = series.tournament_id
        AND exhaustion.roster_id = series.roster_id
        AND exhaustion.old_wave_id = old_wave.id
        AND exhaustion.series_id = series.id
        AND exhaustion.slot_id = slot.id
        AND exhaustion.assignment_id = assignment.id
        AND exhaustion.assignment_attempt_id = failed.id
        AND exhaustion.failed_game_id = failed.id
        AND exhaustion.closure_revision_id = old_wave.revision_id
        AND exhaustion.active_snapshot_id = authority.active_snapshot_id
        AND exhaustion.reserve_position = COALESCE(plan.reserve_count::integer, 2) + 1
        AND exhaustion.category = slot.category
        AND exhaustion.resulting_series_revision = operator_reserve.source_series_revision
    JOIN assignment_plan_edges AS replacement_edge
        ON replacement_edge.id = operator_reserve.edge_id
        AND replacement_edge.plan_id = assignment.plan_id
        AND replacement_edge.branch_id = assignment.branch_id
        AND replacement_edge.operator_reserve_command_id = operator_reserve.command_id
        AND replacement_edge.position = current_edge.position + 1
        AND replacement_edge.task_id = operator_reserve.proposed_task_id
        AND replacement_edge.task_version = operator_reserve.proposed_version
    JOIN task_version_reservations AS replacement_reservation
        ON replacement_reservation.id = operator_reserve.reservation_id
        AND replacement_reservation.edge_id = replacement_edge.id
        AND replacement_reservation.plan_id = replacement_edge.plan_id
        AND replacement_reservation.branch_id = replacement_edge.branch_id
        AND replacement_reservation.task_id = replacement_edge.task_id
        AND replacement_reservation.task_version = replacement_edge.task_version
        AND replacement_reservation.state = 'committed'
        AND replacement_reservation.disclosed_at IS NULL
    JOIN task_snapshots AS replacement_snapshot
        ON replacement_snapshot.id = operator_reserve.proposed_snapshot_id
        AND replacement_snapshot.reservation_id = replacement_reservation.id
        AND replacement_snapshot.task_id = replacement_reservation.task_id
        AND replacement_snapshot.task_version = replacement_reservation.task_version
    JOIN wave_series AS old_membership
        ON old_membership.wave_id = old_wave.id
        AND old_membership.series_id = series.id
    WHERE series.tournament_id = sqlc.arg(tournament_id)
        AND series.state = 'replay_required'
        AND failed.state = 'void'
        AND failed.result_reason IN (
            'no_solve',
            'task_failure',
            'common_platform_failure',
            'disconnect',
            'execution_epoch_break'
        )
        AND route.category = slot.category
        AND NOT EXISTS (
            SELECT 1
            FROM replay_replacements AS replacement
            WHERE replacement.assignment_id = assignment.id
                AND replacement.failed_game_id = failed.id
        )
), exhausted_controls AS (
    SELECT 'reserve_exhausted'::TEXT AS control_kind,
        exhaustion.assignment_id,
        exhaustion.series_id,
        exhaustion.slot_id,
        exhaustion.category::TEXT AS category,
        exhaustion.resulting_series_revision AS expected_authority_revision,
        exhaustion.old_wave_id,
        NULL::TEXT AS pause_reason,
        'replay reserves exhausted'::TEXT AS reason,
        exhaustion.closure_revision_id AS replay_expected_closure_revision_id,
        exhaustion.command_id AS exhaustion_command_id,
        authority.active_snapshot_id AS current_snapshot_id,
        authority.assignment_revision AS expected_assignment_revision,
        authority.pool_revision AS expected_pool_revision,
        authority.pool_revision_id AS expected_pool_revision_id,
        authority.history_revision AS expected_history_revision,
        authority.history_revision_id AS expected_history_revision_id,
        authority.artifact_revision AS expected_artifact_revision,
        authority.artifact_revision_id AS expected_artifact_revision_id,
        authority.reservation_revision AS expected_reservation_revision,
        authority.reservation_revision_id AS expected_reservation_revision_id,
        authority.category_revision AS expected_category_revision,
        authority.category_revision_id AS expected_category_revision_id,
        authority.active_snapshot_id AS expected_snapshot_id
    FROM replay_reserve_exhaustions AS exhaustion
    JOIN series
        ON series.id = exhaustion.series_id
        AND series.tournament_id = exhaustion.tournament_id
        AND series.roster_id = exhaustion.roster_id
    JOIN waves AS old_wave
        ON old_wave.id = exhaustion.old_wave_id
        AND old_wave.tournament_id = exhaustion.tournament_id
        AND old_wave.roster_id = exhaustion.roster_id
        AND old_wave.state = 'completed'
        AND old_wave.revision_id = exhaustion.closure_revision_id
    JOIN game_slots AS slot
        ON slot.id = exhaustion.slot_id
        AND slot.series_id = exhaustion.series_id
        AND slot.roster_id = exhaustion.roster_id
    JOIN game_attempts AS failed
        ON failed.id = exhaustion.failed_game_id
        AND failed.slot_id = exhaustion.slot_id
        AND failed.series_id = exhaustion.series_id
        AND failed.roster_id = exhaustion.roster_id
        AND failed.state = 'void'
        AND failed.result_reason IN (
            'no_solve',
            'task_failure',
            'common_platform_failure',
            'disconnect',
            'execution_epoch_break'
        )
    JOIN assignments AS assignment
        ON assignment.id = exhaustion.assignment_id
        AND assignment.attempt_id = exhaustion.assignment_attempt_id
        AND assignment.series_id = exhaustion.series_id
        AND assignment.roster_id = exhaustion.roster_id
        AND assignment.state = 'active'
    JOIN assignment_plans AS plan
        ON plan.id = assignment.plan_id
    JOIN replay_reserve_authorities AS authority
        ON authority.assignment_id = exhaustion.assignment_id
        AND authority.tournament_id = exhaustion.tournament_id
        AND authority.roster_id = exhaustion.roster_id
        AND authority.series_id = exhaustion.series_id
        AND authority.slot_id = exhaustion.slot_id
        AND authority.assignment_attempt_id = exhaustion.assignment_attempt_id
        AND authority.active_snapshot_id = exhaustion.active_snapshot_id
        AND authority.assignment_revision = assignment.revision
        AND authority.required_category = exhaustion.category
    JOIN wave_member_routes AS route
        ON route.game_attempt_id = failed.id
        AND route.wave_id = exhaustion.old_wave_id
        AND route.tournament_id = exhaustion.tournament_id
        AND route.roster_id = exhaustion.roster_id
        AND route.series_id = exhaustion.series_id
        AND route.slot_id = exhaustion.slot_id
    WHERE exhaustion.tournament_id = sqlc.arg(tournament_id)
        AND exhaustion.reserve_position = COALESCE(plan.reserve_count::integer, 2) + 1
        AND series.state = 'technical_pause'
        AND series.revision = exhaustion.resulting_series_revision
        AND exhaustion.category = slot.category
        AND route.category = slot.category
)
SELECT control.control_kind,
    control.assignment_id,
    control.series_id,
    control.slot_id,
    control.category,
    control.expected_authority_revision,
    control.old_wave_id,
    control.pause_reason,
    control.reason,
    control.replay_expected_closure_revision_id,
    control.exhaustion_command_id,
    control.current_snapshot_id,
    control.expected_assignment_revision,
    control.expected_pool_revision,
    control.expected_pool_revision_id,
    control.expected_history_revision,
    control.expected_history_revision_id,
    control.expected_artifact_revision,
    control.expected_artifact_revision_id,
    control.expected_reservation_revision,
    control.expected_reservation_revision_id,
    control.expected_category_revision,
    control.expected_category_revision_id,
    control.expected_snapshot_id,
    attempt.id AS attempt_id,
    attempt.slot_id AS attempt_slot_id,
    attempt.attempt_number AS attempt_number,
    attempt.state AS attempt_state,
    attempt.result_reason AS attempt_result_reason,
    attempt.winner_id AS attempt_winner_id,
    attempt.result_revision_id AS attempt_result_revision_id
FROM (
    SELECT * FROM replay_controls
    UNION ALL
    SELECT * FROM exhausted_controls
) AS control
JOIN game_attempts AS attempt
    ON attempt.slot_id = control.slot_id
    AND attempt.series_id = control.series_id
ORDER BY control.control_kind,
    control.series_id,
    control.slot_id,
    attempt.attempt_number,
    attempt.id;

-- Candidate eligibility is read from the same normalized authority sources as
-- the reserve mutation. The client mutation generates a fresh proposed_snapshot_id,
-- so this read model exposes only server-validated task/version candidates.
-- name: ListTournamentAdminRecoveryReserveCandidates :many
WITH exhausted AS (
    SELECT exhaustion.command_id,
        exhaustion.assignment_id,
        exhaustion.tournament_id,
        exhaustion.roster_id,
        exhaustion.series_id,
        authority.required_category,
        assignment.plan_id,
        assignment.branch_id,
        series.first_participant_id,
        series.second_participant_id
    FROM replay_reserve_exhaustions AS exhaustion
    JOIN series
        ON series.id = exhaustion.series_id
        AND series.tournament_id = exhaustion.tournament_id
        AND series.roster_id = exhaustion.roster_id
        AND series.state = 'technical_pause'
        AND series.revision = exhaustion.resulting_series_revision
    JOIN assignments AS assignment
        ON assignment.id = exhaustion.assignment_id
        AND assignment.attempt_id = exhaustion.assignment_attempt_id
        AND assignment.state = 'active'
        AND assignment.snapshot_id = exhaustion.active_snapshot_id
    JOIN assignment_plans AS plan
        ON plan.id = assignment.plan_id
    JOIN replay_reserve_authorities AS authority
        ON authority.assignment_id = exhaustion.assignment_id
        AND authority.tournament_id = exhaustion.tournament_id
        AND authority.roster_id = exhaustion.roster_id
        AND authority.series_id = exhaustion.series_id
        AND authority.slot_id = exhaustion.slot_id
        AND authority.assignment_attempt_id = exhaustion.assignment_attempt_id
        AND authority.active_snapshot_id = exhaustion.active_snapshot_id
        AND authority.assignment_revision = assignment.revision
        AND authority.required_category = exhaustion.category
    WHERE exhaustion.tournament_id = sqlc.arg(tournament_id)
        AND exhaustion.reserve_position = COALESCE(plan.reserve_count::integer, 2) + 1
), eligible AS (
    SELECT DISTINCT
        exhausted.command_id AS exhaustion_command_id,
        pool.task_id,
        pool.task_version
    FROM exhausted
    JOIN replay_reserve_authority_pool_versions AS pool
        ON pool.assignment_id = exhausted.assignment_id
    JOIN task_versions AS candidate_version
        ON candidate_version.task_id = pool.task_id
        AND candidate_version.version = pool.task_version
        AND candidate_version.category = exhausted.required_category
    JOIN tasks AS candidate_task
        ON candidate_task.id = candidate_version.task_id
        AND candidate_task.enabled
        AND candidate_task.deleted_at IS NULL
    LEFT JOIN LATERAL (
        SELECT attestation.healthy
        FROM task_version_health_attestations AS attestation
        WHERE attestation.task_id = candidate_version.task_id
            AND attestation.task_version = candidate_version.version
        ORDER BY attestation.revision DESC
        LIMIT 1
    ) AS health ON TRUE
    WHERE COALESCE(health.healthy, false)
        AND NOT EXISTS (
            SELECT 1
            FROM task_public_exposures AS exposure
            WHERE exposure.task_id = pool.task_id
                AND exposure.task_version = pool.task_version
        )
        AND NOT EXISTS (
            SELECT 1
            FROM task_delivery_receipts AS receipt
            JOIN assignments AS receipt_assignment
                ON receipt_assignment.id = receipt.assignment_id
            JOIN series AS receipt_series
                ON receipt_series.id = receipt_assignment.series_id
                AND receipt_series.roster_id = receipt_assignment.roster_id
            WHERE receipt.task_id = pool.task_id
                AND receipt.task_version = pool.task_version
                AND receipt_series.tournament_id = exhausted.tournament_id
                AND receipt_series.roster_id = exhausted.roster_id
                AND receipt.participant_id IN (
                    exhausted.first_participant_id,
                    exhausted.second_participant_id
                )
        )
        AND NOT EXISTS (
            SELECT 1
            FROM task_version_reservations AS used_reservation
            WHERE used_reservation.tournament_id = exhausted.tournament_id
                AND used_reservation.plan_id = exhausted.plan_id
                AND used_reservation.branch_id = exhausted.branch_id
                AND used_reservation.task_id = pool.task_id
                AND used_reservation.task_version = pool.task_version
                AND used_reservation.state = 'committed'
        )
)
SELECT exhaustion_command_id,
    task_id,
    task_version
FROM eligible
ORDER BY exhaustion_command_id,
    task_id,
    task_version;
