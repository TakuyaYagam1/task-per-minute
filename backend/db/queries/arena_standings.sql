-- name: LockArenaProjectionRoster :one
SELECT id,
    tournament_id,
    revision,
    locked_at,
    execution_started_at,
    created_at,
    updated_at
FROM arena_rosters
WHERE id = sqlc.arg(roster_id)
    AND tournament_id = sqlc.arg(tournament_id)
FOR UPDATE;

-- name: GetLatestArenaProjectionCutoff :one
SELECT id,
    tournament_id,
    roster_id,
    sequence_number,
    previous_cutoff_id,
    source_kind,
    official_result_revision_id,
    golden_position_commit_id,
    reason,
    cutoff_at,
    created_at
FROM arena_projection_cutoffs
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY sequence_number DESC
LIMIT 1;

-- name: CreateArenaProjectionCutoff :one
INSERT INTO arena_projection_cutoffs (
    id,
    tournament_id,
    roster_id,
    sequence_number,
    previous_cutoff_id,
    source_kind,
    official_result_revision_id,
    golden_position_commit_id,
    reason,
    cutoff_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(sequence_number),
    sqlc.arg(previous_cutoff_id),
    sqlc.arg(source_kind),
    sqlc.arg(official_result_revision_id),
    sqlc.arg(golden_position_commit_id),
    sqlc.arg(reason),
    sqlc.arg(cutoff_at),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    sequence_number,
    previous_cutoff_id,
    source_kind,
    official_result_revision_id,
    golden_position_commit_id,
    reason,
    cutoff_at,
    created_at;

-- name: CreateArenaProjectionRevision :one
INSERT INTO arena_projection_revisions (
    id,
    tournament_id,
    roster_id,
    revision_number,
    previous_revision_id,
    cutoff_id,
    state,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(revision_number),
    sqlc.arg(previous_revision_id),
    sqlc.arg(cutoff_id),
    'draft',
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    revision_number,
    previous_revision_id,
    cutoff_id,
    state,
    published_at,
    superseded_by_revision_id,
    superseded_at,
    supersession_reason,
    created_at;

-- name: CreateArenaProjectionArtifact :one
INSERT INTO arena_projection_artifacts (
    id,
    tournament_id,
    roster_id,
    produced_by_revision_id,
    artifact_kind,
    artifact_key,
    payload,
    payload_digest,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(produced_by_revision_id),
    sqlc.arg(artifact_kind),
    sqlc.arg(artifact_key),
    sqlc.arg(payload),
    sqlc.arg(payload_digest),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    produced_by_revision_id,
    artifact_kind,
    artifact_key,
    payload,
    payload_digest,
    created_at;

-- name: CreateArenaProjectionArtifactMember :one
INSERT INTO arena_projection_artifact_members (
    artifact_id,
    tournament_id,
    roster_id,
    artifact_kind,
    participant_id,
    position,
    score,
    created_at
)
VALUES (
    sqlc.arg(artifact_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(artifact_kind),
    sqlc.arg(participant_id),
    sqlc.arg(position),
    sqlc.arg(score),
    sqlc.arg(created_at)
)
RETURNING artifact_id,
    tournament_id,
    roster_id,
    artifact_kind,
    participant_id,
    position,
    score,
    created_at;

-- name: CreateArenaProjectionDependency :one
INSERT INTO arena_projection_dependencies (
    id,
    artifact_id,
    tournament_id,
    roster_id,
    dependency_kind,
    depends_on_artifact_id,
    official_result_revision_id,
    official_result_series_id,
    golden_position_commit_id,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(artifact_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(dependency_kind),
    sqlc.arg(depends_on_artifact_id),
    sqlc.arg(official_result_revision_id),
    sqlc.arg(official_result_series_id),
    sqlc.arg(golden_position_commit_id),
    sqlc.arg(created_at)
)
RETURNING id,
    artifact_id,
    tournament_id,
    roster_id,
    dependency_kind,
    depends_on_artifact_id,
    official_result_revision_id,
    official_result_series_id,
    golden_position_commit_id,
    created_at;

-- name: LinkArenaProjectionArtifact :one
INSERT INTO arena_projection_revision_artifacts (
    revision_id,
    tournament_id,
    roster_id,
    artifact_kind,
    artifact_id,
    change_kind,
    created_at
)
VALUES (
    sqlc.arg(revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(artifact_kind),
    sqlc.arg(artifact_id),
    sqlc.arg(change_kind),
    sqlc.arg(created_at)
)
RETURNING revision_id,
    tournament_id,
    roster_id,
    artifact_kind,
    artifact_id,
    change_kind,
    created_at;

-- name: LockArenaProjectionRevisionSet :many
SELECT id
FROM arena_projection_revisions
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY revision_number
FOR UPDATE;

-- name: SupersedeArenaProjectionRevisionCAS :one
UPDATE arena_projection_revisions
SET state = 'superseded',
    superseded_by_revision_id = sqlc.arg(superseded_by_revision_id),
    superseded_at = sqlc.arg(superseded_at),
    supersession_reason = sqlc.arg(supersession_reason)
WHERE id = sqlc.arg(id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND state = 'published'
RETURNING id,
    tournament_id,
    roster_id,
    revision_number,
    previous_revision_id,
    cutoff_id,
    state,
    published_at,
    superseded_by_revision_id,
    superseded_at,
    supersession_reason,
    created_at;

-- name: PublishArenaProjectionRevisionCAS :one
UPDATE arena_projection_revisions
SET state = 'published',
    published_at = sqlc.arg(published_at)
WHERE id = sqlc.arg(id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND state = 'draft'
RETURNING id,
    tournament_id,
    roster_id,
    revision_number,
    previous_revision_id,
    cutoff_id,
    state,
    published_at,
    superseded_by_revision_id,
    superseded_at,
    supersession_reason,
    created_at;

-- name: GetArenaProjectionRevisionScoped :one
SELECT id,
    tournament_id,
    roster_id,
    revision_number,
    previous_revision_id,
    cutoff_id,
    state,
    published_at,
    superseded_by_revision_id,
    superseded_at,
    supersession_reason,
    created_at
FROM arena_projection_revisions
WHERE id = sqlc.arg(id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id);

-- name: GetCurrentArenaProjectionRevision :one
SELECT id,
    tournament_id,
    roster_id,
    revision_number,
    previous_revision_id,
    cutoff_id,
    state,
    published_at,
    superseded_by_revision_id,
    superseded_at,
    supersession_reason,
    created_at
FROM arena_projection_revisions
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND state = 'published';

-- name: GetArenaProjectionCutoffByID :one
SELECT id,
    tournament_id,
    roster_id,
    sequence_number,
    previous_cutoff_id,
    source_kind,
    official_result_revision_id,
    golden_position_commit_id,
    reason,
    cutoff_at,
    created_at
FROM arena_projection_cutoffs
WHERE id = sqlc.arg(id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id);

-- name: ListArenaProjectionRevisionArtifacts :many
SELECT revision_id,
    tournament_id,
    roster_id,
    artifact_kind,
    artifact_id,
    change_kind,
    created_at
FROM arena_projection_revision_artifacts
WHERE revision_id = sqlc.arg(revision_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY artifact_kind;

-- name: GetArenaProjectionArtifactScoped :one
SELECT id,
    tournament_id,
    roster_id,
    produced_by_revision_id,
    artifact_kind,
    artifact_key,
    payload,
    payload_digest,
    created_at
FROM arena_projection_artifacts
WHERE id = sqlc.arg(id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id);

-- name: ListArenaProjectionArtifactMembers :many
SELECT artifact_id,
    tournament_id,
    roster_id,
    artifact_kind,
    participant_id,
    position,
    score,
    created_at
FROM arena_projection_artifact_members
WHERE artifact_id = sqlc.arg(artifact_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY position;

-- name: ListArenaProjectionArtifactDependencies :many
SELECT id,
    artifact_id,
    tournament_id,
    roster_id,
    dependency_kind,
    depends_on_artifact_id,
    official_result_revision_id,
    official_result_series_id,
    golden_position_commit_id,
    created_at
FROM arena_projection_dependencies
WHERE artifact_id = sqlc.arg(artifact_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY created_at, id;

-- name: ListArenaProjectionRevisions :many
SELECT id,
    tournament_id,
    roster_id,
    revision_number,
    previous_revision_id,
    cutoff_id,
    state,
    published_at,
    superseded_by_revision_id,
    superseded_at,
    supersession_reason,
    created_at
FROM arena_projection_revisions
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY revision_number;

-- name: GetCurrentArenaStandingsArtifact :one
SELECT artifact.id,
    artifact.tournament_id,
    artifact.roster_id,
    artifact.produced_by_revision_id,
    artifact.artifact_kind,
    artifact.artifact_key,
    artifact.payload,
    artifact.payload_digest,
    artifact.created_at
FROM arena_projection_revisions AS revision
INNER JOIN arena_projection_revision_artifacts AS revision_artifact
    ON revision_artifact.revision_id = revision.id
INNER JOIN arena_projection_artifacts AS artifact
    ON artifact.id = revision_artifact.artifact_id
WHERE revision.tournament_id = sqlc.arg(tournament_id)
    AND revision.roster_id = sqlc.arg(roster_id)
    AND revision.state = 'published'
    AND revision_artifact.artifact_kind = 'standings';
