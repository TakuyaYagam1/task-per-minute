-- name: GetTaskVersion :one
SELECT task_version.task_id,
    task_version.version,
    task_version.title,
    task_version.description,
    task_version.category,
    task_version.difficulty,
    task_version.time_limit,
    task_version.flag,
    task_version.hint_1,
    task_version.hint_2,
    task_version.hint_3,
    task_version.task_url,
    task_version.source_file_url,
    task_version.content_digest,
    task_version.created_at
FROM task_versions AS task_version
WHERE task_version.task_id = sqlc.arg(task_id)
    AND task_version.version = sqlc.arg(version);

-- Read by the catalog discovery adapter inside a repeatable-read snapshot. The
-- query intentionally returns publication and pool identities only; task
-- bodies remain behind the private task delivery boundary.
-- name: GetLatestTaskPoolPublication :many
WITH latest_publication AS (
    SELECT publication.id, publication.revision, publication.published_at
    FROM task_pool_publications AS publication
    ORDER BY publication.revision DESC, publication.id DESC
    LIMIT 1
)
SELECT publication.id AS publication_id,
    publication.revision AS publication_revision,
    publication.published_at,
    pool.id AS pool_revision_id,
    pool.kind,
    pool.revision AS pool_revision
FROM latest_publication AS publication
JOIN task_pool_revisions AS pool ON pool.publication_id = publication.id
ORDER BY pool.kind, pool.id;

-- name: LockTaskPoolPublicationRevision :many
WITH selected_publication AS (
    SELECT publication.id, publication.revision, publication.published_at
    FROM task_pool_publications AS publication
    WHERE publication.revision = sqlc.arg(content_revision)
    ORDER BY publication.id
    LIMIT 1
    FOR KEY SHARE OF publication
)
SELECT publication.id AS publication_id,
    publication.revision AS publication_revision,
    publication.published_at,
    pool.id AS pool_revision_id,
    pool.kind,
    pool.revision AS pool_revision
FROM selected_publication AS publication
JOIN task_pool_revisions AS pool ON pool.publication_id = publication.id
ORDER BY pool.kind
FOR KEY SHARE OF pool;

-- name: ListTaskPoolVersionHealth :many
SELECT membership.task_id,
    membership.task_version,
    task_version.category,
    pool.id AS pool_revision_id,
    pool.kind AS pool_kind,
    true AS task_exists,
    task.enabled AS task_enabled,
    COALESCE(health.healthy, false) AS task_healthy,
    true AS task_mutation_locked,
    EXISTS (
        SELECT 1
        FROM task_public_exposures AS exposure
        WHERE exposure.task_id = membership.task_id
            AND exposure.task_version = membership.task_version
    ) AS task_publicly_exposed
FROM task_pool_version_memberships AS membership
JOIN task_pool_revisions AS pool ON pool.id = membership.task_pool_revision_id
JOIN task_versions AS task_version
    ON task_version.task_id = membership.task_id
    AND task_version.version = membership.task_version
JOIN tasks AS task ON task.id = membership.task_id
LEFT JOIN LATERAL (
    SELECT attestation.healthy
    FROM task_version_health_attestations AS attestation
    WHERE attestation.task_id = membership.task_id
        AND attestation.task_version = membership.task_version
    ORDER BY attestation.revision DESC
    LIMIT 1
) AS health ON true
WHERE membership.task_pool_revision_id = ANY(sqlc.arg(task_pool_revision_ids)::UUID[])
ORDER BY membership.task_id, membership.task_version, pool.id;

-- The roster mutation path takes this lock in a separate statement immediately
-- before it re-reads ListTaskPoolVersionHealth. A public exposure writer takes
-- the conflicting FOR UPDATE lock on the same task-version row.
-- name: LockTaskPoolVersionsForExposureCheck :exec
SELECT task_version.task_id,
    task_version.version
FROM task_pool_version_memberships AS membership
INNER JOIN task_versions AS task_version
    ON task_version.task_id = membership.task_id
    AND task_version.version = membership.task_version
WHERE membership.task_pool_revision_id = ANY(sqlc.arg(task_pool_revision_ids)::UUID[])
ORDER BY task_version.task_id, task_version.version
FOR SHARE OF task_version;

-- This is the persistence boundary for a real public or spectator disclosure
-- writer. It records an exact task version and never derives exposure from a
-- private participant delivery receipt.
-- name: RecordTaskPublicExposure :one
WITH locked_version AS (
    SELECT task_version.task_id,
        task_version.version
    FROM task_versions AS task_version
    WHERE task_version.task_id = sqlc.arg(task_id)
        AND task_version.version = sqlc.arg(task_version)
    FOR UPDATE
)
INSERT INTO task_public_exposures (
    id,
    task_id,
    task_version,
    audience,
    evidence_id,
    disclosed_at
)
SELECT sqlc.arg(id),
    locked_version.task_id,
    locked_version.version,
    sqlc.arg(audience),
    sqlc.arg(evidence_id),
    sqlc.arg(disclosed_at)
FROM locked_version
RETURNING id, task_id, task_version, audience, evidence_id, disclosed_at, created_at;

-- name: CreateTaskVersionContentValidationAttestation :one
WITH locked_version AS (
    SELECT task_version.task_id, task_version.version
    FROM task_versions AS task_version
    WHERE task_version.task_id = sqlc.arg(task_id)
        AND task_version.version = sqlc.arg(task_version)
    FOR UPDATE
), next_revision AS (
    SELECT COALESCE(MAX(attestation.revision), 0) + 1 AS revision
    FROM task_version_health_attestations AS attestation
    JOIN locked_version ON true
    WHERE attestation.task_id = sqlc.arg(task_id)
        AND attestation.task_version = sqlc.arg(task_version)
)
INSERT INTO task_version_health_attestations (
    task_id,
    task_version,
    revision,
    healthy,
    source
)
SELECT locked_version.task_id,
    locked_version.version,
    next_revision.revision,
    true,
    'content_validation'
FROM locked_version
CROSS JOIN next_revision
RETURNING id, task_id, task_version, revision, healthy, source, attested_at, created_at;

-- name: RecordHealthyTaskVersionProbeAttestation :one
WITH locked_version AS (
    SELECT task_version.task_id, task_version.version
    FROM task_versions AS task_version
    WHERE task_version.task_id = sqlc.arg(task_id)
        AND task_version.version = sqlc.arg(task_version)
    FOR UPDATE
), next_revision AS (
    SELECT COALESCE(MAX(attestation.revision), 0) + 1 AS revision
    FROM task_version_health_attestations AS attestation
    JOIN locked_version ON true
    WHERE attestation.task_id = sqlc.arg(task_id)
        AND attestation.task_version = sqlc.arg(task_version)
)
INSERT INTO task_version_health_attestations (
    task_id,
    task_version,
    revision,
    healthy,
    source
)
SELECT locked_version.task_id,
    locked_version.version,
    next_revision.revision,
    true,
    'probe'
FROM locked_version
CROSS JOIN next_revision
RETURNING id, task_id, task_version, revision, healthy, source, attested_at, created_at;

-- name: RecordUnhealthyTaskVersionProbeAttestation :one
WITH locked_version AS (
    SELECT task_version.task_id, task_version.version
    FROM task_versions AS task_version
    WHERE task_version.task_id = sqlc.arg(task_id)
        AND task_version.version = sqlc.arg(task_version)
    FOR UPDATE
), next_revision AS (
    SELECT COALESCE(MAX(attestation.revision), 0) + 1 AS revision
    FROM task_version_health_attestations AS attestation
    JOIN locked_version ON true
    WHERE attestation.task_id = sqlc.arg(task_id)
        AND attestation.task_version = sqlc.arg(task_version)
)
INSERT INTO task_version_health_attestations (
    task_id,
    task_version,
    revision,
    healthy,
    source
)
SELECT locked_version.task_id,
    locked_version.version,
    next_revision.revision,
    false,
    'probe'
FROM locked_version
CROSS JOIN next_revision
RETURNING id, task_id, task_version, revision, healthy, source, attested_at, created_at;

-- name: GetCurrentTournamentContentConfiguration :one
SELECT configuration.id,
    configuration.tournament_id,
    configuration.revision,
    configuration.reserve_count,
    configuration.pool_publication_id,
    configuration.normal_pool_revision_id,
    normal_pool.revision AS normal_pool_revision,
    configuration.golden_pool_revision_id,
    golden_pool.revision AS golden_pool_revision,
    configuration.published_at
FROM tournament_content_configurations AS configuration
JOIN task_pool_revisions AS normal_pool ON normal_pool.id = configuration.normal_pool_revision_id
JOIN task_pool_revisions AS golden_pool ON golden_pool.id = configuration.golden_pool_revision_id
WHERE configuration.tournament_id = sqlc.arg(tournament_id)
    AND configuration.state = 'published'
ORDER BY configuration.revision DESC, configuration.id DESC
LIMIT 1;

-- name: ListTournamentContentCategoryPoolRevisions :many
SELECT category_pool.id,
    category_pool.configuration_id,
    category_pool.format,
    category_pool.revision,
    category_pool.created_at
FROM tournament_category_pool_revisions AS category_pool
WHERE category_pool.configuration_id = sqlc.arg(configuration_id)
ORDER BY category_pool.format, category_pool.id;

-- name: ListTournamentContentCategoryPoolMemberships :many
SELECT membership.category_pool_revision_id,
    membership.category,
    membership.created_at
FROM tournament_category_pool_memberships AS membership
JOIN tournament_category_pool_revisions AS category_pool ON category_pool.id = membership.category_pool_revision_id
WHERE category_pool.configuration_id = sqlc.arg(configuration_id)
ORDER BY membership.category_pool_revision_id, membership.category;

-- name: ListTournamentContentStageDefaults :many
SELECT stage_default.configuration_id,
    stage_default.stage,
    stage_default.format,
    stage_default.category_mode,
    stage_default.category_pool_revision_id,
    stage_default.task_pool_kind,
    stage_default.created_at
FROM tournament_content_stage_defaults AS stage_default
WHERE stage_default.configuration_id = sqlc.arg(configuration_id)
ORDER BY stage_default.stage;

-- name: CreateTournamentContentConfiguration :one
INSERT INTO tournament_content_configurations (
    id,
    tournament_id,
    revision,
    reserve_count,
    pool_publication_id,
    normal_pool_revision_id,
    golden_pool_revision_id,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(revision),
    sqlc.arg(reserve_count),
    sqlc.arg(pool_publication_id),
    sqlc.arg(normal_pool_revision_id),
    sqlc.arg(golden_pool_revision_id),
    sqlc.arg(created_at)
)
RETURNING id;

-- name: CreateTournamentCategoryPoolRevision :one
INSERT INTO tournament_category_pool_revisions (
    id,
    configuration_id,
    format,
    revision,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(configuration_id),
    sqlc.arg(format),
    sqlc.arg(revision),
    sqlc.arg(created_at)
)
RETURNING id;

-- name: CreateTournamentCategoryPoolMembership :exec
INSERT INTO tournament_category_pool_memberships (
    category_pool_revision_id,
    category,
    created_at
)
VALUES (
    sqlc.arg(category_pool_revision_id),
    sqlc.arg(category),
    sqlc.arg(created_at)
);

-- name: CreateTournamentContentStageDefault :exec
INSERT INTO tournament_content_stage_defaults (
    configuration_id,
    stage,
    format,
    category_mode,
    category_pool_revision_id,
    task_pool_kind,
    categories,
    created_at
)
VALUES (
    sqlc.arg(configuration_id),
    sqlc.arg(stage),
    sqlc.arg(format),
    sqlc.arg(category_mode),
    sqlc.arg(category_pool_revision_id),
    sqlc.arg(task_pool_kind),
    sqlc.arg(categories)::JSONB,
    sqlc.arg(created_at)
);

-- name: PublishTournamentContentConfiguration :one
UPDATE tournament_content_configurations
SET state = 'published',
    published_at = sqlc.arg(published_at)
WHERE id = sqlc.arg(id)
    AND state = 'draft'
RETURNING id;
