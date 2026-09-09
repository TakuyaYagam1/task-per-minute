-- name: GetCurrentProjectionBracketArtifact :one
SELECT artifact.id,
    artifact.tournament_id,
    artifact.roster_id,
    artifact.produced_by_revision_id,
    artifact.artifact_kind,
    artifact.artifact_key,
    artifact.payload,
    artifact.payload_digest,
    artifact.created_at
FROM projection_revisions AS revision
INNER JOIN projection_revision_artifacts AS revision_artifact
    ON revision_artifact.revision_id = revision.id
INNER JOIN projection_artifacts AS artifact
    ON artifact.id = revision_artifact.artifact_id
WHERE revision.tournament_id = sqlc.arg(tournament_id)
    AND revision.roster_id = sqlc.arg(roster_id)
    AND revision.state = 'published'
    AND revision_artifact.artifact_kind = 'bracket';
