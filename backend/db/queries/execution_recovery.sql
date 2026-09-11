-- name: ListExecutionRecoveryTournaments :many
WITH recovery_tournaments AS (
    SELECT epoch.tournament_id
    FROM execution_game_epochs AS epoch
    JOIN game_attempts AS attempt
        ON attempt.id = epoch.game_attempt_id
        AND attempt.series_id = epoch.series_id
        AND attempt.roster_id = epoch.roster_id
    JOIN waves AS wave
        ON wave.id = epoch.wave_id
        AND wave.tournament_id = epoch.tournament_id
        AND wave.roster_id = epoch.roster_id
    JOIN tournaments AS tournament ON tournament.id = epoch.tournament_id
    WHERE attempt.state IN ('active', 'paused')
        AND wave.state IN ('active', 'paused')
        AND tournament.state = 'swiss'

    UNION

    SELECT attempt.tournament_id
    FROM golden_attempts AS attempt
    JOIN tournaments AS tournament ON tournament.id = attempt.tournament_id
    WHERE attempt.state IN ('active', 'technical_pause')
        AND tournament.state = 'golden'
)
SELECT tournament_id
FROM recovery_tournaments
ORDER BY tournament_id;

-- name: ListExecutionRecoveryGames :many
WITH current_authority AS (
    SELECT lease.tournament_id,
        lease.holder_id,
        lease.lease_id,
        lease.epoch,
        lease.revision,
        lease.renewed_at,
        lease.expires_at
    FROM execution_authority_leases AS lease
    WHERE lease.tournament_id = sqlc.arg(tournament_id)
        AND lease.holder_id = sqlc.arg(authority_holder_id)
        AND lease.lease_id = sqlc.arg(authority_lease_id)
        AND lease.epoch = sqlc.arg(authority_epoch)
        AND lease.revision = (
            SELECT MAX(latest.revision)
            FROM execution_authority_leases AS latest
            WHERE latest.tournament_id = sqlc.arg(tournament_id)
        )
    ORDER BY lease.revision DESC
    LIMIT 1
)
SELECT attempt.id AS game_attempt_id,
    attempt.revision AS attempt_revision,
    attempt.attempt_number,
    attempt.state AS attempt_state,
    attempt.started_at,
    epoch.tournament_id,
    epoch.roster_id,
    epoch.wave_id,
    epoch.series_id,
    epoch.slot_id,
    assignment.id AS assignment_id,
    assignment.attempt_id AS assignment_attempt_id,
    assignment.snapshot_id,
    slot.category,
    COALESCE(rebind.authority_lease_id, epoch.authority_lease_id) AS bound_lease_id,
    COALESCE(rebind.authority_epoch, epoch.authority_epoch) AS bound_epoch,
    current_authority.revision AS current_authority_revision,
    COALESCE(
        pause_clock.resumed_deadline,
        attempt.started_at + snapshot.time_limit * INTERVAL '1 second'
    )::TIMESTAMPTZ AS due_at
FROM execution_game_epochs AS epoch
JOIN current_authority ON TRUE
LEFT JOIN LATERAL (
    SELECT successor.authority_lease_id,
        successor.authority_epoch,
        successor.authority_revision
    FROM execution_game_epoch_rebinds AS successor
    WHERE successor.game_attempt_id = epoch.game_attempt_id
    ORDER BY successor.rebind_sequence DESC
    LIMIT 1
) AS rebind ON TRUE
JOIN game_attempts AS attempt
    ON attempt.id = epoch.game_attempt_id
    AND attempt.series_id = epoch.series_id
    AND attempt.roster_id = epoch.roster_id
JOIN game_slots AS slot
    ON slot.id = epoch.slot_id
    AND slot.series_id = epoch.series_id
    AND slot.roster_id = epoch.roster_id
JOIN assignments AS assignment
    ON assignment.attempt_id = attempt.id
    AND assignment.series_id = attempt.series_id
    AND assignment.roster_id = attempt.roster_id
    AND assignment.state = 'active'
JOIN task_snapshots AS snapshot ON snapshot.id = assignment.snapshot_id
JOIN waves AS wave
    ON wave.id = epoch.wave_id
    AND wave.tournament_id = epoch.tournament_id
    AND wave.roster_id = epoch.roster_id
JOIN tournaments AS tournament ON tournament.id = epoch.tournament_id
LEFT JOIN LATERAL (
    SELECT clock.resumed_deadline
    FROM pause_clocks AS clock
    JOIN pauses AS pause
        ON pause.id = clock.pause_id
        AND pause.state = 'resumed'
    WHERE clock.game_attempt_id = attempt.id
        AND clock.resumed_deadline IS NOT NULL
    ORDER BY clock.resumed_at DESC, clock.pause_id DESC
    LIMIT 1
) AS pause_clock ON TRUE
WHERE epoch.tournament_id = sqlc.arg(tournament_id)
    AND attempt.state IN ('active', 'paused')
    AND wave.state IN ('active', 'paused')
    AND tournament.state = 'swiss'
    AND clock_timestamp() >= current_authority.renewed_at
    AND clock_timestamp() < current_authority.expires_at
ORDER BY attempt.id;

-- name: FindExecutionEpochReplayByGame :one
SELECT command_id,
    game_attempt_id,
    tournament_id,
    roster_id,
    wave_id,
    series_id,
    slot_id,
    assignment_id,
    assignment_attempt_id,
    current_holder_id,
    current_lease_id,
    current_epoch,
    expected_lease_revision,
    broken_lease_id,
    broken_epoch,
    expected_attempt_revision,
    command_digest,
    record_document,
    replayed_at,
    created_at
FROM execution_epoch_replays
WHERE game_attempt_id = sqlc.arg(game_attempt_id);

-- name: FindExecutionEpochReplayByCommand :one
SELECT command_id,
    game_attempt_id,
    tournament_id,
    roster_id,
    wave_id,
    series_id,
    slot_id,
    assignment_id,
    assignment_attempt_id,
    current_holder_id,
    current_lease_id,
    current_epoch,
    expected_lease_revision,
    broken_lease_id,
    broken_epoch,
    expected_attempt_revision,
    command_digest,
    record_document,
    replayed_at,
    created_at
FROM execution_epoch_replays
WHERE command_id = sqlc.arg(command_id);

-- name: LockExecutionEpochReplayFence :one
WITH current_authority AS (
    SELECT lease.tournament_id,
        lease.command_id,
        lease.holder_id,
        lease.lease_id,
        lease.epoch,
        lease.process_kind,
        lease.revision,
        lease.previous_revision,
        lease.previous_lease_id,
        lease.previous_epoch,
        lease.acquired_at,
        lease.renewed_at,
        lease.expires_at,
        lease.created_at
    FROM execution_authority_leases AS lease
    WHERE lease.tournament_id = sqlc.arg(tournament_id)
    ORDER BY lease.revision DESC
    LIMIT 1
    FOR UPDATE
)
SELECT attempt.id AS game_attempt_id,
    attempt.revision AS attempt_revision,
    attempt.attempt_number,
    attempt.state AS attempt_state,
    assignment.id AS assignment_id,
    assignment.attempt_id AS assignment_attempt_id,
    assignment.snapshot_id,
    slot.category,
    epoch.tournament_id,
    epoch.roster_id,
    epoch.wave_id,
    epoch.series_id,
    epoch.slot_id,
    COALESCE(rebind.authority_holder_id, epoch.authority_holder_id) AS authority_holder_id,
    COALESCE(rebind.authority_lease_id, epoch.authority_lease_id) AS authority_lease_id,
    COALESCE(rebind.authority_epoch, epoch.authority_epoch) AS authority_epoch,
    COALESCE(rebind.authority_revision, epoch.authority_revision) AS authority_revision,
    COALESCE(
        pause_clock.resumed_deadline,
        attempt.started_at + snapshot.time_limit * INTERVAL '1 second'
    )::TIMESTAMPTZ AS due_at,
    current_authority.command_id AS current_command_id,
    current_authority.holder_id AS current_holder_id,
    current_authority.lease_id AS current_lease_id,
    current_authority.epoch AS current_epoch,
    current_authority.process_kind AS current_process_kind,
    current_authority.revision AS current_revision,
    current_authority.previous_revision AS current_previous_revision,
    current_authority.previous_lease_id AS current_previous_lease_id,
    current_authority.previous_epoch AS current_previous_epoch,
    current_authority.acquired_at AS current_acquired_at,
    current_authority.renewed_at AS current_renewed_at,
    current_authority.expires_at AS current_expires_at,
    current_authority.created_at AS current_created_at
FROM execution_game_epochs AS epoch
JOIN current_authority ON TRUE
LEFT JOIN LATERAL (
    SELECT successor.authority_holder_id,
        successor.authority_lease_id,
        successor.authority_epoch,
        successor.authority_revision
    FROM execution_game_epoch_rebinds AS successor
    WHERE successor.game_attempt_id = epoch.game_attempt_id
    ORDER BY successor.rebind_sequence DESC
    LIMIT 1
) AS rebind ON TRUE
JOIN game_attempts AS attempt
    ON attempt.id = epoch.game_attempt_id
    AND attempt.series_id = epoch.series_id
    AND attempt.roster_id = epoch.roster_id
JOIN game_slots AS slot
    ON slot.id = epoch.slot_id
    AND slot.series_id = epoch.series_id
    AND slot.roster_id = epoch.roster_id
JOIN assignments AS assignment
    ON assignment.attempt_id = attempt.id
    AND assignment.series_id = attempt.series_id
    AND assignment.roster_id = attempt.roster_id
    AND assignment.state = 'active'
JOIN task_snapshots AS snapshot ON snapshot.id = assignment.snapshot_id
JOIN waves AS wave
    ON wave.id = epoch.wave_id
    AND wave.tournament_id = epoch.tournament_id
    AND wave.roster_id = epoch.roster_id
JOIN tournaments AS tournament ON tournament.id = epoch.tournament_id
LEFT JOIN LATERAL (
    SELECT clock.resumed_deadline
    FROM pause_clocks AS clock
    JOIN pauses AS pause
        ON pause.id = clock.pause_id
        AND pause.state = 'resumed'
    WHERE clock.game_attempt_id = attempt.id
        AND clock.resumed_deadline IS NOT NULL
    ORDER BY clock.resumed_at DESC, clock.pause_id DESC
    LIMIT 1
) AS pause_clock ON TRUE
WHERE epoch.game_attempt_id = sqlc.arg(game_attempt_id)
    AND epoch.tournament_id = sqlc.arg(tournament_id)
    AND epoch.roster_id = sqlc.arg(roster_id)
    AND epoch.wave_id = sqlc.arg(wave_id)
    AND epoch.series_id = sqlc.arg(series_id)
    AND epoch.slot_id = sqlc.arg(slot_id)
    AND assignment.id = sqlc.arg(assignment_id)
    AND assignment.attempt_id = sqlc.arg(assignment_attempt_id)
    AND attempt.state = 'active'
    AND wave.state = 'active'
    AND tournament.state = 'swiss'
    AND clock_timestamp() >= current_authority.renewed_at
    AND clock_timestamp() < current_authority.expires_at
FOR UPDATE OF attempt, epoch, assignment;

-- name: CreateExecutionEpochReplay :one
INSERT INTO execution_epoch_replays (
    command_id,
    game_attempt_id,
    tournament_id,
    roster_id,
    wave_id,
    series_id,
    slot_id,
    assignment_id,
    assignment_attempt_id,
    current_holder_id,
    current_lease_id,
    current_epoch,
    expected_lease_revision,
    broken_lease_id,
    broken_epoch,
    expected_attempt_revision,
    command_digest,
    record_document,
    replayed_at,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(game_attempt_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(wave_id),
    sqlc.arg(series_id),
    sqlc.arg(slot_id),
    sqlc.arg(assignment_id),
    sqlc.arg(assignment_attempt_id),
    sqlc.arg(current_holder_id),
    sqlc.arg(current_lease_id),
    sqlc.arg(current_epoch),
    sqlc.arg(expected_lease_revision),
    sqlc.arg(broken_lease_id),
    sqlc.arg(broken_epoch),
    sqlc.arg(expected_attempt_revision),
    sqlc.arg(command_digest),
    sqlc.arg(record_document),
    sqlc.arg(replayed_at),
    sqlc.arg(replayed_at)
)
RETURNING command_id;
