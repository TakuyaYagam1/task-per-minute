-- name: CreateTournament :one
INSERT INTO tournaments (
    id,
    name,
    public_id,
    planned_roster_size,
    content_revision,
    preset,
    state,
    revision,
    created_at,
    updated_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(name),
    sqlc.arg(public_id),
    sqlc.arg(planned_roster_size),
    sqlc.arg(content_revision),
    'tournament_v1',
    'draft',
    1,
    sqlc.arg(created_at),
    sqlc.arg(created_at)
)
RETURNING id,
    preset,
    state,
    paused_from_state,
    revision,
    created_at,
    updated_at,
    started_at,
    finished_at,
    name,
    public_id,
    planned_roster_size,
    content_revision,
    deleted_at;

-- name: CreateTournamentRoster :one
INSERT INTO rosters (
    id,
    tournament_id,
    revision,
    created_at,
    updated_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    1,
    sqlc.arg(created_at),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    revision,
    locked_at,
    execution_started_at,
    created_at,
    updated_at;

-- name: LockTournamentCreateCommand :one
SELECT 1::integer AS locked
FROM pg_advisory_xact_lock(hashtextextended(sqlc.arg(command_id)::text, 0));

-- name: GetTournamentCreateReceipt :one
SELECT command_id,
    actor_id,
    request_digest,
    tournament_id,
    roster_id,
    content_configuration_id,
    result_schema_version,
    result_preset,
    result_state,
    result_revision,
    result_roster_size,
    result_created_at,
    result_updated_at,
    result_changed,
    result_document,
    created_at
FROM tournament_create_command_receipts
WHERE command_id = sqlc.arg(command_id);

-- name: InsertTournamentCreateReceipt :one
INSERT INTO tournament_create_command_receipts (
    command_id,
    actor_id,
    request_digest,
    tournament_id,
    roster_id,
    content_configuration_id,
    result_schema_version,
    result_preset,
    result_state,
    result_revision,
    result_roster_size,
    result_created_at,
    result_updated_at,
    result_changed,
    result_document,
    created_at
)
VALUES (
    sqlc.arg(command_id)::uuid,
    sqlc.arg(actor_id)::uuid,
    sqlc.arg(request_digest)::bytea,
    sqlc.arg(tournament_id)::uuid,
    sqlc.arg(roster_id)::uuid,
    sqlc.arg(content_configuration_id)::uuid,
    sqlc.arg(result_schema_version)::smallint,
    sqlc.arg(result_preset)::varchar,
    sqlc.arg(result_state)::varchar,
    sqlc.arg(result_revision)::bigint,
    sqlc.arg(result_roster_size)::integer,
    sqlc.arg(result_created_at)::timestamptz,
    sqlc.arg(result_updated_at)::timestamptz,
    sqlc.arg(result_changed)::boolean,
    jsonb_build_object(
        'schema_version', sqlc.arg(result_schema_version)::smallint,
        'tournament_id', sqlc.arg(tournament_id)::uuid,
        'roster_id', sqlc.arg(roster_id)::uuid,
        'preset', sqlc.arg(result_preset)::varchar,
        'state', sqlc.arg(result_state)::varchar,
        'revision', sqlc.arg(result_revision)::bigint,
        'roster_size', sqlc.arg(result_roster_size)::integer,
        'name', sqlc.arg(result_name)::varchar,
        'public_id', sqlc.arg(result_public_id)::varchar,
        'planned_roster_size', sqlc.arg(result_planned_roster_size)::integer,
        'content_revision', sqlc.arg(result_content_revision)::bigint,
        'created_at', sqlc.arg(result_created_at)::timestamptz,
        'updated_at', sqlc.arg(result_updated_at)::timestamptz,
        'changed', sqlc.arg(result_changed)::boolean
    ),
    sqlc.arg(created_at)::timestamptz
)
RETURNING command_id,
    actor_id,
    request_digest,
    tournament_id,
    roster_id,
    content_configuration_id,
    result_schema_version,
    result_preset,
    result_state,
    result_revision,
    result_roster_size,
    result_created_at,
    result_updated_at,
    result_changed,
    result_document,
    created_at;

-- name: GetTournament :one
SELECT id,
    preset,
    state,
    paused_from_state,
    revision,
    created_at,
    updated_at,
    started_at,
    finished_at,
    name,
    public_id,
    planned_roster_size,
    content_revision,
    deleted_at
FROM tournaments
WHERE id = sqlc.arg(id)
    AND deleted_at IS NULL;

-- name: GetTournamentSummary :one
SELECT tournament.id,
    tournament.name,
    tournament.public_id,
    tournament.planned_roster_size,
    tournament.content_revision,
    tournament.preset,
    tournament.state,
    tournament.paused_from_state,
    tournament.revision,
    tournament.created_at,
    tournament.updated_at,
    tournament.started_at,
    tournament.finished_at,
    roster.id AS roster_id,
    COUNT(participant.id)::BIGINT AS roster_size
FROM tournaments AS tournament
INNER JOIN rosters AS roster ON roster.tournament_id = tournament.id
LEFT JOIN participants AS participant ON participant.roster_id = roster.id
WHERE tournament.id = sqlc.arg(id)
    AND tournament.deleted_at IS NULL
GROUP BY tournament.id,
    roster.id;

-- name: ListTournaments :many
SELECT id,
    preset,
    state,
    paused_from_state,
    revision,
    created_at,
    updated_at,
    started_at,
    finished_at,
    name,
    public_id,
    planned_roster_size,
    content_revision,
    deleted_at
FROM tournaments
WHERE deleted_at IS NULL
ORDER BY created_at DESC,
    id;

-- name: ListTournamentSummaries :many
SELECT tournament.id,
    tournament.name,
    tournament.public_id,
    tournament.planned_roster_size,
    tournament.content_revision,
    tournament.preset,
    tournament.state,
    tournament.paused_from_state,
    tournament.revision,
    tournament.created_at,
    tournament.updated_at,
    tournament.started_at,
    tournament.finished_at,
    roster.id AS roster_id,
    COUNT(participant.id)::BIGINT AS roster_size
FROM tournaments AS tournament
INNER JOIN rosters AS roster ON roster.tournament_id = tournament.id
LEFT JOIN participants AS participant ON participant.roster_id = roster.id
WHERE tournament.deleted_at IS NULL
GROUP BY tournament.id,
    roster.id
ORDER BY tournament.created_at DESC,
    tournament.id;

-- name: UpdateTournamentCAS :one
UPDATE tournaments
SET state = sqlc.arg(next_state),
    paused_from_state = sqlc.narg(paused_from_state)::VARCHAR,
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at),
    started_at = sqlc.narg(started_at)::TIMESTAMPTZ,
    finished_at = sqlc.narg(finished_at)::TIMESTAMPTZ
WHERE id = sqlc.arg(id)
    AND revision = sqlc.arg(expected_revision)
    AND state = sqlc.arg(expected_state)
RETURNING id,
    preset,
    state,
    paused_from_state,
    revision,
    created_at,
    updated_at,
    started_at,
    finished_at,
    name,
    public_id,
    planned_roster_size,
    content_revision,
    deleted_at;

-- name: GetTournamentRoster :one
SELECT id,
    tournament_id,
    revision,
    locked_at,
    execution_started_at,
    created_at,
    updated_at
FROM rosters
WHERE id = sqlc.arg(id);

-- name: LockTournamentRosterForUpdate :one
SELECT id,
    tournament_id,
    revision,
    locked_at,
    execution_started_at,
    created_at,
    updated_at
FROM rosters
WHERE id = sqlc.arg(id)
FOR UPDATE;

-- name: InsertTournamentParticipant :one
WITH locked_roster AS MATERIALIZED (
    UPDATE rosters AS roster
    SET revision = roster.revision + 1,
        updated_at = sqlc.arg(created_at)
    WHERE roster.id = sqlc.arg(roster_id)
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
SELECT sqlc.arg(id) AS participant_id,
    roster.id AS roster_id,
    player.id AS player_id,
    sqlc.arg(seed),
    sqlc.arg(attendance),
    sqlc.arg(created_at),
    sqlc.arg(created_at)
FROM locked_roster AS roster
INNER JOIN players AS player
    ON player.id = sqlc.arg(player_id)
    AND player.deleted_at IS NULL
RETURNING id,
    roster_id,
    player_id,
    seed,
    attendance,
    created_at,
    updated_at;

-- name: UpdateTournamentParticipantAttendanceCAS :one
UPDATE participants AS participant
SET attendance = sqlc.arg(next_attendance),
    updated_at = sqlc.arg(updated_at)
FROM rosters AS roster
WHERE participant.id = sqlc.arg(id)
    AND participant.attendance = sqlc.arg(expected_attendance)
    AND roster.id = participant.roster_id
    AND roster.locked_at IS NULL
    AND roster.execution_started_at IS NULL
RETURNING participant.id,
    participant.roster_id,
    participant.player_id,
    participant.seed,
    participant.attendance,
    participant.created_at,
    participant.updated_at;

-- name: ReplaceWithdrawnTournamentParticipant :one
UPDATE participants AS participant
SET id = sqlc.arg(replacement_participant_id),
    player_id = replacement.id,
    attendance = 'invited',
    updated_at = sqlc.arg(replaced_at)
FROM rosters AS roster,
    players AS replacement
WHERE participant.id = sqlc.arg(withdrawn_participant_id)
    AND participant.roster_id = sqlc.arg(roster_id)
    AND participant.attendance = 'withdrawn'
    AND roster.id = participant.roster_id
    AND roster.locked_at IS NULL
    AND roster.execution_started_at IS NULL
    AND replacement.id = sqlc.arg(replacement_player_id)
    AND replacement.deleted_at IS NULL
RETURNING participant.id,
    participant.roster_id,
    participant.player_id,
    participant.seed,
    participant.attendance,
    participant.created_at,
    participant.updated_at;

-- name: ListTournamentParticipants :many
SELECT participant.id,
    participant.roster_id,
    roster.tournament_id,
    participant.player_id,
    participant.seed,
    participant.attendance,
    participant.created_at,
    participant.updated_at
FROM participants AS participant
JOIN rosters AS roster ON roster.id = participant.roster_id
WHERE participant.roster_id = sqlc.arg(roster_id)
ORDER BY participant.seed,
    participant.id;

-- name: ListCheckedInTournamentPlayerIDs :many
SELECT player_id
FROM participants
WHERE roster_id = sqlc.arg(roster_id)
    AND attendance = 'checked_in'
ORDER BY player_id;

-- name: ReserveCheckedInTournamentParticipants :many
INSERT INTO participant_reservations (
    player_id,
    tournament_id,
    revision,
    acquired_at,
    updated_at
)
SELECT participant.player_id,
    roster.tournament_id,
    1,
    sqlc.arg(acquired_at),
    sqlc.arg(acquired_at)
FROM participants AS participant
JOIN rosters AS roster ON roster.id = participant.roster_id
WHERE participant.roster_id = sqlc.arg(roster_id)
    AND participant.attendance = 'checked_in'
ORDER BY participant.player_id
ON CONFLICT (player_id) DO UPDATE
SET updated_at = participant_reservations.updated_at
WHERE participant_reservations.tournament_id = EXCLUDED.tournament_id
RETURNING player_id;

-- name: LockTournamentRosterCAS :one
UPDATE rosters
SET revision = revision + 1,
    locked_at = sqlc.arg(locked_at),
    updated_at = sqlc.arg(locked_at)
WHERE id = sqlc.arg(id)
    AND revision = sqlc.arg(expected_revision)
    AND locked_at IS NULL
    AND execution_started_at IS NULL
RETURNING id,
    tournament_id,
    revision,
    locked_at,
    execution_started_at,
    created_at,
    updated_at;

-- name: UnlockTournamentRosterCAS :one
UPDATE rosters
SET revision = revision + 1,
    locked_at = NULL,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
    AND revision = sqlc.arg(expected_revision)
    AND locked_at IS NOT NULL
    AND execution_started_at IS NULL
RETURNING id,
    tournament_id,
    revision,
    locked_at,
    execution_started_at,
    created_at,
    updated_at;

-- name: MarkTournamentRosterExecutionStartedCAS :one
UPDATE rosters
SET revision = revision + 1,
    execution_started_at = sqlc.arg(started_at),
    updated_at = sqlc.arg(started_at)
WHERE id = sqlc.arg(id)
    AND revision = sqlc.arg(expected_revision)
    AND locked_at IS NOT NULL
    AND execution_started_at IS NULL
RETURNING id,
    tournament_id,
    revision,
    locked_at,
    execution_started_at,
    created_at,
    updated_at;

-- name: ReleaseTournamentReservations :execrows
DELETE FROM participant_reservations
WHERE tournament_id = sqlc.arg(tournament_id);

-- name: ListTournamentReservations :many
SELECT player_id,
    reservation_id,
    tournament_id,
    revision,
    acquired_at,
    updated_at
FROM participant_reservations
WHERE tournament_id = sqlc.arg(tournament_id)
ORDER BY player_id;
