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
    COALESCE(participant.seed, 0)::INTEGER AS participant_seed,
    COALESCE(participant.attendance, '')::TEXT AS participant_attendance
FROM tournaments AS tournament
JOIN rosters AS roster ON roster.tournament_id = tournament.id
CROSS JOIN LATERAL (
    SELECT COUNT(*)::BIGINT AS roster_size
    FROM participants AS roster_participant
    WHERE roster_participant.roster_id = roster.id
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
    RETURNING roster.id
)
UPDATE participants AS participant
SET attendance = 'withdrawn',
    updated_at = sqlc.arg(updated_at)
FROM locked_roster AS roster
WHERE participant.roster_id = roster.id
    AND participant.player_id = sqlc.arg(player_id)
    AND participant.attendance IN ('invited', 'registered', 'checked_in')
RETURNING participant.id,
    participant.roster_id,
    participant.player_id,
    participant.seed,
    participant.attendance,
    participant.created_at,
    participant.updated_at;
