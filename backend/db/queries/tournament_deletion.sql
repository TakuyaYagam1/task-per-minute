-- name: LockTournamentDeletionScope :one
SELECT tournament.id AS tournament_id,
    roster.id AS roster_id,
    tournament.state,
    tournament.revision,
    tournament.updated_at,
    tournament.finished_at,
    tournament.deleted_at
FROM tournaments AS tournament
INNER JOIN rosters AS roster ON roster.tournament_id = tournament.id
WHERE tournament.id = sqlc.arg(tournament_id)
FOR UPDATE OF tournament, roster;

-- name: FindTournamentDeletion :one
SELECT command_id,
    tournament_id,
    actor_id,
    source_revision,
    resulting_revision,
    source_state,
    cancelled,
    reason,
    deleted_at,
    created_at
FROM tournament_deletions
WHERE tournament_id = sqlc.arg(tournament_id)
    AND command_id = sqlc.arg(command_id);

-- name: MarkTournamentDeleted :one
-- A cancellation inserted in this transaction has deferred evidence tied to its
-- lifecycle revision and updated_at. Deletion metadata must not change either.
UPDATE tournaments
SET deleted_at = sqlc.arg(deleted_at),
    revision = revision + CASE WHEN sqlc.arg(cancelled)::BOOLEAN THEN 0 ELSE 1 END,
    updated_at = CASE
        WHEN sqlc.arg(cancelled)::BOOLEAN THEN updated_at
        ELSE sqlc.arg(deleted_at)
    END
WHERE id = sqlc.arg(tournament_id)
    AND revision = sqlc.arg(expected_revision)
    AND deleted_at IS NULL
    AND (NOT sqlc.arg(cancelled)::BOOLEAN OR state = 'cancelled')
RETURNING id AS tournament_id,
    state,
    revision,
    updated_at,
    deleted_at;

-- name: CreateTournamentDeletion :one
INSERT INTO tournament_deletions (
    command_id,
    tournament_id,
    actor_id,
    source_revision,
    resulting_revision,
    source_state,
    cancelled,
    reason,
    deleted_at,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(actor_id),
    sqlc.arg(source_revision),
    sqlc.arg(resulting_revision),
    sqlc.arg(source_state),
    sqlc.arg(cancelled),
    sqlc.arg(reason),
    sqlc.arg(deleted_at),
    sqlc.arg(created_at)
)
RETURNING command_id,
    tournament_id,
    actor_id,
    source_revision,
    resulting_revision,
    source_state,
    cancelled,
    reason,
    deleted_at,
    created_at;
