-- name: LockExecutionAuthorityScope :one
SELECT tournament.id
FROM tournaments AS tournament
WHERE tournament.id = sqlc.arg(tournament_id)
FOR UPDATE OF tournament;

-- name: ReadExecutionAuthorityTime :one
SELECT clock_timestamp()::TIMESTAMPTZ AS authoritative_at;

-- name: FindExecutionAuthorityCommand :one
SELECT tournament_id,
    command_id,
    holder_id,
    lease_id,
    epoch,
    process_kind,
    revision,
    previous_revision,
    previous_lease_id,
    previous_epoch,
    acquired_at,
    renewed_at,
    expires_at,
    created_at
FROM execution_authority_leases
WHERE tournament_id = sqlc.arg(tournament_id)
    AND command_id = sqlc.arg(command_id);

-- name: LoadExecutionAuthority :one
SELECT tournament_id,
    command_id,
    holder_id,
    lease_id,
    epoch,
    process_kind,
    revision,
    previous_revision,
    previous_lease_id,
    previous_epoch,
    acquired_at,
    renewed_at,
    expires_at,
    created_at
FROM execution_authority_leases
WHERE tournament_id = sqlc.arg(tournament_id)
ORDER BY revision DESC
LIMIT 1;

-- name: CreateExecutionAuthorityLease :one
INSERT INTO execution_authority_leases (
    tournament_id,
    command_id,
    holder_id,
    lease_id,
    epoch,
    process_kind,
    revision,
    previous_revision,
    previous_lease_id,
    previous_epoch,
    acquired_at,
    renewed_at,
    expires_at,
    created_at
)
VALUES (
    sqlc.arg(tournament_id),
    sqlc.arg(command_id),
    sqlc.arg(holder_id),
    sqlc.arg(lease_id),
    sqlc.arg(epoch),
    sqlc.arg(process_kind),
    sqlc.arg(revision),
    sqlc.narg(previous_revision),
    sqlc.narg(previous_lease_id),
    sqlc.narg(previous_epoch),
    sqlc.arg(acquired_at),
    sqlc.arg(renewed_at),
    sqlc.arg(expires_at),
    sqlc.arg(created_at)
)
RETURNING tournament_id,
    command_id,
    holder_id,
    lease_id,
    epoch,
    process_kind,
    revision,
    previous_revision,
    previous_lease_id,
    previous_epoch,
    acquired_at,
    renewed_at,
    expires_at,
    created_at;
