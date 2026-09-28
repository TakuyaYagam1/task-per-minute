-- POST-MVP-031 configuration edits are fenced by one projection/cutoff
-- snapshot.  The write queries repeat the fence so a caller cannot turn a
-- stale read into a published configuration by omitting the outer lock.

-- name: GetTournamentConfigurationEditRoster :one
SELECT id,
    tournament_id,
    revision,
    locked_at
FROM rosters
WHERE tournament_id = sqlc.arg(tournament_id)::UUID;

-- name: LockTournamentConfigurationEditAuthority :one
WITH published_projection AS (
    SELECT projection.id,
        projection.revision_number,
        projection.cutoff_id
    FROM projection_revisions AS projection
    WHERE projection.tournament_id = sqlc.arg(tournament_id)::UUID
        AND projection.roster_id = sqlc.arg(roster_id)::UUID
        AND projection.state = 'published'
    ORDER BY projection.revision_number DESC, projection.id DESC
    LIMIT 1
    FOR UPDATE
)
SELECT tournament.id AS tournament_id,
    tournament.state AS tournament_state,
    tournament.revision AS tournament_revision,
    roster.id AS roster_id,
    roster.revision AS roster_revision,
    roster.locked_at AS roster_locked_at,
    head.configuration_id,
    head.configuration_revision,
    head.revision AS configuration_head_revision,
    configuration.state AS configuration_state,
    projection.id AS projection_revision_id,
    projection.revision_number AS projection_revision,
    projection.cutoff_id,
    cutoff.sequence_number AS cutoff_sequence
FROM tournaments AS tournament
JOIN rosters AS roster
    ON roster.tournament_id = tournament.id
    AND roster.id = sqlc.arg(roster_id)::UUID
JOIN tournament_content_configuration_heads AS head
    ON head.tournament_id = tournament.id
JOIN tournament_content_configurations AS configuration
    ON configuration.id = head.configuration_id
    AND configuration.tournament_id = tournament.id
JOIN published_projection AS projection ON true
JOIN projection_cutoffs AS cutoff
    ON cutoff.id = projection.cutoff_id
    AND cutoff.tournament_id = tournament.id
    AND cutoff.roster_id = roster.id
WHERE tournament.id = sqlc.arg(tournament_id)::UUID
    AND head.configuration_id = sqlc.arg(expected_configuration_id)::UUID
    AND head.configuration_revision = sqlc.arg(expected_configuration_revision)::BIGINT
    AND head.revision = sqlc.arg(expected_configuration_head_revision)::BIGINT
    AND projection.id = sqlc.arg(expected_projection_revision_id)::UUID
    AND projection.revision_number = sqlc.arg(expected_projection_revision)::BIGINT
    AND cutoff.id = sqlc.arg(expected_cutoff_id)::UUID
    AND cutoff.sequence_number = sqlc.arg(expected_cutoff_sequence)::BIGINT
FOR UPDATE OF tournament, roster, head, configuration, cutoff;

-- name: GetTournamentConfigurationEditAuthority :one
WITH published_projection AS (
    SELECT projection.id,
        projection.revision_number,
        projection.cutoff_id
    FROM projection_revisions AS projection
    WHERE projection.tournament_id = sqlc.arg(tournament_id)::UUID
        AND projection.roster_id = sqlc.arg(roster_id)::UUID
        AND projection.state = 'published'
    ORDER BY projection.revision_number DESC, projection.id DESC
    LIMIT 1
)
SELECT tournament.id AS tournament_id,
    tournament.state AS tournament_state,
    tournament.revision AS tournament_revision,
    roster.id AS roster_id,
    roster.revision AS roster_revision,
    roster.locked_at AS roster_locked_at,
    head.configuration_id,
    head.configuration_revision,
    head.revision AS configuration_head_revision,
    configuration.state AS configuration_state,
    projection.id AS projection_revision_id,
    projection.revision_number AS projection_revision,
    projection.cutoff_id,
    cutoff.sequence_number AS cutoff_sequence
FROM tournaments AS tournament
JOIN rosters AS roster
    ON roster.tournament_id = tournament.id
    AND roster.id = sqlc.arg(roster_id)::UUID
JOIN tournament_content_configuration_heads AS head
    ON head.tournament_id = tournament.id
JOIN tournament_content_configurations AS configuration
    ON configuration.id = head.configuration_id
    AND configuration.tournament_id = tournament.id
JOIN published_projection AS projection ON true
JOIN projection_cutoffs AS cutoff
    ON cutoff.id = projection.cutoff_id
    AND cutoff.tournament_id = tournament.id
    AND cutoff.roster_id = roster.id
WHERE tournament.id = sqlc.arg(tournament_id)::UUID;

-- Configuration reads run in a repeatable-read snapshot.  Keep the standings
-- payload read-only and bind it to the exact published projection selected by
-- the authority query above.
-- name: GetTournamentConfigurationEditPublishedStandings :one
SELECT standings.payload AS standings_payload
FROM projection_revisions AS projection
JOIN projection_revision_artifacts AS revision_artifact
    ON revision_artifact.revision_id = projection.id
    AND revision_artifact.artifact_kind = 'standings'
JOIN projection_artifacts AS standings
    ON standings.id = revision_artifact.artifact_id
    AND standings.artifact_kind = 'standings'
    AND standings.tournament_id = projection.tournament_id
    AND standings.roster_id = projection.roster_id
JOIN projection_revisions AS producer
    ON producer.id = standings.produced_by_revision_id
WHERE projection.id = sqlc.arg(projection_revision_id)::UUID
    AND projection.tournament_id = sqlc.arg(tournament_id)::UUID
    AND projection.roster_id = sqlc.arg(roster_id)::UUID
    AND projection.revision_number = sqlc.arg(expected_projection_revision)::BIGINT
    AND projection.state = 'published';

-- Keep all attendance states in the read surface so the adapter can retain
-- the same fail-closed validation used by tournamentAdminStandings.  Only
-- checked-in rows become pairing participants and receive stable seeds.
-- name: ListTournamentConfigurationEditParticipants :many
SELECT participant.id,
    CASE
        WHEN participant.attendance = 'withdrawn' THEN
            ((participant.seed - 1) % tournament.planned_roster_size) + 1
        ELSE participant.seed
    END::INTEGER AS seed,
    participant.attendance
FROM participants AS participant
JOIN rosters AS roster ON roster.id = participant.roster_id
JOIN tournaments AS tournament ON tournament.id = roster.tournament_id
WHERE participant.roster_id = sqlc.arg(roster_id)::UUID
ORDER BY participant.seed, participant.id;

-- name: GetTournamentConfigurationEditConfiguration :one
SELECT configuration.id,
    configuration.tournament_id,
    configuration.revision,
    configuration.state,
    configuration.reserve_count,
    configuration.pool_publication_id,
    configuration.normal_pool_revision_id,
    normal_pool.revision AS normal_pool_revision,
    configuration.golden_pool_revision_id,
    golden_pool.revision AS golden_pool_revision,
    configuration.created_at,
    configuration.published_at
FROM tournament_content_configurations AS configuration
JOIN task_pool_revisions AS normal_pool
    ON normal_pool.id = configuration.normal_pool_revision_id
JOIN task_pool_revisions AS golden_pool
    ON golden_pool.id = configuration.golden_pool_revision_id
WHERE configuration.id = sqlc.arg(configuration_id)::UUID
    AND configuration.tournament_id = sqlc.arg(tournament_id)::UUID;

-- name: ListTournamentConfigurationEditPoolRevisions :many
SELECT pool.id,
    pool.configuration_id,
    pool.format,
    pool.revision,
    pool.created_at
FROM tournament_category_pool_revisions AS pool
WHERE pool.configuration_id = sqlc.arg(configuration_id)::UUID
ORDER BY pool.format, pool.id;

-- name: ListTournamentConfigurationEditPoolMemberships :many
SELECT membership.category_pool_revision_id,
    membership.category,
    membership.created_at
FROM tournament_category_pool_memberships AS membership
JOIN tournament_category_pool_revisions AS pool
    ON pool.id = membership.category_pool_revision_id
WHERE pool.configuration_id = sqlc.arg(configuration_id)::UUID
ORDER BY membership.category_pool_revision_id, membership.category;

-- name: ListTournamentConfigurationEditStageDefaults :many
SELECT defaults.configuration_id,
    defaults.stage,
    defaults.format,
    defaults.category_mode,
    defaults.category_pool_revision_id,
    defaults.task_pool_kind,
    defaults.categories,
    defaults.created_at
FROM tournament_content_stage_defaults AS defaults
WHERE defaults.configuration_id = sqlc.arg(configuration_id)::UUID
ORDER BY defaults.stage;

-- The read surface intentionally filters superseded Series identities.  The
-- old rows remain queryable through the edit ledger and lineage tables.
-- name: ListActiveTournamentConfigurationEditSeries :many
SELECT series.id,
    series.tournament_id,
    series.roster_id,
    series.first_participant_id,
    series.second_participant_id,
    series.format,
    series.state,
    series.revision,
    series.content_configuration_id,
    series.content_configuration_revision,
    series.category_mode,
    series.effective_categories,
    series.supersedes_series_id,
    series.superseded_by_series_id,
    series.created_at,
    series.updated_at,
    series.started_at,
    series.finished_at
FROM series
WHERE series.tournament_id = sqlc.arg(tournament_id)::UUID
    AND series.roster_id = sqlc.arg(roster_id)::UUID
    AND series.state <> 'superseded'
ORDER BY series.created_at, series.id;

-- Series does not duplicate its stage identity. Resolve it from the retained
-- Swiss Wave and playoff evidence instead of adding a mutable stage column.
-- A remaining unstarted Series belongs to the Golden stage in the tournament
-- lifecycle and is therefore the safe fallback here.
-- name: ListTournamentConfigurationEditSeriesStageBindings :many
SELECT series.id,
    CASE
        WHEN swiss_stage.round_number IS NOT NULL THEN 'swiss'
        WHEN semifinal.series_id IS NOT NULL THEN 'semifinal'
        WHEN final_stage.final_series_id IS NOT NULL THEN 'final'
        ELSE 'golden'
    END AS stage,
    COALESCE(swiss_stage.round_number, 0)::SMALLINT AS round_number
FROM series
LEFT JOIN LATERAL (
    SELECT round.round_number
    FROM wave_series
    JOIN swiss_wave_links AS link ON link.wave_id = wave_series.wave_id
    JOIN swiss_rounds AS round ON round.id = link.round_id
    WHERE wave_series.series_id = series.id
        AND link.roster_id = series.roster_id
    ORDER BY round.round_number DESC, round.id DESC
    LIMIT 1
) AS swiss_stage ON true
LEFT JOIN tournament_stage_playoff_semifinals AS semifinal
    ON semifinal.series_id = series.id
    AND semifinal.tournament_id = series.tournament_id
    AND semifinal.roster_id = series.roster_id
LEFT JOIN tournament_stage_playoff_finals AS final_stage
    ON final_stage.final_series_id = series.id
    AND final_stage.tournament_id = series.tournament_id
    AND final_stage.roster_id = series.roster_id
WHERE series.tournament_id = sqlc.arg(tournament_id)::UUID
    AND series.roster_id = sqlc.arg(roster_id)::UUID
ORDER BY series.id;

-- Reservation ownership is resolved through the immutable assignment plan
-- source draft. The caller still supplies exact reservation IDs when it
-- mutates; this read is only for cutoff and unlock-intent validation.
-- name: ListTournamentConfigurationEditReservations :many
SELECT reservation.id,
    draft_revision.series_id,
    assignment.series_id AS assignment_series_id,
    plan.id AS owner_id,
    plan.revision_id AS source_revision_id,
    reservation.revision,
    reservation.state,
    reservation.disclosed_at,
    reservation.committed_at,
    reservation.task_id,
    reservation.task_version,
    reservation.plan_id,
    reservation.branch_id,
    edge.selection_evidence
FROM task_version_reservations AS reservation
JOIN assignment_plans AS plan ON plan.id = reservation.plan_id
JOIN assignment_plan_edges AS edge
    ON edge.id = reservation.edge_id
    AND edge.plan_id = reservation.plan_id
    AND edge.branch_id = reservation.branch_id
LEFT JOIN draft_revisions AS draft_revision
    ON draft_revision.id = plan.source_draft_revision_id
LEFT JOIN assignments AS assignment
    ON assignment.reservation_id = reservation.id
    AND assignment.plan_id = reservation.plan_id
WHERE plan.tournament_id = sqlc.arg(tournament_id)::UUID
    AND plan.roster_id = sqlc.arg(roster_id)::UUID
    AND reservation.state IN ('reserved', 'committed')
ORDER BY draft_revision.series_id, reservation.id;

-- name: ListTournamentConfigurationEditSwissRounds :many
SELECT round.id,
    round.tournament_id,
    round.roster_id,
    round.round_number,
    round.revision,
    round.generation_kind,
    round.lock_revision,
    round.locked_at,
    round.content_configuration_id,
    round.content_configuration_revision,
    round.category_mode,
    round.effective_categories,
    COALESCE((
        SELECT jsonb_agg(DISTINCT round_participant.participant_id ORDER BY round_participant.participant_id)
        FROM (
            SELECT member.participant_id
            FROM swiss_pairing_members AS member
            WHERE member.round_id = round.id
                AND member.roster_id = round.roster_id
            UNION
            SELECT link.bye_participant_id
            FROM swiss_wave_links AS link
            WHERE link.round_id = round.id
                AND link.roster_id = round.roster_id
                AND link.bye_participant_id IS NOT NULL
        ) AS round_participant
    ), '[]'::jsonb) AS participant_ids,
    COALESCE((
        SELECT jsonb_agg(DISTINCT wave_series.series_id ORDER BY wave_series.series_id)
        FROM swiss_wave_links AS link
        JOIN wave_series ON wave_series.wave_id = link.wave_id
        JOIN series ON series.id = wave_series.series_id
        WHERE link.round_id = round.id
            AND link.roster_id = round.roster_id
            AND series.state <> 'superseded'
    ), '[]'::jsonb) AS series_ids,
    (
        SELECT link.bye_participant_id
        FROM swiss_wave_links AS link
        WHERE link.round_id = round.id
            AND link.roster_id = round.roster_id
        ORDER BY link.wave_id
        LIMIT 1
    ) AS bye_participant_id,
    (
        SELECT link.bye_revision_id
        FROM swiss_wave_links AS link
        WHERE link.round_id = round.id
            AND link.roster_id = round.roster_id
        ORDER BY link.wave_id
        LIMIT 1
    ) AS bye_revision_id,
    round.generated_at,
    round.created_at,
    round.updated_at
FROM swiss_rounds AS round
WHERE round.tournament_id = sqlc.arg(tournament_id)::UUID
    AND round.roster_id = sqlc.arg(roster_id)::UUID
ORDER BY round.round_number, round.id;

-- The draft revision is derived from the locked active head.  Existing
-- published configurations are never updated or deleted.
-- name: CreateTournamentConfigurationEditDraft :one
WITH locked_head AS (
    SELECT head.tournament_id,
        head.configuration_revision
    FROM tournament_content_configuration_heads AS head
    WHERE head.tournament_id = sqlc.arg(tournament_id)::UUID
        AND head.configuration_id = sqlc.arg(source_configuration_id)::UUID
        AND head.configuration_revision = sqlc.arg(source_configuration_revision)::BIGINT
        AND head.revision = sqlc.arg(source_configuration_head_revision)::BIGINT
    FOR UPDATE
)
INSERT INTO tournament_content_configurations (
    id,
    tournament_id,
    revision,
    state,
    reserve_count,
    pool_publication_id,
    normal_pool_revision_id,
    golden_pool_revision_id,
    created_at
)
SELECT sqlc.arg(configuration_id)::UUID,
    locked_head.tournament_id,
    locked_head.configuration_revision + 1,
    'draft',
    sqlc.arg(reserve_count)::SMALLINT,
    sqlc.arg(pool_publication_id)::UUID,
    sqlc.arg(normal_pool_revision_id)::UUID,
    sqlc.arg(golden_pool_revision_id)::UUID,
    sqlc.arg(created_at)::TIMESTAMPTZ
FROM locked_head
JOIN tournament_content_configurations AS current_configuration
    ON current_configuration.tournament_id = locked_head.tournament_id
    AND current_configuration.id = sqlc.arg(source_configuration_id)::UUID
    AND current_configuration.revision = locked_head.configuration_revision
RETURNING id,
    tournament_id,
    revision,
    state,
    reserve_count,
    pool_publication_id,
    normal_pool_revision_id,
    golden_pool_revision_id,
    created_at,
    published_at;

-- name: CreateTournamentConfigurationEditPoolRevision :one
INSERT INTO tournament_category_pool_revisions (
    id,
    configuration_id,
    format,
    revision,
    created_at
)
VALUES (
    sqlc.arg(pool_revision_id)::UUID,
    sqlc.arg(configuration_id)::UUID,
    sqlc.arg(format)::VARCHAR,
    sqlc.arg(revision)::BIGINT,
    sqlc.arg(created_at)::TIMESTAMPTZ
)
RETURNING id, configuration_id, format, revision, created_at;

-- name: CreateTournamentConfigurationEditPoolMembership :one
INSERT INTO tournament_category_pool_memberships (
    category_pool_revision_id,
    category,
    created_at
)
VALUES (
    sqlc.arg(pool_revision_id)::UUID,
    sqlc.arg(category)::VARCHAR,
    sqlc.arg(created_at)::TIMESTAMPTZ
)
RETURNING category_pool_revision_id, category, created_at;

-- name: CreateTournamentConfigurationEditStageDefault :one
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
    sqlc.arg(configuration_id)::UUID,
    sqlc.arg(stage)::VARCHAR,
    sqlc.arg(format)::VARCHAR,
    sqlc.arg(category_mode)::VARCHAR,
    sqlc.arg(category_pool_revision_id)::UUID,
    sqlc.arg(task_pool_kind)::VARCHAR,
    sqlc.arg(categories)::JSONB,
    sqlc.arg(created_at)::TIMESTAMPTZ
)
RETURNING configuration_id,
    stage,
    format,
    category_mode,
    category_pool_revision_id,
    task_pool_kind,
    categories,
    created_at;

-- name: PublishTournamentConfigurationEditDraftCAS :one
WITH authority AS (
    SELECT head.tournament_id,
        head.configuration_id,
        head.configuration_revision,
        head.revision AS configuration_head_revision
    FROM tournament_content_configuration_heads AS head
    JOIN rosters AS roster ON roster.tournament_id = head.tournament_id
    JOIN LATERAL (
        SELECT projection.id,
            projection.revision_number,
            projection.cutoff_id
        FROM projection_revisions AS projection
        WHERE projection.tournament_id = head.tournament_id
            AND projection.roster_id = roster.id
            AND projection.state = 'published'
        ORDER BY projection.revision_number DESC, projection.id DESC
        LIMIT 1
        FOR UPDATE
    ) AS projection ON true
    JOIN projection_cutoffs AS cutoff
        ON cutoff.id = projection.cutoff_id
        AND cutoff.tournament_id = head.tournament_id
        AND cutoff.roster_id = roster.id
    WHERE head.tournament_id = sqlc.arg(tournament_id)::UUID
        AND roster.id = sqlc.arg(roster_id)::UUID
        AND head.configuration_id = sqlc.arg(source_configuration_id)::UUID
        AND head.configuration_revision = sqlc.arg(source_configuration_revision)::BIGINT
        AND head.revision = sqlc.arg(source_configuration_head_revision)::BIGINT
        AND projection.id = sqlc.arg(expected_projection_revision_id)::UUID
        AND projection.revision_number = sqlc.arg(expected_projection_revision)::BIGINT
        AND cutoff.id = sqlc.arg(expected_cutoff_id)::UUID
        AND cutoff.sequence_number = sqlc.arg(expected_cutoff_sequence)::BIGINT
    FOR UPDATE OF head, roster, cutoff
)
UPDATE tournament_content_configurations AS configuration
SET state = 'published',
    published_at = sqlc.arg(published_at)::TIMESTAMPTZ
FROM authority
WHERE configuration.id = sqlc.arg(configuration_id)::UUID
    AND configuration.tournament_id = authority.tournament_id
    AND configuration.state = 'draft'
    AND configuration.revision = authority.configuration_revision + 1
RETURNING configuration.id,
    configuration.tournament_id,
    configuration.revision,
    configuration.state,
    configuration.published_at;

-- name: CreateTournamentConfigurationEditCommand :one
INSERT INTO tournament_configuration_edit_commands (
    command_id,
    tournament_id,
    roster_id,
    actor_id,
    action,
    source_projection_revision_id,
    source_projection_revision,
    source_cutoff_id,
    source_cutoff_sequence,
    source_configuration_id,
    source_configuration_revision,
    result_configuration_id,
    result_configuration_revision,
    source_tournament_revision,
    result_tournament_revision,
    source_series_id,
    source_series_revision,
    result_series_id,
    result_series_revision,
    source_round_id,
    source_round_revision,
    result_round_revision,
    request_digest,
    intent_document,
    result_document,
    occurred_at,
    created_at
)
VALUES (
    sqlc.arg(command_id)::UUID,
    sqlc.arg(tournament_id)::UUID,
    sqlc.arg(roster_id)::UUID,
    sqlc.arg(actor_id)::UUID,
    sqlc.arg(action)::VARCHAR,
    sqlc.arg(source_projection_revision_id)::UUID,
    sqlc.arg(source_projection_revision)::BIGINT,
    sqlc.arg(source_cutoff_id)::UUID,
    sqlc.arg(source_cutoff_sequence)::BIGINT,
    sqlc.arg(source_configuration_id)::UUID,
    sqlc.arg(source_configuration_revision)::BIGINT,
    sqlc.arg(result_configuration_id)::UUID,
    sqlc.arg(result_configuration_revision)::BIGINT,
    sqlc.arg(source_tournament_revision)::BIGINT,
    sqlc.arg(result_tournament_revision)::BIGINT,
    sqlc.narg(source_series_id)::UUID,
    sqlc.narg(source_series_revision)::BIGINT,
    sqlc.narg(result_series_id)::UUID,
    sqlc.narg(result_series_revision)::BIGINT,
    sqlc.narg(source_round_id)::UUID,
    sqlc.narg(source_round_revision)::BIGINT,
    sqlc.narg(result_round_revision)::BIGINT,
    sqlc.arg(request_digest)::BYTEA,
    sqlc.arg(intent_document)::JSONB,
    sqlc.arg(result_document)::JSONB,
    sqlc.arg(occurred_at)::TIMESTAMPTZ,
    sqlc.arg(created_at)::TIMESTAMPTZ
)
RETURNING command_id,
    tournament_id,
    roster_id,
    action,
    source_configuration_id,
    source_configuration_revision,
    result_configuration_id,
    result_configuration_revision,
    result_tournament_revision,
    result_series_id,
    result_series_revision,
    result_round_revision,
    occurred_at,
    created_at;

-- name: GetTournamentConfigurationEditCommand :one
SELECT command_id,
    tournament_id,
    roster_id,
    actor_id,
    action,
    source_projection_revision_id,
    source_projection_revision,
    source_cutoff_id,
    source_cutoff_sequence,
    source_configuration_id,
    source_configuration_revision,
    result_configuration_id,
    result_configuration_revision,
    source_tournament_revision,
    result_tournament_revision,
    source_series_id,
    source_series_revision,
    result_series_id,
    result_series_revision,
    source_round_id,
    source_round_revision,
    result_round_revision,
    request_digest,
    intent_document,
    result_document,
    occurred_at,
    created_at
FROM tournament_configuration_edit_commands
WHERE tournament_id = sqlc.arg(tournament_id)::UUID
    AND command_id = sqlc.arg(command_id)::UUID;

-- name: CreateTournamentConfigurationEditUnlockIntent :one
INSERT INTO tournament_configuration_edit_unlock_intents (
    intent_id,
    command_id,
    tournament_id,
    roster_id,
    artifact_kind,
    artifact_id,
    expected_revision,
    reservation_ids,
    invalidate_proof,
    invalidate_readiness,
    created_at
)
VALUES (
    sqlc.arg(intent_id)::UUID,
    sqlc.arg(command_id)::UUID,
    sqlc.arg(tournament_id)::UUID,
    sqlc.arg(roster_id)::UUID,
    sqlc.arg(artifact_kind)::VARCHAR,
    sqlc.arg(artifact_id)::UUID,
    sqlc.arg(expected_revision)::BIGINT,
    sqlc.arg(reservation_ids)::UUID[],
    sqlc.arg(invalidate_proof)::BOOLEAN,
    sqlc.arg(invalidate_readiness)::BOOLEAN,
    sqlc.arg(created_at)::TIMESTAMPTZ
)
RETURNING intent_id,
    command_id,
    tournament_id,
    roster_id,
    artifact_kind,
    artifact_id,
    expected_revision,
    reservation_ids,
    invalidate_proof,
    invalidate_readiness,
    created_at;

-- name: LockTournamentConfigurationEditReservations :many
SELECT reservation.id,
    reservation.plan_id,
    reservation.branch_id,
    reservation.task_id,
    reservation.task_version,
    reservation.revision,
    reservation.state,
    reservation.disclosed_at,
    reservation.committed_at
FROM task_version_reservations AS reservation
JOIN assignment_plans AS plan
    ON plan.id = reservation.plan_id
    AND plan.tournament_id = sqlc.arg(tournament_id)::UUID
    AND plan.roster_id = sqlc.arg(roster_id)::UUID
WHERE reservation.id = ANY(sqlc.arg(reservation_ids)::UUID[])
ORDER BY reservation.id
FOR UPDATE OF reservation, plan;

-- Reserved undisclosed rows may be released. Committed undisclosed rows must
-- remain committed evidence and are superseded instead. Disclosed rows cannot
-- match this mutation and therefore cannot be made available again.
-- name: ReleaseTournamentConfigurationEditReservations :many
WITH selected AS (
    SELECT reservation.id,
        reservation.state
    FROM task_version_reservations AS reservation
    JOIN assignment_plans AS plan
        ON plan.id = reservation.plan_id
        AND plan.tournament_id = sqlc.arg(tournament_id)::UUID
        AND plan.roster_id = sqlc.arg(roster_id)::UUID
    WHERE reservation.id = ANY(sqlc.arg(reservation_ids)::UUID[])
        AND reservation.disclosed_at IS NULL
        AND reservation.state IN ('reserved', 'committed')
    FOR UPDATE OF reservation, plan
)
UPDATE task_version_reservations AS reservation
SET revision = reservation.revision + 1,
    state = CASE selected.state
        WHEN 'reserved' THEN 'released'
        ELSE 'superseded'
    END,
    released_at = CASE selected.state
        WHEN 'reserved' THEN sqlc.arg(occurred_at)::TIMESTAMPTZ
        ELSE NULL
    END,
    release_reason = CASE selected.state
        WHEN 'reserved' THEN sqlc.arg(reason)::TEXT
        ELSE NULL
    END,
    superseded_at = CASE selected.state
        WHEN 'committed' THEN sqlc.arg(occurred_at)::TIMESTAMPTZ
        ELSE NULL
    END,
    supersession_reason = CASE selected.state
        WHEN 'committed' THEN sqlc.arg(reason)::TEXT
        ELSE NULL
    END
FROM selected
WHERE reservation.id = selected.id
RETURNING reservation.id,
    reservation.revision,
    reservation.state,
    reservation.released_at,
    reservation.superseded_at;

-- name: CreateTournamentConfigurationEditArtifactLineage :one
INSERT INTO tournament_configuration_edit_artifacts (
    lineage_id,
    command_id,
    tournament_id,
    roster_id,
    artifact_kind,
    source_artifact_id,
    source_artifact_revision,
    successor_artifact_id,
    successor_artifact_revision,
    proof_hash,
    lineage_document,
    superseded_at,
    created_at
)
VALUES (
    sqlc.arg(lineage_id)::UUID,
    sqlc.arg(command_id)::UUID,
    sqlc.arg(tournament_id)::UUID,
    sqlc.arg(roster_id)::UUID,
    sqlc.arg(artifact_kind)::VARCHAR,
    sqlc.arg(source_artifact_id)::UUID,
    sqlc.narg(source_artifact_revision)::BIGINT,
    sqlc.narg(successor_artifact_id)::UUID,
    sqlc.narg(successor_artifact_revision)::BIGINT,
    sqlc.narg(proof_hash)::BYTEA,
    sqlc.arg(lineage_document)::JSONB,
    sqlc.arg(superseded_at)::TIMESTAMPTZ,
    sqlc.arg(created_at)::TIMESTAMPTZ
)
RETURNING lineage_id,
    command_id,
    artifact_kind,
    source_artifact_id,
    successor_artifact_id,
    successor_artifact_revision,
    superseded_at,
    created_at;

-- name: CreateTournamentConfigurationEditInvalidation :one
INSERT INTO tournament_configuration_edit_invalidations (
    invalidation_id,
    command_id,
    tournament_id,
    roster_id,
    artifact_kind,
    artifact_id,
    artifact_revision,
    reason,
    created_at
)
VALUES (
    sqlc.arg(invalidation_id)::UUID,
    sqlc.arg(command_id)::UUID,
    sqlc.arg(tournament_id)::UUID,
    sqlc.arg(roster_id)::UUID,
    sqlc.arg(artifact_kind)::VARCHAR,
    sqlc.arg(artifact_id)::UUID,
    sqlc.narg(artifact_revision)::BIGINT,
    sqlc.arg(reason)::TEXT,
    sqlc.arg(created_at)::TIMESTAMPTZ
)
RETURNING invalidation_id,
    command_id,
    artifact_kind,
    artifact_id,
    artifact_revision,
    reason,
    created_at;

-- name: CreateTournamentConfigurationEditSeriesSuccessor :one
INSERT INTO series (
    id,
    tournament_id,
    roster_id,
    first_participant_id,
    second_participant_id,
    format,
    state,
    current_score_revision_id,
    revision,
    supersedes_series_id,
    content_configuration_id,
    content_configuration_revision,
    category_mode,
    effective_categories,
    created_at,
    updated_at
)
SELECT sqlc.arg(successor_series_id)::UUID,
    source.tournament_id,
    source.roster_id,
    sqlc.arg(first_participant_id)::UUID,
    sqlc.arg(second_participant_id)::UUID,
    source.format,
    sqlc.arg(successor_state)::VARCHAR,
    sqlc.narg(initial_score_revision_id)::UUID,
    1,
    source.id,
    sqlc.arg(configuration_id)::UUID,
    sqlc.arg(configuration_revision)::BIGINT,
    sqlc.arg(category_mode)::VARCHAR,
    sqlc.arg(effective_categories)::JSONB,
    sqlc.arg(created_at)::TIMESTAMPTZ,
    sqlc.arg(created_at)::TIMESTAMPTZ
FROM series AS source
WHERE source.id = sqlc.arg(source_series_id)::UUID
    AND source.tournament_id = sqlc.arg(tournament_id)::UUID
    AND source.roster_id = sqlc.arg(roster_id)::UUID
    AND source.revision = sqlc.arg(expected_series_revision)::BIGINT
    AND source.state IN ('planned', 'locked', 'draft', 'ready')
    AND source.started_at IS NULL
    AND sqlc.arg(successor_state)::VARCHAR IN ('planned', 'locked', 'draft', 'ready')
RETURNING id,
    tournament_id,
    roster_id,
    first_participant_id,
    second_participant_id,
    format,
    state,
    revision,
    supersedes_series_id,
    content_configuration_id,
    content_configuration_revision,
    category_mode,
    effective_categories,
    created_at;

-- name: GetTournamentConfigurationEditSeriesWave :one
SELECT wave.id, wave.revision, wave.state
FROM wave_series
JOIN waves AS wave ON wave.id = wave_series.wave_id
WHERE wave_series.series_id = sqlc.arg(series_id)::UUID
    AND wave_series.roster_id = sqlc.arg(roster_id)::UUID
    AND wave.tournament_id = sqlc.arg(tournament_id)::UUID
    AND wave.state = 'planned'
FOR KEY SHARE OF wave;

-- The successor is inserted first because the Series superseded_by FK is
-- restrictive and immediate. This update then closes the old identity at
-- revision + 1 while retaining all result and assignment references.
-- name: SupersedeTournamentConfigurationEditSeriesCAS :one
UPDATE series AS source
SET state = 'superseded',
    revision = source.revision + 1,
    superseded_by_series_id = sqlc.arg(successor_series_id)::UUID,
    superseded_at = sqlc.arg(superseded_at)::TIMESTAMPTZ,
    supersession_reason = sqlc.arg(reason)::TEXT,
    updated_at = sqlc.arg(superseded_at)::TIMESTAMPTZ
WHERE source.id = sqlc.arg(source_series_id)::UUID
    AND source.tournament_id = sqlc.arg(tournament_id)::UUID
    AND source.roster_id = sqlc.arg(roster_id)::UUID
    AND source.revision = sqlc.arg(expected_series_revision)::BIGINT
    AND source.state IN ('planned', 'locked', 'draft', 'ready')
    AND source.started_at IS NULL
RETURNING source.id,
    source.revision,
    source.state,
    source.superseded_by_series_id,
    source.superseded_at;

-- A manual Swiss round can be retargeted while its Wave is still planned and
-- no immutable start proof exists. Its identity remains stable.
-- name: UpdateTournamentConfigurationEditSwissRoundCAS :one
UPDATE swiss_rounds AS round
SET revision = round.revision + 1,
    pairing_inputs = sqlc.arg(pairing_inputs)::JSONB,
    content_configuration_id = sqlc.arg(configuration_id)::UUID,
    content_configuration_revision = sqlc.arg(configuration_revision)::BIGINT,
    category_mode = sqlc.arg(category_mode)::VARCHAR,
    effective_categories = sqlc.arg(effective_categories)::JSONB,
    updated_at = sqlc.arg(updated_at)::TIMESTAMPTZ
WHERE round.id = sqlc.arg(round_id)::UUID
    AND round.tournament_id = sqlc.arg(tournament_id)::UUID
    AND round.roster_id = sqlc.arg(roster_id)::UUID
    AND round.revision = sqlc.arg(expected_round_revision)::BIGINT
    AND round.generation_kind = 'manual'
    AND round.lock_revision IS NULL
    AND round.locked_at IS NULL
    AND NOT EXISTS (
        SELECT 1
        FROM swiss_round_lock_proofs AS proof
        WHERE proof.round_id = round.id
    )
    AND EXISTS (
        SELECT 1
        FROM swiss_wave_links AS link
        JOIN waves AS wave ON wave.id = link.wave_id
        WHERE link.round_id = round.id
            AND link.roster_id = round.roster_id
            AND wave.state = 'planned'
            AND wave.started_at IS NULL
    )
RETURNING round.id,
    round.tournament_id,
    round.roster_id,
    round.round_number,
    round.revision,
    round.content_configuration_id,
    round.content_configuration_revision,
    round.category_mode,
    round.effective_categories,
    round.updated_at;

-- A pre-start configuration edit changes the normalized bye pair together.
-- The old pair is an exact CAS fence; both old and new values must be either
-- NULL or non-NULL.  The round and wave predicates keep the link editable
-- only while its planned execution has no lock or start proof.
-- name: UpdateTournamentConfigurationEditSwissWaveLinkByeCAS :one
UPDATE swiss_wave_links AS link
SET bye_participant_id = sqlc.narg(next_bye_participant_id)::UUID,
    bye_revision_id = sqlc.narg(next_bye_revision_id)::UUID
FROM waves AS wave
WHERE link.wave_id = wave.id
    AND link.tournament_id = sqlc.arg(tournament_id)::UUID
    AND link.roster_id = sqlc.arg(roster_id)::UUID
    AND link.round_id = sqlc.arg(round_id)::UUID
    AND wave.tournament_id = sqlc.arg(tournament_id)::UUID
    AND wave.roster_id = sqlc.arg(roster_id)::UUID
    AND link.bye_participant_id IS NOT DISTINCT FROM sqlc.narg(expected_bye_participant_id)::UUID
    AND link.bye_revision_id IS NOT DISTINCT FROM sqlc.narg(expected_bye_revision_id)::UUID
    AND (sqlc.narg(expected_bye_participant_id)::UUID IS NULL) = (sqlc.narg(expected_bye_revision_id)::UUID IS NULL)
    AND (sqlc.narg(next_bye_participant_id)::UUID IS NULL) = (sqlc.narg(next_bye_revision_id)::UUID IS NULL)
    AND wave.state = 'planned'
    AND wave.started_at IS NULL
    AND wave.paused_at IS NULL
    AND wave.closed_at IS NULL
    AND EXISTS (
        SELECT 1
        FROM swiss_rounds AS round
        WHERE round.id = link.round_id
            AND round.tournament_id = sqlc.arg(tournament_id)::UUID
            AND round.roster_id = sqlc.arg(roster_id)::UUID
            AND round.revision = sqlc.arg(expected_round_revision)::BIGINT
            AND round.generation_kind = 'manual'
            AND round.lock_revision IS NULL
            AND round.locked_at IS NULL
    )
    AND NOT EXISTS (
        SELECT 1
        FROM swiss_round_lock_proofs AS proof
        WHERE proof.round_id = link.round_id
            AND proof.roster_id = link.roster_id
    )
RETURNING link.wave_id,
    link.tournament_id,
    link.roster_id,
    link.round_id,
    link.bye_participant_id,
    link.bye_revision_id;

-- Once an unstarted Series successor is attached, remove only the superseded
-- execution membership.  The old Series and edit lineage remain retained, but
-- runtime Wave reads cannot mistake both generations for active members.
-- name: DeleteTournamentConfigurationEditSupersededWaveSeriesCAS :one
DELETE FROM wave_series AS membership
USING waves AS wave,
    series AS source
WHERE membership.wave_id = sqlc.arg(wave_id)::UUID
    AND membership.series_id = sqlc.arg(source_series_id)::UUID
    AND wave.id = membership.wave_id
    AND wave.tournament_id = sqlc.arg(tournament_id)::UUID
    AND wave.roster_id = sqlc.arg(roster_id)::UUID
    AND wave.state = 'planned'
    AND wave.started_at IS NULL
    AND wave.paused_at IS NULL
    AND wave.closed_at IS NULL
    AND source.id = membership.series_id
    AND source.tournament_id = sqlc.arg(tournament_id)::UUID
    AND source.roster_id = sqlc.arg(roster_id)::UUID
    AND source.state = 'superseded'
    AND source.superseded_by_series_id = sqlc.arg(successor_series_id)::UUID
    AND NOT EXISTS (
        SELECT 1
        FROM wave_member_routes AS route
        WHERE route.wave_id = membership.wave_id
            AND route.series_id = membership.series_id
    )
    AND NOT EXISTS (
        SELECT 1
        FROM swiss_round_lock_proof_series AS proof_series
        WHERE proof_series.series_id = membership.series_id
            AND proof_series.roster_id = sqlc.arg(roster_id)::UUID
    )
RETURNING membership.wave_id,
    membership.series_id;

-- Wave/readiness evidence cannot be deleted. These mutations close an
-- undisclosed unstarted execution lineage so a new graph can be created.
-- name: SupersedeTournamentConfigurationEditReadyWindowCAS :one
UPDATE ready_windows AS ready_window
SET state = 'superseded'
WHERE ready_window.id = sqlc.arg(ready_window_id)::UUID
    AND ready_window.wave_id = sqlc.arg(wave_id)::UUID
    AND ready_window.roster_id = sqlc.arg(roster_id)::UUID
    AND ready_window.state IN ('open', 'expired')
RETURNING ready_window.id, ready_window.wave_id, ready_window.roster_id, ready_window.state;

-- name: SupersedeTournamentConfigurationEditWaveCAS :one
UPDATE waves AS wave
SET state = 'superseded',
    revision = wave.revision + 1,
    closed_at = sqlc.arg(superseded_at)::TIMESTAMPTZ,
    updated_at = sqlc.arg(superseded_at)::TIMESTAMPTZ
WHERE wave.id = sqlc.arg(wave_id)::UUID
    AND wave.tournament_id = sqlc.arg(tournament_id)::UUID
    AND wave.roster_id = sqlc.arg(roster_id)::UUID
    AND wave.revision = sqlc.arg(expected_wave_revision)::BIGINT
    AND wave.state IN ('planned', 'ready_window_open', 'ready', 'ready_window_expired')
    AND wave.started_at IS NULL
    AND wave.paused_at IS NULL
    AND wave.closed_at IS NULL
RETURNING wave.id, wave.tournament_id, wave.roster_id, wave.revision, wave.state, wave.closed_at;

-- An unbound readiness head can be cleared under its normal revision guard.
-- A bound head remains retained evidence and is invalidated by the immutable
-- edit record plus supersession of its ready window.
-- name: ClearTournamentConfigurationEditUnboundReadinessCAS :many
UPDATE wave_readiness AS readiness
SET ready = false,
    ready_at = NULL,
    revision = readiness.revision + 1,
    updated_at = sqlc.arg(updated_at)::TIMESTAMPTZ
WHERE readiness.wave_id = sqlc.arg(wave_id)::UUID
    AND readiness.roster_id = sqlc.arg(roster_id)::UUID
    AND readiness.revision = sqlc.arg(expected_readiness_revision)::BIGINT
    AND readiness.ready_window_id IS NULL
RETURNING readiness.wave_id,
    readiness.roster_id,
    readiness.participant_id,
    readiness.revision,
    readiness.ready,
    readiness.ready_window_id,
    readiness.updated_at;

-- name: ListTournamentConfigurationEditLineage :many
SELECT lineage_id,
    command_id,
    tournament_id,
    roster_id,
    artifact_kind,
    source_artifact_id,
    source_artifact_revision,
    successor_artifact_id,
    successor_artifact_revision,
    proof_hash,
    lineage_document,
    superseded_at,
    created_at
FROM tournament_configuration_edit_artifacts
WHERE tournament_id = sqlc.arg(tournament_id)::UUID
    AND roster_id = sqlc.arg(roster_id)::UUID
ORDER BY created_at, lineage_id;

-- name: ListTournamentConfigurationEditInvalidations :many
SELECT invalidation_id,
    command_id,
    tournament_id,
    roster_id,
    artifact_kind,
    artifact_id,
    artifact_revision,
    reason,
    created_at
FROM tournament_configuration_edit_invalidations
WHERE tournament_id = sqlc.arg(tournament_id)::UUID
    AND roster_id = sqlc.arg(roster_id)::UUID
ORDER BY created_at, invalidation_id;
