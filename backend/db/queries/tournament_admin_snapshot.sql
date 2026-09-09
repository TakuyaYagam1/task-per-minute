-- name: GetTournamentAdminSnapshotHeader :one
SELECT tournament.id,
    roster.id AS roster_id,
    tournament.preset,
    tournament.state,
    tournament.paused_from_state,
    tournament.revision AS tournament_revision,
    tournament.created_at AS tournament_created_at,
    tournament.updated_at AS tournament_updated_at,
    tournament.started_at AS tournament_started_at,
    tournament.finished_at AS tournament_finished_at,
    COALESCE(roster_size.participant_count, 0)::INTEGER AS roster_size,
    COALESCE(projection.revision_number, 0)::BIGINT AS projection_revision,
    COALESCE(authority.revision, tournament.revision)::BIGINT AS authority_revision,
    COALESCE(audit.event_count, 0)::BIGINT AS audit_sequence,
    COALESCE(authority.holder_id, '00000000-0000-0000-0000-000000000000'::UUID) AS authority_holder_id,
    COALESCE(authority.lease_id, '00000000-0000-0000-0000-000000000000'::UUID) AS authority_lease_id,
    COALESCE(authority.epoch, 0)::BIGINT AS authority_epoch,
    COALESCE(authority.process_kind, '')::TEXT AS authority_process_kind
FROM tournaments AS tournament
LEFT JOIN rosters AS roster ON roster.tournament_id = tournament.id
LEFT JOIN LATERAL (
    SELECT COUNT(*)::INTEGER AS participant_count
    FROM participants AS participant
    WHERE participant.roster_id = roster.id
) AS roster_size ON TRUE
LEFT JOIN LATERAL (
    SELECT revision.revision_number
    FROM projection_revisions AS revision
    WHERE revision.tournament_id = tournament.id
        AND revision.roster_id = roster.id
        AND revision.state = 'published'
    ORDER BY revision.revision_number DESC, revision.id DESC
    LIMIT 1
) AS projection ON TRUE
LEFT JOIN LATERAL (
    SELECT lease.holder_id,
        lease.lease_id,
        lease.epoch,
        lease.process_kind,
        lease.revision
    FROM execution_authority_leases AS lease
    WHERE lease.tournament_id = tournament.id
    ORDER BY lease.revision DESC, lease.created_at DESC, lease.command_id DESC
    LIMIT 1
) AS authority ON TRUE
LEFT JOIN LATERAL (
    SELECT COUNT(*)::BIGINT AS event_count
    FROM audit_events AS event
    WHERE event.tournament_id = tournament.id
) AS audit ON TRUE
WHERE tournament.id = sqlc.arg(tournament_id);

-- name: ListTournamentAdminSnapshotWaves :many
SELECT wave.id,
    wave.tournament_id,
    wave.revision_id,
    wave.revision,
    wave.state,
    wave.created_at,
    wave.updated_at,
    wave.started_at,
    wave.paused_at,
    wave.closed_at,
    ready_window.id AS ready_window_id,
    ready_window.revision_id AS ready_window_revision_id,
    ready_window.state AS ready_window_state,
    ready_window.opened_at AS ready_window_opened_at,
    ready_window.deadline AS ready_window_deadline,
    ready_window.consumed_at AS ready_window_consumed_at,
    link.bye_participant_id
FROM waves AS wave
LEFT JOIN ready_windows AS ready_window ON ready_window.wave_id = wave.id
LEFT JOIN swiss_wave_links AS link ON link.wave_id = wave.id
WHERE wave.tournament_id = sqlc.arg(tournament_id)
    AND wave.roster_id = sqlc.arg(roster_id)
ORDER BY wave.created_at, wave.id;

-- name: ListTournamentAdminSnapshotWaveMembers :many
SELECT member.wave_id,
    member.participant_id,
    readiness.ready,
    readiness.revision AS readiness_revision,
    series.id AS series_id
FROM wave_members AS member
JOIN waves AS wave
    ON wave.id = member.wave_id
    AND wave.roster_id = member.roster_id
LEFT JOIN wave_readiness AS readiness
    ON readiness.wave_id = member.wave_id
    AND readiness.roster_id = member.roster_id
    AND readiness.participant_id = member.participant_id
LEFT JOIN LATERAL (
    SELECT series.id
    FROM wave_series AS membership
    JOIN series
        ON series.id = membership.series_id
        AND series.tournament_id = membership.tournament_id
        AND series.roster_id = membership.roster_id
    WHERE membership.wave_id = member.wave_id
        AND membership.roster_id = member.roster_id
        AND member.participant_id IN (
            series.first_participant_id,
            series.second_participant_id
        )
    ORDER BY series.id
) AS series ON TRUE
WHERE wave.tournament_id = sqlc.arg(tournament_id)
    AND member.roster_id = sqlc.arg(roster_id)
ORDER BY member.wave_id, member.participant_id, series.id;

-- name: ListTournamentAdminSnapshotSeries :many
SELECT id,
    tournament_id,
    roster_id,
    first_participant_id,
    second_participant_id,
    format,
    state,
    first_participant_wins,
    second_participant_wins,
    winner_id,
    current_score_revision_id,
    current_result_revision_id,
    revision,
    created_at,
    updated_at,
    started_at,
    finished_at
FROM series
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY created_at, id;

-- name: ListTournamentAdminSnapshotGameSlots :many
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
JOIN series ON series.id = slot.series_id
    AND series.roster_id = slot.roster_id
WHERE series.tournament_id = sqlc.arg(tournament_id)
    AND slot.roster_id = sqlc.arg(roster_id)
ORDER BY slot.series_id, slot.slot_number, slot.id;

-- name: ListTournamentAdminSnapshotGameAttempts :many
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
JOIN series ON series.id = attempt.series_id
    AND series.roster_id = attempt.roster_id
WHERE series.tournament_id = sqlc.arg(tournament_id)
    AND attempt.roster_id = sqlc.arg(roster_id)
ORDER BY attempt.series_id, attempt.slot_id, attempt.attempt_number, attempt.id;

-- name: ListTournamentAdminSnapshotPauses :many
SELECT pause.id,
    pause.tournament_id,
    pause.roster_id,
    pause.scope_kind,
    pause.scope_id,
    pause.wave_id,
    pause.series_id,
    pause.game_attempt_id,
    pause.parent_pause_id,
    pause.depth,
    pause.reason,
    pause.paused_from_state,
    pause.state,
    pause.current_revision_id,
    pause.revision,
    pause.started_at,
    pause.resolved_at,
    pause.created_at,
    pause.updated_at
FROM pauses AS pause
WHERE pause.tournament_id = sqlc.arg(tournament_id)
    AND pause.roster_id = sqlc.arg(roster_id)
ORDER BY pause.depth, pause.started_at, pause.id;

-- name: ListTournamentAdminSnapshotPresence :many
SELECT presence.id,
    presence.tournament_id,
    presence.roster_id,
    presence.series_id,
    presence.participant_id,
    presence.state,
    presence.presence_epoch,
    presence.revision,
    presence.connected_at,
    presence.disconnected_at,
    presence.updated_at
FROM presence_states AS presence
JOIN pause_presence_snapshots AS snapshot
    ON snapshot.roster_id = presence.roster_id
    AND snapshot.series_id = presence.series_id
    AND snapshot.participant_id = presence.participant_id
    AND snapshot.presence_state = presence.state
    AND snapshot.presence_epoch = presence.presence_epoch
    AND snapshot.presence_revision = presence.revision
JOIN pauses AS pause ON pause.id = snapshot.pause_id
WHERE snapshot.pause_id = sqlc.arg(pause_id)
    AND pause.tournament_id = sqlc.arg(tournament_id)
    AND pause.roster_id = sqlc.arg(roster_id)
    AND snapshot.captured_at = pause.started_at
ORDER BY presence.series_id, presence.participant_id;

-- name: ListTournamentAdminSnapshotReconnectIntervals :many
SELECT reconnect.id,
    reconnect.pause_id,
    reconnect.roster_id,
    reconnect.series_id,
    reconnect.game_attempt_id,
    reconnect.participant_id,
    reconnect.presence_epoch,
    reconnect.interval_number,
    reconnect.state,
    reconnect.opened_at,
    reconnect.deadline_at,
    reconnect.closed_at,
    reconnect.revision,
    reconnect.created_at,
    reconnect.updated_at,
    reconnect.continuation_number,
    reconnect.continued_from_id,
    reconnect.suspended_by_pause_id
FROM reconnect_intervals AS reconnect
JOIN series
    ON series.id = reconnect.series_id
    AND series.roster_id = reconnect.roster_id
WHERE series.tournament_id = sqlc.arg(tournament_id)
    AND reconnect.roster_id = sqlc.arg(roster_id)
ORDER BY reconnect.series_id,
    reconnect.participant_id,
    reconnect.interval_number,
    reconnect.continuation_number,
    reconnect.id;

-- name: ListTournamentAdminSnapshotReconnectCounters :many
SELECT counter.pause_id,
    counter.roster_id,
    counter.participant_id,
    counter.slot_limit,
    counter.slots_used,
    counter.revision,
    counter.created_at,
    counter.updated_at
FROM reconnect_slot_counters AS counter
JOIN pauses AS pause ON pause.id = counter.pause_id
WHERE pause.tournament_id = sqlc.arg(tournament_id)
    AND counter.roster_id = sqlc.arg(roster_id)
ORDER BY counter.pause_id, counter.participant_id;

-- name: ListTournamentAdminSnapshotPauseClocks :many
SELECT clock.pause_id,
    clock.game_attempt_id,
    clock.original_deadline,
    clock.frozen_at,
    clock.frozen_remaining_ms,
    clock.resumed_at,
    clock.resumed_deadline,
    clock.revision,
    clock.created_at,
    clock.updated_at
FROM pause_clocks AS clock
JOIN pauses AS pause ON pause.id = clock.pause_id
WHERE pause.tournament_id = sqlc.arg(tournament_id)
    AND pause.roster_id = sqlc.arg(roster_id)
ORDER BY clock.game_attempt_id, clock.pause_id;

-- name: ListTournamentAdminSnapshotActiveDraftIDs :many
SELECT draft.id
FROM drafts AS draft
JOIN series
    ON series.id = draft.series_id
    AND series.roster_id = draft.roster_id
JOIN LATERAL (
    SELECT revision.state
    FROM draft_revisions AS revision
    WHERE revision.draft_id = draft.id
    ORDER BY revision.revision DESC, revision.id DESC
    LIMIT 1
) AS current_revision ON TRUE
JOIN wave_series AS membership ON membership.series_id = series.id
WHERE membership.wave_id = sqlc.arg(wave_id)
    AND current_revision.state IN ('active', 'paused', 'recovery_required')
ORDER BY draft.created_at, draft.id;
