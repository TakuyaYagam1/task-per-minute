-- The connection lifecycle always locks the participant identity first.  Every
-- subsequent query is scoped by that identity so a socket can never select a
-- different roster, wave, assignment, or game by accident.

-- name: LockParticipantConnectionIdentity :one
SELECT tournament.id AS tournament_id,
    tournament.state AS tournament_state,
    roster.id AS roster_id,
    participant.id AS participant_id,
    participant.player_id AS player_id
FROM tournaments AS tournament
JOIN rosters AS roster
    ON roster.tournament_id = tournament.id
JOIN participants AS participant
    ON participant.roster_id = roster.id
    AND participant.player_id = sqlc.arg(player_id)
WHERE tournament.id = sqlc.arg(tournament_id)
FOR UPDATE OF tournament, roster, participant;

-- A participant can have completed historical waves.  Prefer the one current
-- execution states use, then fall back to the latest immutable wave so Golden
-- and terminal participants still have a durable scope for their lease.
-- name: LockParticipantConnectionWave :one
SELECT wave.id AS wave_id,
    wave.tournament_id,
    wave.roster_id,
    wave.revision_id AS wave_revision_id,
    wave.revision AS wave_revision,
    wave.state AS wave_state,
    wave.started_at AS wave_started_at,
    member.participant_id
FROM waves AS wave
JOIN wave_members AS member
    ON member.wave_id = wave.id
    AND member.roster_id = wave.roster_id
    AND member.participant_id = sqlc.arg(participant_id)
WHERE wave.tournament_id = sqlc.arg(tournament_id)
    AND wave.roster_id = sqlc.arg(roster_id)
    AND wave.state <> 'superseded'
ORDER BY CASE
        WHEN wave.state IN ('planned', 'ready_window_open', 'ready', 'active', 'paused') THEN 0
        ELSE 1
    END,
    wave.updated_at DESC,
    wave.revision DESC,
    wave.id DESC
LIMIT 1
FOR UPDATE OF wave, member;

-- name: LockParticipantConnectionReadyWindow :one
SELECT ready_window.id AS ready_window_id,
    ready_window.wave_id,
    ready_window.roster_id,
    ready_window.revision_id AS ready_window_revision_id,
    ready_window.state AS ready_window_state,
    ready_window.opened_at AS ready_window_opened_at,
    ready_window.deadline AS ready_window_deadline
FROM ready_windows AS ready_window
WHERE ready_window.wave_id = sqlc.arg(wave_id)
    AND ready_window.roster_id = sqlc.arg(roster_id)
ORDER BY ready_window.created_at DESC, ready_window.id DESC
LIMIT 1
FOR UPDATE;

-- name: LockParticipantConnectionReadiness :one
SELECT readiness.wave_id,
    readiness.ready_window_id,
    readiness.roster_id,
    readiness.participant_id,
    readiness.ready,
    readiness.revision AS readiness_revision
FROM wave_readiness AS readiness
JOIN wave_members AS member
    ON member.wave_id = readiness.wave_id
    AND member.roster_id = readiness.roster_id
    AND member.participant_id = readiness.participant_id
WHERE readiness.wave_id = sqlc.arg(wave_id)
    AND readiness.ready_window_id = sqlc.arg(ready_window_id)
    AND readiness.roster_id = sqlc.arg(roster_id)
    AND readiness.participant_id = sqlc.arg(participant_id)
FOR UPDATE OF member, readiness;

-- Locks are ordered by the immutable connection fence.  The participant row
-- is already locked by the identity query, making this lock set the stable
-- serialization point for multi-tab open/close and active-count decisions.
-- name: LockParticipantConnectionLeases :many
SELECT lease.id,
    lease.tournament_id,
    lease.roster_id,
    lease.participant_id,
    lease.player_id,
    lease.connection_id,
    lease.connection_generation,
    lease.assignment_id,
    lease.series_id,
    lease.game_attempt_id,
    lease.state,
    lease.revision,
    lease.connected_at,
    lease.disconnected_at,
    lease.updated_at
FROM participant_connection_leases AS lease
WHERE lease.tournament_id = sqlc.arg(tournament_id)
    AND lease.roster_id = sqlc.arg(roster_id)
    AND lease.participant_id = sqlc.arg(participant_id)
    AND lease.state = 'active'
ORDER BY lease.connection_id, lease.connection_generation, lease.id
FOR UPDATE OF lease;

-- name: FindParticipantConnectionLeaseByFence :one
SELECT lease.id,
    lease.tournament_id,
    lease.roster_id,
    lease.participant_id,
    lease.player_id,
    lease.connection_id,
    lease.connection_generation,
    lease.assignment_id,
    lease.series_id,
    lease.game_attempt_id,
    lease.state,
    lease.revision,
    lease.connected_at,
    lease.disconnected_at,
    lease.updated_at
FROM participant_connection_leases AS lease
WHERE lease.connection_id = sqlc.arg(connection_id)
    AND lease.connection_generation = sqlc.arg(connection_generation)
FOR UPDATE;

-- name: InsertParticipantConnectionLease :one
INSERT INTO participant_connection_leases (
    id,
    tournament_id,
    roster_id,
    participant_id,
    player_id,
    connection_id,
    connection_generation,
    assignment_id,
    series_id,
    game_attempt_id,
    state,
    revision,
    connected_at,
    updated_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(participant_id),
    sqlc.arg(player_id),
    sqlc.arg(connection_id),
    sqlc.arg(connection_generation),
    sqlc.narg(assignment_id)::UUID,
    sqlc.narg(series_id)::UUID,
    sqlc.narg(game_attempt_id)::UUID,
    'active',
    1,
    transaction_timestamp(),
    transaction_timestamp()
)
ON CONFLICT (connection_id, connection_generation) DO NOTHING
RETURNING id,
    tournament_id,
    roster_id,
    participant_id,
    player_id,
    connection_id,
    connection_generation,
    assignment_id,
    series_id,
    game_attempt_id,
    state,
    revision,
    connected_at,
    disconnected_at,
    updated_at;

-- The update is deliberately fenced by the complete server-resolved
-- identity.  A stale generation or wrong participant updates no row.
-- name: CloseParticipantConnectionLease :one
UPDATE participant_connection_leases AS lease
SET state = 'disconnected',
    disconnected_at = GREATEST(transaction_timestamp(), lease.connected_at),
    updated_at = GREATEST(transaction_timestamp(), lease.connected_at),
    revision = lease.revision + 1
WHERE lease.tournament_id = sqlc.arg(tournament_id)
    AND lease.roster_id = sqlc.arg(roster_id)
    AND lease.participant_id = sqlc.arg(participant_id)
    AND lease.player_id = sqlc.arg(player_id)
    AND lease.connection_id = sqlc.arg(connection_id)
    AND lease.connection_generation = sqlc.arg(connection_generation)
    AND lease.state = 'active'
RETURNING lease.id,
    lease.tournament_id,
    lease.roster_id,
    lease.participant_id,
    lease.player_id,
    lease.connection_id,
    lease.connection_generation,
    lease.assignment_id,
    lease.series_id,
    lease.game_attempt_id,
    lease.state,
    lease.revision,
    lease.connected_at,
    lease.disconnected_at,
    lease.updated_at;

-- name: CountParticipantConnectionLeases :one
SELECT COUNT(*)::BIGINT AS active_lease_count
FROM participant_connection_leases AS lease
WHERE lease.tournament_id = sqlc.arg(tournament_id)
    AND lease.roster_id = sqlc.arg(roster_id)
    AND lease.participant_id = sqlc.arg(participant_id)
    AND lease.state = 'active';

-- The operator pause is wave-scoped and joins the exact participant presence
-- row.  Returning more than one row is a repository conflict, never a reason
-- to choose one pause or one series arbitrarily.
-- name: LockParticipantConnectionOperatorPause :many
SELECT pause.id AS pause_id,
    pause.tournament_id,
    pause.roster_id,
    pause.scope_id AS wave_id,
    pause.revision AS pause_revision,
    assignment.id AS assignment_id,
    assignment.attempt_id AS game_attempt_id,
    assignment.series_id,
    presence.id AS presence_id,
    presence.series_id AS presence_series_id,
    presence.participant_id,
    presence.state AS presence_state,
    presence.presence_epoch,
    presence.revision AS presence_revision
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
    ON presence.tournament_id = pause.tournament_id
    AND presence.roster_id = pause.roster_id
    AND presence.series_id = membership.series_id
    AND presence.participant_id = sqlc.arg(participant_id)
WHERE pause.tournament_id = sqlc.arg(tournament_id)
    AND pause.roster_id = sqlc.arg(roster_id)
    AND pause.wave_id = sqlc.arg(wave_id)
    AND pause.scope_kind = 'wave'
    AND pause.reason = 'operator'
    AND pause.state = 'active'
    AND series.state = 'technical_pause'
    AND attempt.state = 'paused'
ORDER BY pause.id, assignment.series_id, assignment.attempt_id, presence.id
FOR UPDATE OF pause, membership, series, attempt, assignment, snapshot, receipt, presence;

-- Only a delivered, normal task in an active game can produce ordinary game
-- disconnect.  All joins are identity-bound and locked in graph order.
-- name: LockParticipantConnectionActiveGame :many
SELECT assignment.id AS assignment_id,
    assignment.attempt_id AS game_attempt_id,
    assignment.series_id,
    series.state AS series_state,
    attempt.state AS game_state,
    snapshot.kind AS task_kind,
    presence.id AS presence_id,
    presence.state AS presence_state,
    presence.presence_epoch,
    presence.revision AS presence_revision
FROM wave_series AS membership
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
JOIN task_delivery_receipts AS receipt
    ON receipt.assignment_id = assignment.id
    AND receipt.attempt_id = assignment.attempt_id
    AND receipt.roster_id = assignment.roster_id
    AND receipt.participant_id = sqlc.arg(participant_id)
    AND receipt.snapshot_id = assignment.snapshot_id
JOIN presence_states AS presence
    ON presence.tournament_id = membership.tournament_id
    AND presence.roster_id = membership.roster_id
    AND presence.series_id = series.id
    AND presence.participant_id = sqlc.arg(participant_id)
WHERE membership.wave_id = sqlc.arg(wave_id)
    AND membership.tournament_id = sqlc.arg(tournament_id)
    AND membership.roster_id = sqlc.arg(roster_id)
    AND sqlc.arg(participant_id) IN (series.first_participant_id, series.second_participant_id)
    AND series.state = 'active'
    AND attempt.state = 'active'
    AND snapshot.kind = 'normal'
    AND presence.state = 'connected'
ORDER BY assignment.series_id, assignment.attempt_id, assignment.id, presence.id
FOR UPDATE OF membership, series, attempt, assignment, snapshot, receipt, presence;

-- A paused attempt with one open reconnect interval is the only ordinary
-- reconnect action.  The deadline is returned so the coordinator can fence it
-- against its injected clock before entering the nested game usecase.
-- name: LockParticipantConnectionReconnect :many
SELECT assignment.id AS assignment_id,
    assignment.attempt_id AS game_attempt_id,
    assignment.series_id,
    series.state AS series_state,
    attempt.state AS game_state,
    snapshot.kind AS task_kind,
    reconnect.id AS interval_id,
    reconnect.deadline_at AS interval_deadline,
    reconnect.presence_epoch AS interval_presence_epoch,
    presence.id AS presence_id,
    presence.state AS presence_state,
    presence.presence_epoch,
    presence.revision AS presence_revision
FROM wave_series AS membership
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
JOIN task_delivery_receipts AS receipt
    ON receipt.assignment_id = assignment.id
    AND receipt.attempt_id = assignment.attempt_id
    AND receipt.roster_id = assignment.roster_id
    AND receipt.participant_id = sqlc.arg(participant_id)
    AND receipt.snapshot_id = assignment.snapshot_id
JOIN presence_states AS presence
    ON presence.tournament_id = membership.tournament_id
    AND presence.roster_id = membership.roster_id
    AND presence.series_id = series.id
    AND presence.participant_id = sqlc.arg(participant_id)
JOIN reconnect_intervals AS reconnect
    ON reconnect.roster_id = assignment.roster_id
    AND reconnect.series_id = assignment.series_id
    AND reconnect.game_attempt_id = assignment.attempt_id
    AND reconnect.participant_id = sqlc.arg(participant_id)
    AND reconnect.state = 'open'
    AND reconnect.presence_epoch = presence.presence_epoch
    AND reconnect.deadline_at > transaction_timestamp()
WHERE membership.wave_id = sqlc.arg(wave_id)
    AND membership.tournament_id = sqlc.arg(tournament_id)
    AND membership.roster_id = sqlc.arg(roster_id)
    AND sqlc.arg(participant_id) IN (series.first_participant_id, series.second_participant_id)
    AND series.state = 'active'
    AND attempt.state = 'paused'
    AND snapshot.kind = 'normal'
    AND presence.state = 'disconnected'
ORDER BY assignment.series_id, assignment.attempt_id, assignment.id, reconnect.id, presence.id
FOR UPDATE OF membership, series, attempt, assignment, snapshot, receipt, presence, reconnect;
