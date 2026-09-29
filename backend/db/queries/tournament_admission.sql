-- Public admission uses the existing roster and participant records. The
-- tournament/roster prefix is locked before capacity and attendance checks so
-- concurrent public joins and admin roster mutations observe one order.

-- name: LockTournamentAdmissionScope :one
WITH locked_tournament AS MATERIALIZED (
    SELECT tournament.id AS tournament_id,
        tournament.state AS tournament_state,
        tournament.revision AS tournament_revision,
        tournament.planned_roster_size
    FROM tournaments AS tournament
    WHERE tournament.id = sqlc.arg(tournament_id)
    FOR UPDATE OF tournament
), locked_roster AS MATERIALIZED (
    SELECT roster.id AS roster_id,
        roster.tournament_id,
        roster.revision AS roster_revision,
        (roster.locked_at IS NOT NULL)::BOOLEAN AS roster_locked,
        (roster.execution_started_at IS NOT NULL)::BOOLEAN AS roster_execution_started
    FROM locked_tournament AS tournament
    JOIN rosters AS roster ON roster.tournament_id = tournament.tournament_id
    FOR UPDATE OF roster
)
SELECT tournament.tournament_id,
    tournament.tournament_state,
    tournament.tournament_revision,
    tournament.planned_roster_size,
    roster.roster_id,
    roster.tournament_id AS roster_tournament_id,
    roster.roster_revision,
    roster.roster_locked,
    roster.roster_execution_started
FROM locked_tournament AS tournament
JOIN locked_roster AS roster ON roster.tournament_id = tournament.tournament_id;

-- name: LockAdmissionPlayer :one
SELECT player.id
FROM players AS player
WHERE player.id = sqlc.arg(player_id)
    AND player.deleted_at IS NULL
FOR UPDATE OF player;

-- name: HasConflictingParticipantReservation :one
SELECT EXISTS (
    SELECT 1
    FROM participant_reservations AS reservation
    WHERE reservation.player_id = sqlc.arg(player_id)
        AND reservation.tournament_id <> sqlc.arg(tournament_id)
)::BOOLEAN AS has_conflict;

-- name: GetTournamentAdmissionStatus :one
SELECT tournament.id AS tournament_id,
    tournament.state AS tournament_state,
    tournament.revision AS tournament_revision,
    tournament.planned_roster_size,
    roster.id AS roster_id,
    roster.revision AS roster_revision,
    (roster.locked_at IS NOT NULL)::BOOLEAN AS roster_locked,
    (roster.execution_started_at IS NOT NULL)::BOOLEAN AS roster_execution_started,
    roster_count.roster_size,
    COALESCE(participant.id, '00000000-0000-0000-0000-000000000000'::UUID) AS participant_id,
    COALESCE(participant.player_id, '00000000-0000-0000-0000-000000000000'::UUID) AS participant_player_id,
    COALESCE(
        CASE
            WHEN participant.attendance = 'withdrawn' THEN
                ((participant.seed - 1) % tournament.planned_roster_size) + 1
            ELSE participant.seed
        END,
        0
    )::INTEGER AS participant_seed,
    COALESCE(participant.attendance, '')::TEXT AS participant_attendance
FROM tournaments AS tournament
JOIN rosters AS roster ON roster.tournament_id = tournament.id
CROSS JOIN LATERAL (
    SELECT COUNT(*)::BIGINT AS roster_size
    FROM participants AS roster_participant
    WHERE roster_participant.roster_id = roster.id
        AND roster_participant.attendance <> 'withdrawn'
) AS roster_count
LEFT JOIN participants AS participant
    ON participant.roster_id = roster.id
    AND participant.player_id = sqlc.arg(player_id)
WHERE tournament.id = sqlc.arg(tournament_id);

-- name: FindFirstAvailableParticipantSeed :one
SELECT candidate.seed::INTEGER
FROM generate_series(1, sqlc.arg(planned_roster_size)::INTEGER) AS candidate(seed)
WHERE NOT EXISTS (
    SELECT 1
    FROM participants AS participant
    WHERE participant.roster_id = sqlc.arg(roster_id)
        AND participant.seed = candidate.seed
)
ORDER BY candidate.seed
LIMIT 1;

-- Withdrawn rows stay in participants for history, but move outside the active
-- seed range. The modulo preserves their former seat for historical reads.
-- Rebase legacy rows created before this rule before allocating a new seed.
-- name: RebaseWithdrawnParticipantSeeds :one
WITH legacy_withdrawn AS MATERIALIZED (
    SELECT participant.id,
        participant.seed::BIGINT AS original_seed,
        ROW_NUMBER() OVER (ORDER BY participant.seed, participant.id)::BIGINT AS sequence
    FROM participants AS participant
    WHERE participant.roster_id = sqlc.arg(roster_id)
        AND participant.attendance = 'withdrawn'
        AND participant.seed BETWEEN 1 AND sqlc.arg(planned_roster_size)::INTEGER
), seed_limits AS MATERIALIZED (
    SELECT sqlc.arg(planned_roster_size)::BIGINT AS planned_roster_size,
        COALESCE((
            SELECT MAX(participant.seed)::BIGINT
            FROM participants AS participant
            WHERE participant.roster_id = sqlc.arg(roster_id)
        ), 0) AS max_seed,
        COALESCE(MAX(legacy_withdrawn.original_seed), 0) AS max_original_seed,
        COUNT(legacy_withdrawn.id)::BIGINT AS legacy_count
    FROM legacy_withdrawn
), rebase_limits AS MATERIALIZED (
    SELECT seed_limits.*,
        (seed_limits.max_seed / seed_limits.planned_roster_size) + 1 AS base_offset,
        CASE
            WHEN seed_limits.legacy_count = 0 THEN TRUE
            ELSE seed_limits.max_original_seed + seed_limits.planned_roster_size * (
                (seed_limits.max_seed / seed_limits.planned_roster_size) + seed_limits.legacy_count
            ) <= 2147483647
        END AS can_rebase
    FROM seed_limits
), rebased AS (
    UPDATE participants AS participant
    SET seed = (
        legacy_withdrawn.original_seed + rebase_limits.planned_roster_size * (
            rebase_limits.base_offset + legacy_withdrawn.sequence - 1
        )
    )::INTEGER
    FROM legacy_withdrawn
    CROSS JOIN rebase_limits
    WHERE participant.id = legacy_withdrawn.id
        AND rebase_limits.can_rebase
    RETURNING participant.id
)
SELECT rebase_limits.can_rebase,
    COUNT(rebased.id)::BIGINT AS rebased_count
FROM rebase_limits
LEFT JOIN rebased ON TRUE
GROUP BY rebase_limits.can_rebase;

-- name: InsertRegisteredParticipant :one
WITH locked_roster AS MATERIALIZED (
    UPDATE rosters AS roster
    SET revision = roster.revision + 1,
        updated_at = sqlc.arg(created_at)
    FROM tournaments AS tournament
    WHERE roster.id = sqlc.arg(roster_id)
        AND tournament.id = roster.tournament_id
        AND tournament.state = 'registration'
        AND roster.locked_at IS NULL
        AND roster.execution_started_at IS NULL
    RETURNING roster.id,
        roster.tournament_id
)
INSERT INTO participants (
    id,
    roster_id,
    player_id,
    seed,
    attendance,
    created_at,
    updated_at
)
SELECT sqlc.arg(participant_id) AS participant_id,
    roster.id AS roster_id,
    player.id AS player_id,
    sqlc.arg(seed),
    'registered',
    sqlc.arg(created_at),
    sqlc.arg(created_at)
FROM locked_roster AS roster
JOIN players AS player
    ON player.id = sqlc.arg(player_id)
    AND player.deleted_at IS NULL
RETURNING id,
    roster_id,
    player_id,
    seed,
    attendance,
    created_at,
    updated_at;

-- name: RegisterInvitedParticipant :one
WITH locked_roster AS MATERIALIZED (
    UPDATE rosters AS roster
    SET revision = roster.revision + 1,
        updated_at = sqlc.arg(updated_at)
    FROM tournaments AS tournament
    WHERE roster.id = sqlc.arg(roster_id)
        AND tournament.id = roster.tournament_id
        AND tournament.state = 'registration'
        AND roster.locked_at IS NULL
        AND roster.execution_started_at IS NULL
    RETURNING roster.id
)
UPDATE participants AS participant
SET attendance = 'registered',
    updated_at = sqlc.arg(updated_at)
FROM locked_roster AS roster
WHERE participant.roster_id = roster.id
    AND participant.player_id = sqlc.arg(player_id)
    AND participant.attendance = 'invited'
RETURNING participant.id,
    participant.roster_id,
    participant.player_id,
    participant.seed,
    participant.attendance,
    participant.created_at,
    participant.updated_at;

-- name: CheckInRegisteredParticipant :one
WITH locked_roster AS MATERIALIZED (
    UPDATE rosters AS roster
    SET revision = roster.revision + 1,
        updated_at = sqlc.arg(updated_at)
    FROM tournaments AS tournament
    WHERE roster.id = sqlc.arg(roster_id)
        AND tournament.id = roster.tournament_id
        AND tournament.state = 'registration'
        AND roster.locked_at IS NULL
        AND roster.execution_started_at IS NULL
    RETURNING roster.id
)
UPDATE participants AS participant
SET attendance = 'checked_in',
    updated_at = sqlc.arg(updated_at)
FROM locked_roster AS roster
WHERE participant.roster_id = roster.id
    AND participant.player_id = sqlc.arg(player_id)
    AND participant.attendance = 'registered'
RETURNING participant.id,
    participant.roster_id,
    participant.player_id,
    participant.seed,
    participant.attendance,
    participant.created_at,
    participant.updated_at;

-- name: RegisterWithdrawnParticipant :one
WITH locked_roster AS MATERIALIZED (
    UPDATE rosters AS roster
    SET revision = roster.revision + 1,
        updated_at = sqlc.arg(updated_at)
    FROM tournaments AS tournament
    WHERE roster.id = sqlc.arg(roster_id)
        AND tournament.id = roster.tournament_id
        AND tournament.state = 'registration'
        AND roster.locked_at IS NULL
        AND roster.execution_started_at IS NULL
    RETURNING roster.id
)
UPDATE participants AS participant
SET seed = sqlc.arg(seed),
    attendance = 'registered',
    updated_at = sqlc.arg(updated_at)
FROM locked_roster AS roster
WHERE participant.roster_id = roster.id
    AND participant.player_id = sqlc.arg(player_id)
    AND participant.attendance = 'withdrawn'
RETURNING participant.id,
    participant.roster_id,
    participant.player_id,
    participant.seed,
    participant.attendance,
    participant.created_at,
    participant.updated_at;

-- name: WithdrawAdmissionParticipant :one
WITH locked_roster AS MATERIALIZED (
    UPDATE rosters AS roster
    SET revision = roster.revision + 1,
        updated_at = sqlc.arg(updated_at)
    FROM tournaments AS tournament
    WHERE roster.id = sqlc.arg(roster_id)
        AND tournament.id = roster.tournament_id
        AND tournament.state = 'registration'
        AND roster.locked_at IS NULL
        AND roster.execution_started_at IS NULL
    RETURNING roster.id, roster.tournament_id
), withdrawable AS MATERIALIZED (
    SELECT participant.id,
        participant.seed::BIGINT AS participant_seed,
        tournament.planned_roster_size::BIGINT AS planned_roster_size,
        MAX(roster_participant.seed)::BIGINT AS max_seed
    FROM locked_roster AS roster
    JOIN tournaments AS tournament ON tournament.id = roster.tournament_id
    JOIN participants AS participant ON participant.roster_id = roster.id
    JOIN participants AS roster_participant ON roster_participant.roster_id = roster.id
    WHERE participant.player_id = sqlc.arg(player_id)
        AND participant.attendance IN ('invited', 'registered', 'checked_in')
    GROUP BY participant.id, participant.seed, tournament.planned_roster_size
)
UPDATE participants AS participant
SET seed = (
        withdrawable.participant_seed + withdrawable.planned_roster_size * (
            ((withdrawable.max_seed - withdrawable.participant_seed) / withdrawable.planned_roster_size) + 1
        )
    )::INTEGER,
    attendance = 'withdrawn',
    updated_at = sqlc.arg(updated_at)
FROM withdrawable
WHERE participant.id = withdrawable.id
RETURNING participant.id,
    participant.roster_id,
    participant.player_id,
    participant.seed,
    participant.attendance,
    participant.created_at,
    participant.updated_at;
