-- Reconnect command receipts are the domain idempotency boundary.  The
-- complete record is retained in record_document so a retry can return the
-- exact result without re-running a settlement or selecting a lease.
-- name: GetTournamentReconnectCommandReceipt :one
SELECT command_id,
    tournament_id,
    roster_id,
    wave_id,
    mutation_kind,
    participant_id,
    interval_id,
    expected_authority_revision,
    result_authority_revision,
    schema_version,
    record_document,
    recorded_at,
    created_at
FROM reconnect_command_receipts
WHERE tournament_id = sqlc.arg(tournament_id)
    AND command_id = sqlc.arg(command_id);

-- The latest receipt supplies the durable aggregate revision for this
-- participant when the current game has already resumed.  The command id is
-- a deterministic tie breaker for timestamps supplied by one authoritative
-- transaction.
-- name: GetLatestTournamentReconnectCommandReceipt :one
SELECT command_id,
    tournament_id,
    roster_id,
    wave_id,
    mutation_kind,
    participant_id,
    interval_id,
    expected_authority_revision,
    result_authority_revision,
    schema_version,
    record_document,
    recorded_at,
    created_at
FROM reconnect_command_receipts
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND wave_id = sqlc.arg(wave_id)
    AND participant_id = sqlc.arg(participant_id)
ORDER BY created_at DESC, command_id DESC
LIMIT 1;

-- Receipt persistence is intentionally last in CommitMutation.  The primary
-- key makes a concurrent command with the same identity fail atomically; the
-- caller maps that unique violation to a retry conflict.
-- name: ListTournamentReconnectCommandReceipts :many
SELECT command_id,
    tournament_id,
    roster_id,
    wave_id,
    mutation_kind,
    participant_id,
    interval_id,
    expected_authority_revision,
    result_authority_revision,
    schema_version,
    record_document,
    recorded_at,
    created_at
FROM reconnect_command_receipts
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND wave_id = sqlc.arg(wave_id)
ORDER BY created_at DESC, command_id DESC;

-- Receipt persistence is intentionally last in CommitMutation.  The primary
-- key makes a concurrent command with the same identity fail atomically; the
-- caller maps that unique violation to a retry conflict.
-- name: InsertTournamentReconnectCommandReceipt :one
INSERT INTO reconnect_command_receipts (
    command_id,
    tournament_id,
    roster_id,
    wave_id,
    mutation_kind,
    participant_id,
    interval_id,
    expected_authority_revision,
    result_authority_revision,
    schema_version,
    record_document,
    recorded_at,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(wave_id),
    sqlc.arg(mutation_kind),
    sqlc.arg(participant_id),
    sqlc.narg(interval_id)::UUID,
    sqlc.arg(expected_authority_revision),
    sqlc.arg(result_authority_revision),
    sqlc.arg(schema_version),
    sqlc.arg(record_document),
    sqlc.arg(recorded_at),
    sqlc.arg(created_at)
)
RETURNING command_id;

-- Load the one game pause that can own reconnect intervals.  A graph scope
-- may contain several games; the adapter rejects that ambiguity before using
-- this row, so no arbitrary active pause is ever selected.
-- name: LockTournamentReconnectGamePause :one
SELECT sqlc.embed(pause)
FROM pauses AS pause
JOIN series AS series
    ON series.id = pause.series_id
    AND series.tournament_id = pause.tournament_id
    AND series.roster_id = pause.roster_id
JOIN wave_series AS membership
    ON membership.series_id = series.id
    AND membership.wave_id = sqlc.arg(wave_id)
    AND membership.roster_id = pause.roster_id
WHERE pause.tournament_id = sqlc.arg(tournament_id)
    AND pause.roster_id = sqlc.arg(roster_id)
    AND pause.game_attempt_id = sqlc.arg(game_attempt_id)
    AND pause.scope_kind = 'game_attempt'
    AND pause.parent_pause_id IS NULL
    AND pause.state = 'active'
ORDER BY pause.started_at DESC, pause.id DESC
LIMIT 1
FOR UPDATE;

-- Read the frozen clock belonging to an active game pause.
-- Reconnect freeze advances the domain clock revision together with the game
-- authority.  Keep that revision in the durable clock instead of using the
-- normal pause helper's fixed initial revision.
-- name: CreateTournamentReconnectPauseClock :one
INSERT INTO pause_clocks (pause_id, game_attempt_id, original_deadline, frozen_at,
    frozen_remaining_ms, revision, created_at, updated_at)
VALUES (sqlc.arg(pause_id), sqlc.arg(game_attempt_id), sqlc.arg(original_deadline),
    sqlc.arg(frozen_at), sqlc.arg(frozen_remaining_ms), sqlc.arg(revision),
    sqlc.arg(frozen_at), sqlc.arg(frozen_at))
RETURNING pause_id;

-- Read the frozen clock belonging to an active game pause.
-- name: LockTournamentReconnectPauseClock :one
SELECT sqlc.embed(pause_clock)
FROM pause_clocks AS pause_clock
JOIN pauses AS pause
    ON pause.id = pause_clock.pause_id
    AND pause.tournament_id = sqlc.arg(tournament_id)
    AND pause.roster_id = sqlc.arg(roster_id)
    AND pause.game_attempt_id = sqlc.arg(game_attempt_id)
JOIN series AS series
    ON series.id = pause.series_id
    AND series.tournament_id = pause.tournament_id
    AND series.roster_id = pause.roster_id
JOIN wave_series AS membership
    ON membership.series_id = series.id
    AND membership.wave_id = sqlc.arg(wave_id)
    AND membership.roster_id = pause.roster_id
WHERE pause_clock.pause_id = sqlc.arg(pause_id)
    AND pause_clock.game_attempt_id = sqlc.arg(game_attempt_id)
FOR UPDATE OF pause_clock;

-- Resume decisions must cite immutable pre-mutation presence evidence.  The
-- live rows have already advanced to the reconnecting state by the time the
-- decision is appended.
-- name: LockTournamentReconnectPausePresenceSnapshots :many
SELECT snapshot.pause_id,
    snapshot.roster_id,
    snapshot.series_id,
    snapshot.participant_id,
    snapshot.presence_state,
    snapshot.presence_epoch,
    snapshot.presence_revision,
    snapshot.captured_at,
    snapshot.created_at
FROM pause_presence_snapshots AS snapshot
WHERE snapshot.pause_id = sqlc.arg(pause_id)
    AND snapshot.roster_id = sqlc.arg(roster_id)
    AND snapshot.series_id = sqlc.arg(series_id)
ORDER BY snapshot.participant_id
FOR UPDATE OF snapshot;

-- After a prior reconnect/resume, the current game clock is the most recent
-- resumed clock.  The query is also used as a consistency check for an active
-- game that has no currently active pause.
-- name: GetLatestTournamentReconnectResumedClock :one
SELECT sqlc.embed(pause_clock)
FROM pause_clocks AS pause_clock
JOIN pauses AS pause
    ON pause.id = pause_clock.pause_id
    AND pause.tournament_id = sqlc.arg(tournament_id)
    AND pause.roster_id = sqlc.arg(roster_id)
    AND pause.game_attempt_id = sqlc.arg(game_attempt_id)
JOIN series AS series
    ON series.id = pause.series_id
    AND series.tournament_id = pause.tournament_id
    AND series.roster_id = pause.roster_id
JOIN wave_series AS membership
    ON membership.series_id = series.id
    AND membership.wave_id = sqlc.arg(wave_id)
    AND membership.roster_id = pause.roster_id
WHERE pause_clock.game_attempt_id = sqlc.arg(game_attempt_id)
    AND pause_clock.resumed_at IS NOT NULL
    AND pause_clock.resumed_deadline IS NOT NULL
ORDER BY pause_clock.resumed_at DESC, pause_clock.pause_id DESC
LIMIT 1;

-- Presence updates are guarded by the identity, epoch, revision, and prior
-- state.  The trigger on presence_states enforces the same transition shape;
-- this predicate is the application-visible CAS result.
-- name: UpdateTournamentReconnectPresenceCAS :one
UPDATE presence_states
SET state = sqlc.arg(next_state),
    connected_at = CASE
        WHEN sqlc.arg(next_state)::VARCHAR = 'connected' THEN sqlc.arg(updated_at)::TIMESTAMPTZ
        ELSE connected_at
    END,
    presence_epoch = presence_epoch + 1,
    revision = revision + 1,
    disconnected_at = CASE
        WHEN sqlc.arg(next_state)::VARCHAR = 'connected' THEN NULL
        ELSE sqlc.arg(updated_at)::TIMESTAMPTZ
    END,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND series_id = sqlc.arg(series_id)
    AND participant_id = sqlc.arg(participant_id)
    AND state = sqlc.arg(expected_state)
    AND presence_epoch = sqlc.arg(expected_epoch)
    AND revision = sqlc.arg(expected_revision)
    AND sqlc.arg(next_state)::VARCHAR IN ('connected', 'disconnected')
RETURNING id,
    tournament_id,
    roster_id,
    series_id,
    participant_id,
    state,
    presence_epoch,
    revision,
    connected_at,
    disconnected_at,
    updated_at;

-- Close an open interval as reconnected or expired.  The live presence CAS is
-- performed before this statement, satisfying the deferred interval guard.
-- name: CloseTournamentReconnectIntervalCAS :one
UPDATE reconnect_intervals
SET state = sqlc.arg(next_state),
    closed_at = sqlc.arg(closed_at),
    revision = revision + 1,
    updated_at = sqlc.arg(closed_at)
WHERE id = sqlc.arg(id)
    AND pause_id = sqlc.arg(pause_id)
    AND roster_id = sqlc.arg(roster_id)
    AND series_id = sqlc.arg(series_id)
    AND game_attempt_id = sqlc.arg(game_attempt_id)
    AND participant_id = sqlc.arg(participant_id)
    AND presence_epoch = sqlc.arg(presence_epoch)
    AND state = 'open'
    AND revision = sqlc.arg(expected_revision)
    AND sqlc.arg(next_state)::VARCHAR IN ('reconnected', 'expired')
    AND sqlc.arg(closed_at)::TIMESTAMPTZ >= opened_at
    AND (
        (sqlc.arg(next_state)::VARCHAR = 'reconnected'
            AND sqlc.arg(closed_at)::TIMESTAMPTZ <= deadline_at)
        OR (sqlc.arg(next_state)::VARCHAR = 'expired'
            AND sqlc.arg(closed_at)::TIMESTAMPTZ >= deadline_at)
    )
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

-- Terminal cancellation is separate so a continuation predecessor can never
-- accidentally gain suspension provenance from this game-level writer.
-- Normal Wave pause suspension remains owned by the normal-pause adapter.
-- name: CancelTournamentReconnectIntervalCAS :one
UPDATE reconnect_intervals
SET state = 'cancelled',
    closed_at = sqlc.arg(closed_at),
    revision = revision + 1,
    updated_at = sqlc.arg(closed_at)
WHERE id = sqlc.arg(id)
    AND pause_id = sqlc.arg(pause_id)
    AND roster_id = sqlc.arg(roster_id)
    AND series_id = sqlc.arg(series_id)
    AND game_attempt_id = sqlc.arg(game_attempt_id)
    AND participant_id = sqlc.arg(participant_id)
    AND presence_epoch = sqlc.arg(presence_epoch)
    AND state = 'open'
    AND revision = sqlc.arg(expected_revision)
    AND suspended_by_pause_id IS NULL
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

-- Series is part of the reconnect authority graph.  Every state-preserving
-- reconnect mutation still advances its revision when its game advances.
-- name: AdvanceTournamentReconnectSeriesCAS :one
UPDATE series
SET revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(series_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND revision = sqlc.arg(expected_revision)
    AND state = sqlc.arg(expected_state)
RETURNING id;
