-- name: GetRecoveryDeadlineReceipt :one
SELECT id,
    command_id,
    transition_kind,
    deadline_id,
    expected_deadline_revision,
    expected_authority_revision,
    tournament_id,
    roster_id,
    wave_id,
    series_id,
    game_attempt_id,
    pause_id,
    participant_id,
    ready_window_id,
    result_commit_id,
    normal_no_show_commit_ids,
    route_evidence_id,
    resolved_at,
    created_at
FROM deadline_transition_receipts
WHERE deadline_id = sqlc.arg(deadline_id)
    AND expected_deadline_revision = sqlc.arg(expected_deadline_revision)
    AND (
        (sqlc.arg(deadline_kind)::TEXT = 'game' AND transition_kind = 'game_timeout_replay')
        OR (sqlc.arg(deadline_kind)::TEXT = 'ready_window' AND transition_kind = 'ready_window_no_show')
        OR (sqlc.arg(deadline_kind)::TEXT = 'reconnect' AND transition_kind IN ('reconnect_interval_expired', 'reconnect_forfeit', 'reconnect_replay'))
    );

-- name: LockRecoveryGameTimeout :one
SELECT sqlc.embed(game_attempt),
    sqlc.embed(game_slot),
    sqlc.embed(assignment),
    sqlc.embed(series),
    sqlc.embed(wave),
    sqlc.embed(score_head)
FROM game_attempts AS game_attempt
JOIN game_slots AS game_slot
    ON game_slot.id = game_attempt.slot_id
    AND game_slot.series_id = game_attempt.series_id
    AND game_slot.roster_id = game_attempt.roster_id
JOIN assignments AS assignment
    ON assignment.attempt_id = game_attempt.id
    AND assignment.series_id = game_attempt.series_id
    AND assignment.roster_id = game_attempt.roster_id
    AND assignment.state = 'active'
JOIN task_snapshots AS task_snapshot
    ON task_snapshot.id = assignment.snapshot_id
JOIN series
    ON series.id = game_attempt.series_id
    AND series.roster_id = game_attempt.roster_id
JOIN wave_series
    ON wave_series.series_id = series.id
    AND wave_series.wave_id = sqlc.arg(wave_id)
JOIN waves AS wave
    ON wave.id = wave_series.wave_id
    AND wave.tournament_id = series.tournament_id
    AND wave.roster_id = series.roster_id
JOIN series_score_heads AS score_head
    ON score_head.series_id = series.id
    AND score_head.roster_id = series.roster_id
LEFT JOIN LATERAL (
    SELECT pause_clock.resumed_at + (pause_clock.original_deadline - pause_clock.frozen_at) AS exact_resumed_deadline
    FROM pause_clocks AS pause_clock
    JOIN pauses AS clock_pause
        ON clock_pause.id = pause_clock.pause_id
        AND clock_pause.state = 'resumed'
    WHERE pause_clock.game_attempt_id = game_attempt.id
        AND pause_clock.resumed_at IS NOT NULL
        AND pause_clock.resumed_deadline IS NOT NULL
    ORDER BY pause_clock.resumed_at DESC, pause_clock.pause_id DESC
    LIMIT 1
) AS latest_clock ON TRUE
WHERE game_attempt.id = sqlc.arg(game_attempt_id)
    AND game_attempt.roster_id = sqlc.arg(roster_id)
    AND series.tournament_id = sqlc.arg(tournament_id)
    AND game_attempt.revision = sqlc.arg(expected_revision)
    AND game_attempt.state = 'active'
    AND wave.state = 'active'
    AND COALESCE(
        latest_clock.exact_resumed_deadline,
        game_attempt.started_at + INTERVAL '180 seconds'
    ) = sqlc.arg(due_at)::TIMESTAMPTZ
FOR UPDATE OF game_attempt, game_slot, assignment, series, wave, score_head;

-- name: LockRecoveryReadyWindow :one
SELECT sqlc.embed(ready_window),
    sqlc.embed(wave)
FROM ready_windows AS ready_window
JOIN waves AS wave
    ON wave.id = ready_window.wave_id
    AND wave.roster_id = ready_window.roster_id
WHERE ready_window.id = sqlc.arg(ready_window_id)
    AND ready_window.revision_id = sqlc.arg(ready_window_revision_id)
    AND ready_window.deadline = sqlc.arg(due_at)::TIMESTAMPTZ
    AND ready_window.state = 'open'
    AND wave.id = sqlc.arg(wave_id)
    AND wave.tournament_id = sqlc.arg(tournament_id)
    AND wave.roster_id = sqlc.arg(roster_id)
    AND wave.revision = sqlc.arg(expected_revision)
    AND wave.state = 'ready_window_open'
FOR UPDATE OF ready_window, wave;

-- name: ListRecoveryReadyWindowSeries :many
SELECT sqlc.embed(series),
    sqlc.embed(score_head)
FROM wave_series
JOIN series
    ON series.id = wave_series.series_id
    AND series.tournament_id = wave_series.tournament_id
    AND series.roster_id = wave_series.roster_id
JOIN series_score_heads AS score_head
    ON score_head.series_id = series.id
    AND score_head.roster_id = series.roster_id
WHERE wave_series.wave_id = sqlc.arg(wave_id)
    AND series.state IN ('ready', 'active', 'replay_required')
    AND EXISTS (
        SELECT 1
        FROM wave_readiness AS readiness
        WHERE readiness.wave_id = wave_series.wave_id
            AND readiness.ready_window_id = sqlc.arg(ready_window_id)
            AND readiness.participant_id IN (
                series.first_participant_id,
                series.second_participant_id
            )
            AND NOT readiness.ready
    )
ORDER BY series.id
FOR UPDATE OF series, score_head;

-- name: ListRecoverySeriesGraph :many
SELECT sqlc.embed(game_slot),
    sqlc.embed(game_attempt)
FROM game_slots AS game_slot
JOIN game_attempts AS game_attempt
    ON game_attempt.slot_id = game_slot.id
    AND game_attempt.series_id = game_slot.series_id
    AND game_attempt.roster_id = game_slot.roster_id
WHERE game_slot.series_id = sqlc.arg(series_id)
    AND game_slot.roster_id = sqlc.arg(roster_id)
ORDER BY game_slot.slot_number, game_attempt.attempt_number
FOR UPDATE OF game_slot, game_attempt;

-- name: ListRecoveryGameResultRevisionIDs :many
SELECT result_head.current_revision_id
FROM official_result_heads AS result_head
JOIN game_attempts AS game_attempt
    ON game_attempt.id = result_head.entity_id
    AND result_head.entity_kind = 'game_attempt'
WHERE game_attempt.series_id = sqlc.arg(series_id)
    AND game_attempt.roster_id = sqlc.arg(roster_id)
ORDER BY game_attempt.finished_at, game_attempt.id;

-- name: LockRecoveryReconnectTimeout :one
SELECT sqlc.embed(reconnect_interval),
    sqlc.embed(pause),
    sqlc.embed(pause_clock),
    sqlc.embed(game_attempt),
    sqlc.embed(series),
    sqlc.embed(score_head)
FROM reconnect_intervals AS reconnect_interval
JOIN pauses AS pause
    ON pause.id = reconnect_interval.pause_id
    AND pause.series_id = reconnect_interval.series_id
    AND pause.game_attempt_id = reconnect_interval.game_attempt_id
    AND pause.roster_id = reconnect_interval.roster_id
JOIN pause_clocks AS pause_clock
    ON pause_clock.pause_id = pause.id
    AND pause_clock.game_attempt_id = reconnect_interval.game_attempt_id
JOIN game_attempts AS game_attempt
    ON game_attempt.id = reconnect_interval.game_attempt_id
    AND game_attempt.series_id = reconnect_interval.series_id
    AND game_attempt.roster_id = reconnect_interval.roster_id
JOIN series
    ON series.id = reconnect_interval.series_id
    AND series.roster_id = reconnect_interval.roster_id
JOIN wave_series
    ON wave_series.series_id = series.id
    AND wave_series.wave_id = sqlc.arg(wave_id)
JOIN waves AS wave
    ON wave.id = wave_series.wave_id
    AND wave.tournament_id = series.tournament_id
    AND wave.roster_id = series.roster_id
JOIN series_score_heads AS score_head
    ON score_head.series_id = series.id
    AND score_head.roster_id = series.roster_id
WHERE reconnect_interval.id = sqlc.arg(reconnect_interval_id)
    AND reconnect_interval.participant_id = sqlc.arg(participant_id)
    AND reconnect_interval.revision = sqlc.arg(expected_revision)
    AND reconnect_interval.deadline_at = sqlc.arg(due_at)::TIMESTAMPTZ
    AND reconnect_interval.state = 'open'
    AND pause.id = sqlc.arg(pause_id)
    AND pause.tournament_id = sqlc.arg(tournament_id)
    AND pause.roster_id = sqlc.arg(roster_id)
    AND pause.state = 'active'
    AND pause.reason = 'disconnect'
    AND game_attempt.state = 'paused'
    AND series.state = 'active'
    AND wave.state = 'active'
FOR UPDATE OF reconnect_interval, pause, pause_clock, game_attempt, series, score_head;

-- name: ListRecoveryPresence :many
SELECT id,
    tournament_id,
    roster_id,
    series_id,
    participant_id,
    state,
    presence_epoch,
    revision,
    connected_at,
    disconnected_at,
    updated_at
FROM presence_states
WHERE series_id = sqlc.arg(series_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY participant_id
FOR UPDATE;

-- name: ListRecoveryReconnectIntervals :many
SELECT id,
    pause_id,
    roster_id,
    series_id,
    game_attempt_id,
    participant_id,
    presence_epoch,
    interval_number,
    state,
    opened_at,
    deadline_at,
    closed_at,
    revision,
    created_at,
    updated_at,
    continuation_number,
    continued_from_id,
    suspended_by_pause_id
FROM reconnect_intervals
WHERE pause_id = sqlc.arg(pause_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY participant_id, interval_number, continuation_number, id
FOR UPDATE;

-- name: ListRecoveryReconnectCounters :many
SELECT pause_id,
    roster_id,
    participant_id,
    slot_limit,
    slots_used,
    revision,
    created_at,
    updated_at
FROM reconnect_slot_counters
WHERE pause_id = sqlc.arg(pause_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY participant_id
FOR UPDATE;

-- name: CreateRecoveryWaveMemberRoute :one
INSERT INTO wave_member_routes (
    id,
    tournament_id,
    roster_id,
    wave_id,
    series_id,
    slot_id,
    game_attempt_id,
    category,
    routed_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(wave_id),
    sqlc.arg(series_id),
    sqlc.arg(slot_id),
    sqlc.arg(game_attempt_id),
    sqlc.arg(category),
    sqlc.arg(routed_at),
    sqlc.arg(routed_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    wave_id,
    series_id,
    slot_id,
    game_attempt_id,
    category,
    routed_at,
    created_at;

-- name: ExpireRecoveryReadyWindowCAS :one
UPDATE ready_windows
SET state = 'expired'
WHERE id = sqlc.arg(ready_window_id)
    AND wave_id = sqlc.arg(wave_id)
    AND revision_id = sqlc.arg(expected_revision_id)
    AND state = 'open'
    AND deadline = sqlc.arg(deadline)::TIMESTAMPTZ
RETURNING id,
    wave_id,
    roster_id,
    revision_id,
    state,
    opened_at,
    deadline,
    consumed_at,
    created_at;

-- name: ExpireRecoveryWaveCAS :one
UPDATE waves
SET state = 'ready_window_expired',
    revision = revision + 1,
    updated_at = sqlc.arg(expired_at)
WHERE id = sqlc.arg(wave_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND revision = sqlc.arg(expected_revision)
    AND state = 'ready_window_open'
RETURNING id,
    tournament_id,
    roster_id,
    revision_id,
    revision,
    state,
    replaces_wave_id,
    created_at,
    updated_at,
    started_at,
    paused_at,
    closed_at;

-- name: CreateRecoveryNormalNoShowCommit :one
INSERT INTO normal_no_show_commits (
    id,
    tournament_id,
    roster_id,
    wave_id,
    ready_window_id,
    ready_window_revision_id,
    series_id,
    result_event_id,
    series_score_revision_id,
    series_result_revision_id,
    audit_event_id,
    outbox_event_id,
    projection_evidence_id,
    command_id,
    expected_authority_revision,
    expected_wave_revision,
    action,
    resolved_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(wave_id),
    sqlc.arg(ready_window_id),
    sqlc.arg(ready_window_revision_id),
    sqlc.arg(series_id),
    sqlc.arg(result_event_id),
    sqlc.arg(series_score_revision_id),
    sqlc.arg(series_result_revision_id),
    sqlc.arg(audit_event_id),
    sqlc.arg(outbox_event_id),
    sqlc.arg(projection_evidence_id),
    sqlc.arg(command_id),
    sqlc.arg(expected_authority_revision),
    sqlc.arg(expected_wave_revision),
    sqlc.arg(action),
    sqlc.arg(resolved_at),
    sqlc.arg(resolved_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    wave_id,
    ready_window_id,
    ready_window_revision_id,
    series_id,
    result_event_id,
    series_score_revision_id,
    series_result_revision_id,
    audit_event_id,
    outbox_event_id,
    projection_evidence_id,
    command_id,
    expected_authority_revision,
    expected_wave_revision,
    action,
    resolved_at,
    created_at;

-- name: CreateRecoveryNormalNoShowGame :exec
INSERT INTO normal_no_show_commit_games (
    commit_id,
    game_attempt_id,
    game_result_revision_id,
    position
)
VALUES (
    sqlc.arg(commit_id),
    sqlc.arg(game_attempt_id),
    sqlc.arg(game_result_revision_id),
    sqlc.arg(position)
);

-- name: ExpireRecoveryReconnectIntervalCAS :one
UPDATE reconnect_intervals
SET state = 'expired',
    closed_at = sqlc.arg(expired_at),
    revision = revision + 1,
    updated_at = sqlc.arg(expired_at)
WHERE id = sqlc.arg(reconnect_interval_id)
    AND pause_id = sqlc.arg(pause_id)
    AND participant_id = sqlc.arg(participant_id)
    AND revision = sqlc.arg(expected_revision)
    AND state = 'open'
    AND deadline_at <= sqlc.arg(expired_at)::TIMESTAMPTZ
RETURNING id,
    pause_id,
    roster_id,
    series_id,
    game_attempt_id,
    participant_id,
    presence_epoch,
    interval_number,
    state,
    opened_at,
    deadline_at,
    closed_at,
    revision,
    created_at,
    updated_at,
    continuation_number,
    continued_from_id,
    suspended_by_pause_id;

-- name: CreateRecoveryPauseRevision :one
INSERT INTO pause_revisions (
    id,
    pause_id,
    previous_revision_id,
    revision_number,
    state,
    transition_reason,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(pause_id),
    sqlc.arg(previous_revision_id),
    sqlc.arg(revision_number),
    'cancelled',
    'terminal_reconnect_timeout',
    sqlc.arg(created_at)
)
RETURNING id,
    pause_id,
    previous_revision_id,
    revision_number,
    state,
    transition_reason,
    created_at;

-- name: CancelRecoveryPauseCAS :one
UPDATE pauses
SET state = 'cancelled',
    current_revision_id = sqlc.arg(current_revision_id),
    revision = revision + 1,
    resolved_at = sqlc.arg(resolved_at),
    updated_at = sqlc.arg(resolved_at)
WHERE id = sqlc.arg(pause_id)
    AND current_revision_id = sqlc.arg(expected_revision_id)
    AND revision = sqlc.arg(expected_revision)
    AND state = 'active'
RETURNING id,
    tournament_id,
    roster_id,
    scope_kind,
    scope_id,
    wave_id,
    series_id,
    game_attempt_id,
    parent_pause_id,
    depth,
    reason,
    paused_from_state,
    state,
    current_revision_id,
    revision,
    started_at,
    resolved_at,
    created_at,
    updated_at;

-- name: CreateRecoveryDeadlineReceipt :one
INSERT INTO deadline_transition_receipts (
    id,
    command_id,
    transition_kind,
    deadline_id,
    expected_deadline_revision,
    expected_authority_revision,
    tournament_id,
    roster_id,
    wave_id,
    series_id,
    game_attempt_id,
    pause_id,
    participant_id,
    ready_window_id,
    result_commit_id,
    normal_no_show_commit_ids,
    route_evidence_id,
    resolved_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(command_id),
    sqlc.arg(transition_kind),
    sqlc.arg(deadline_id),
    sqlc.arg(expected_deadline_revision),
    sqlc.arg(expected_authority_revision),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(wave_id),
    sqlc.arg(series_id),
    sqlc.arg(game_attempt_id),
    sqlc.arg(pause_id),
    sqlc.arg(participant_id),
    sqlc.arg(ready_window_id),
    sqlc.arg(result_commit_id),
    sqlc.arg(normal_no_show_commit_ids),
    sqlc.arg(route_evidence_id),
    sqlc.arg(resolved_at),
    sqlc.arg(resolved_at)
)
RETURNING id,
    command_id,
    transition_kind,
    deadline_id,
    expected_deadline_revision,
    expected_authority_revision,
    tournament_id,
    roster_id,
    wave_id,
    series_id,
    game_attempt_id,
    pause_id,
    participant_id,
    ready_window_id,
    result_commit_id,
    normal_no_show_commit_ids,
    route_evidence_id,
    resolved_at,
    created_at;
