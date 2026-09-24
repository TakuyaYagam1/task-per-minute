-- Paused participant presence is a child mutation of one active normal
-- operator Wave pause.  The root and command receipt are locked before the
-- existing normal-pause graph loader reads its immutable evidence.

-- name: LockTournamentPausedPresenceRoot :many
SELECT pause.id AS pause_id,
    pause.tournament_id,
    pause.roster_id,
    pause.scope_kind,
    pause.scope_id,
    pause.wave_id,
    pause.reason,
    pause.state,
    pause.current_revision_id,
    pause.revision AS pause_revision,
    pause.started_at,
    command.command_id,
    command.result_document
FROM pauses AS pause
JOIN wave_control_commands AS command
    ON command.tournament_id = pause.tournament_id
    AND command.roster_id = pause.roster_id
    AND command.wave_id = pause.wave_id
    AND command.action = 'pause'
    AND command.executed_at = pause.started_at
WHERE pause.tournament_id = sqlc.arg(tournament_id)
    AND pause.roster_id = sqlc.arg(roster_id)
    AND pause.scope_kind = 'wave'
    AND pause.scope_id = sqlc.arg(wave_id)
    AND pause.wave_id = sqlc.arg(wave_id)
    AND pause.parent_pause_id IS NULL
    AND pause.reason = 'operator'
    AND pause.state = 'active'
ORDER BY pause.started_at DESC, pause.id DESC, command.command_id DESC
FOR UPDATE OF pause, command;

-- Resolve exactly one current normal assignment and its selected participant
-- Presence.  A participant in multiple matching Series is an ambiguity, not
-- a reason to pick one row.
-- name: LockTournamentPausedPresenceParticipant :many
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
FROM pauses AS pause
JOIN wave_series AS membership
    ON membership.wave_id = pause.wave_id
    AND membership.tournament_id = pause.tournament_id
    AND membership.roster_id = pause.roster_id
JOIN series
    ON series.id = membership.series_id
    AND series.tournament_id = membership.tournament_id
    AND series.roster_id = membership.roster_id
JOIN game_attempts AS attempt
    ON attempt.series_id = series.id
    AND attempt.roster_id = series.roster_id
JOIN assignments AS assignment
    ON assignment.attempt_id = attempt.id
    AND assignment.series_id = series.id
    AND assignment.roster_id = series.roster_id
    AND assignment.state = 'active'
JOIN task_snapshots AS snapshot
    ON snapshot.id = assignment.snapshot_id
    AND snapshot.reservation_id = assignment.reservation_id
    AND snapshot.task_id = assignment.task_id
    AND snapshot.task_version = assignment.task_version
    AND snapshot.kind = 'normal'
JOIN task_delivery_receipts AS receipt
    ON receipt.assignment_id = assignment.id
    AND receipt.attempt_id = assignment.attempt_id
    AND receipt.roster_id = assignment.roster_id
    AND receipt.participant_id = sqlc.arg(participant_id)
    AND receipt.snapshot_id = assignment.snapshot_id
JOIN presence_states AS presence
    ON presence.tournament_id = series.tournament_id
    AND presence.roster_id = series.roster_id
    AND presence.series_id = series.id
    AND presence.participant_id = sqlc.arg(participant_id)
WHERE pause.tournament_id = sqlc.arg(tournament_id)
    AND pause.roster_id = sqlc.arg(roster_id)
    AND pause.scope_kind = 'wave'
    AND pause.scope_id = sqlc.arg(wave_id)
    AND pause.wave_id = sqlc.arg(wave_id)
    AND pause.parent_pause_id IS NULL
    AND pause.reason = 'operator'
    AND pause.state = 'active'
    AND (series.state = 'technical_pause' OR (
        series.state = 'active'
        AND EXISTS (
            SELECT 1 FROM pauses AS source_pause
            WHERE source_pause.tournament_id = series.tournament_id
                AND source_pause.roster_id = series.roster_id
                AND source_pause.series_id = series.id
                AND source_pause.game_attempt_id = attempt.id
                AND source_pause.scope_kind = 'game_attempt'
                AND source_pause.scope_id = attempt.id
                AND source_pause.parent_pause_id IS NULL
                AND source_pause.depth = 0
                AND source_pause.reason = 'disconnect'
                AND source_pause.state = 'active'
        )
    ))
    AND attempt.state = 'paused'
ORDER BY presence.series_id, presence.participant_id, presence.id
FOR UPDATE OF pause, membership, series, attempt, assignment, snapshot, receipt, presence;

-- The usecase has already locked and validated the immutable pause graph. This
-- statement performs the selected Presence-only CAS and rechecks the active
-- operator Wave pause identity in the same caller transaction.
-- name: UpdateTournamentPausedPresenceCAS :one
UPDATE presence_states AS presence
SET state = sqlc.arg(next_state),
    presence_epoch = sqlc.arg(next_presence_epoch),
    revision = sqlc.arg(next_presence_revision),
    connected_at = sqlc.arg(connected_at),
    disconnected_at = sqlc.narg(disconnected_at)::TIMESTAMPTZ,
    updated_at = sqlc.arg(updated_at)
FROM pauses AS pause
WHERE pause.id = sqlc.arg(pause_id)
    AND pause.tournament_id = sqlc.arg(tournament_id)
    AND pause.roster_id = sqlc.arg(roster_id)
    AND pause.scope_kind = 'wave'
    AND pause.scope_id = sqlc.arg(wave_id)
    AND pause.wave_id = sqlc.arg(wave_id)
    AND pause.parent_pause_id IS NULL
    AND pause.reason = 'operator'
    AND pause.state = 'active'
    AND pause.revision = sqlc.arg(expected_pause_revision)
    AND presence.id = sqlc.arg(presence_id)
    AND presence.tournament_id = sqlc.arg(tournament_id)
    AND presence.roster_id = sqlc.arg(roster_id)
    AND presence.series_id = sqlc.arg(series_id)
    AND presence.participant_id = sqlc.arg(participant_id)
    AND presence.presence_epoch = sqlc.arg(expected_presence_epoch)
    AND presence.revision = sqlc.arg(expected_presence_revision)
    AND presence.state <> sqlc.arg(next_state)
RETURNING presence.id,
    presence.tournament_id,
    presence.roster_id,
    presence.series_id,
    presence.participant_id,
    presence.state,
    presence.presence_epoch,
    presence.revision,
    presence.connected_at,
    presence.disconnected_at,
    presence.updated_at;
