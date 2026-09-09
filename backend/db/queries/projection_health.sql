-- name: GetProjectionPublicationHealth :one
SELECT COUNT(*)::BIGINT AS pending_count,
    MIN(created_at)::TIMESTAMPTZ AS oldest_pending_at,
    clock_timestamp()::TIMESTAMPTZ AS observed_at
FROM projection_revisions
WHERE state = 'draft';
