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
ORDER BY receipt.delivered_at DESC,
    receipt.id DESC
LIMIT 1;

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
    AND series.state NOT IN ('completed', 'cancelled')
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
    bracket.payload AS bracket_payload
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
WHERE revision.tournament_id = sqlc.arg(tournament_id)
    AND revision.state = 'published';

-- name: ListTournamentReadParticipants :many
SELECT participant.id AS participant_id,
    player.username AS display_name
FROM participants AS participant
JOIN rosters AS roster ON roster.id = participant.roster_id
JOIN players AS player ON player.id = participant.player_id
WHERE roster.tournament_id = sqlc.arg(tournament_id)
ORDER BY participant.seed,
    participant.id;

-- name: ListPublicTournamentReadSeries :many
SELECT series.id AS series_id,
    series.format,
    series.state,
    first_player.username AS first_display_name,
    second_player.username AS second_display_name,
    series.first_participant_wins,
    series.second_participant_wins,
    COALESCE(current_slot.slot_number, 0)::INTEGER AS current_game_position
FROM series
JOIN participants AS first_participant
    ON first_participant.id = series.first_participant_id
JOIN players AS first_player ON first_player.id = first_participant.player_id
JOIN participants AS second_participant
    ON second_participant.id = series.second_participant_id
JOIN players AS second_player ON second_player.id = second_participant.player_id
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
    AND series.state NOT IN ('completed', 'cancelled')
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
