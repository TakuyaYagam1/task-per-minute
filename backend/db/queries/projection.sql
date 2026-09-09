-- Result/execution writers acquire this prefix before any projection or child
-- lock. Materialized dependencies enforce Tournament -> Roster -> published
-- Projection independently of join planning. Before initial publication, the
-- same Tournament/Roster prefix is held even though no projection exists yet.
-- name: LockTournamentResultScope :one
WITH locked_tournament AS MATERIALIZED (
    SELECT tournament.id
    FROM tournaments AS tournament
    WHERE tournament.id = sqlc.arg(tournament_id)
    FOR UPDATE OF tournament
), locked_roster AS MATERIALIZED (
    SELECT roster.id, roster.tournament_id
    FROM locked_tournament
    JOIN rosters AS roster ON roster.tournament_id = locked_tournament.id
    WHERE sqlc.narg(roster_id)::uuid IS NULL OR roster.id = sqlc.narg(roster_id)::uuid
    FOR UPDATE OF roster
), locked_projection AS MATERIALIZED (
    SELECT projection.id
    FROM locked_roster
    JOIN projection_revisions AS projection ON projection.roster_id = locked_roster.id
        AND projection.tournament_id = locked_roster.tournament_id AND projection.state = 'published'
    ORDER BY projection.revision_number, projection.id
    FOR UPDATE OF projection
)
SELECT locked_roster.id, locked_projection.id AS projection_revision_id
FROM locked_roster
LEFT JOIN locked_projection ON TRUE;

-- name: LockProjectionRoster :one
SELECT id,
    tournament_id,
    revision,
    locked_at,
    execution_started_at,
    created_at,
    updated_at
FROM rosters
WHERE id = sqlc.arg(roster_id)
    AND tournament_id = sqlc.arg(tournament_id)
FOR UPDATE;

-- name: LockTerminalProjectionCommit :many
SELECT id, command_id, 'normal_no_show_commit'::text AS source_kind,
    series_score_revision_id, series_result_revision_id, result_event_id, resolved_at
FROM normal_no_show_commits AS no_show
WHERE no_show.tournament_id = sqlc.arg(tournament_id)::uuid AND no_show.roster_id = sqlc.arg(roster_id)::uuid
    AND no_show.series_id = sqlc.arg(series_id)::uuid AND no_show.projection_evidence_id = sqlc.arg(projection_revision_id)::uuid
UNION ALL
SELECT id, command_id, 'operator_forfeit_commit',
    series_score_revision_id, series_result_revision_id, result_event_id, resolved_at
FROM operator_forfeit_commits AS forfeit
WHERE forfeit.tournament_id = sqlc.arg(tournament_id)::uuid AND forfeit.roster_id = sqlc.arg(roster_id)::uuid
    AND forfeit.series_id = sqlc.arg(series_id)::uuid AND forfeit.projection_evidence_id = sqlc.arg(projection_revision_id)::uuid
UNION ALL
SELECT commit.id, commit.idempotency_key, 'result_commit',
    commit.series_score_revision_id, commit.series_result_revision_id, commit.result_event_id, commit.created_at
FROM result_commits AS commit
JOIN result_events AS event ON event.id = commit.result_event_id
    AND event.tournament_id = commit.tournament_id AND event.roster_id = commit.roster_id
    AND event.series_id = commit.series_id AND event.attempt_id = commit.attempt_id
JOIN audit_events AS audit ON audit.id = commit.audit_event_id AND audit.result_event_id = event.id
    AND audit.tournament_id = commit.tournament_id AND audit.roster_id = commit.roster_id
    AND audit.series_id = commit.series_id
WHERE commit.tournament_id = sqlc.arg(tournament_id)::uuid AND commit.roster_id = sqlc.arg(roster_id)::uuid
    AND commit.series_id = sqlc.arg(series_id)::uuid AND commit.projection_evidence_id = sqlc.arg(projection_revision_id)::uuid
    AND commit.series_result_revision_id IS NOT NULL
    AND audit.actor_kind = 'operator' AND event.result_reason = 'operator_forfeit';

-- name: CreateTerminalProjectionAuthority :exec
INSERT INTO result_projection_node_authorities
    (id, tournament_id, roster_id, source_kind, normal_no_show_commit_id, operator_forfeit_commit_id, created_at)
VALUES (sqlc.arg(id), sqlc.arg(tournament_id), sqlc.arg(roster_id), sqlc.arg(source_kind),
    sqlc.narg(normal_no_show_commit_id)::uuid, sqlc.narg(operator_forfeit_commit_id)::uuid, sqlc.arg(created_at));

-- name: LockTerminalProjectionRevisions :many
SELECT result.id, CASE result.entity_kind WHEN 'series' THEN 'series_result' ELSE 'game_result' END::text AS artifact_kind,
    result.entity_id, result.revision_number, result.previous_revision_id, result.created_at
FROM official_result_revisions AS result
WHERE result.tournament_id = sqlc.arg(tournament_id) AND result.roster_id = sqlc.arg(roster_id)
    AND result.series_id = sqlc.arg(series_id) AND result.result_event_id = sqlc.arg(result_event_id)
UNION ALL
SELECT score.id, 'series_score', score.series_id, score.revision_number, score.previous_revision_id, score.created_at
FROM series_score_revisions AS score
WHERE score.tournament_id = sqlc.arg(tournament_id) AND score.roster_id = sqlc.arg(roster_id)
    AND score.series_id = sqlc.arg(series_id) AND score.result_event_id = sqlc.arg(result_event_id);

-- name: LockTerminalSwissPointSource :many
SELECT proof.round_id, proof.round_number, series.first_participant_id, series.second_participant_id,
    series.winner_id, series.current_result_revision_id
FROM swiss_round_lock_proofs AS proof
JOIN swiss_round_lock_proof_series AS member ON member.round_id = proof.round_id AND member.roster_id = proof.roster_id
JOIN series ON series.id = member.series_id AND series.tournament_id = proof.tournament_id AND series.roster_id = proof.roster_id
WHERE proof.tournament_id = sqlc.arg(tournament_id) AND proof.roster_id = sqlc.arg(roster_id)
    AND series.id = sqlc.arg(series_id) AND series.current_result_revision_id = sqlc.arg(result_revision_id)
    AND series.state IN ('completed', 'cancelled')
FOR UPDATE OF proof, member, series;

-- name: CreateWaveDisclosureOutboxEvent :one
WITH locked_idempotency AS MATERIALIZED (
    SELECT pg_advisory_xact_lock(
        hashtextextended(sqlc.arg(idempotency_key)::TEXT, 0)
    )
),
existing AS MATERIALIZED (
    SELECT outbox_event.id,
        outbox_event.tournament_id,
        outbox_event.roster_id,
        outbox_event.projection_revision_id,
        outbox_event.projection_revision,
        outbox_event.sequence,
        outbox_event.projection_ordinal,
        outbox_event.terminal,
        outbox_event.idempotency_key,
        outbox_event.audience,
        outbox_event.principal_id,
        outbox_event.topic,
        outbox_event.payload,
        outbox_event.created_at,
        outbox_event.available_at,
        outbox_event.claimed_by,
        outbox_event.claim_token,
        outbox_event.claimed_until,
        outbox_event.attempt_count,
        outbox_event.last_error,
        outbox_event.published_at
    FROM locked_idempotency
    CROSS JOIN outbox_events AS outbox_event
    WHERE outbox_event.idempotency_key = sqlc.arg(idempotency_key)
),
locked_wave AS MATERIALIZED (
    SELECT wave.id,
        wave.tournament_id,
        wave.roster_id,
        wave.revision_id,
        wave.revision
    FROM waves AS wave
    WHERE wave.id = sqlc.arg(wave_id)
        AND wave.tournament_id = sqlc.arg(tournament_id)
        AND wave.roster_id = sqlc.arg(roster_id)
        AND wave.revision_id = sqlc.arg(expected_wave_revision_id)
        AND wave.revision = sqlc.arg(expected_wave_revision)
        AND wave.state = 'active'
    FOR UPDATE
),
published_projection AS MATERIALIZED (
    SELECT projection_revision.id,
        projection_revision.tournament_id,
        projection_revision.roster_id,
        projection_revision.revision_number
    FROM projection_revisions AS projection_revision
    INNER JOIN locked_wave
        ON locked_wave.tournament_id = projection_revision.tournament_id
        AND locked_wave.roster_id = projection_revision.roster_id
    WHERE projection_revision.state = 'published'
    FOR SHARE
),
allocated_sequence AS (
    INSERT INTO tournament_outbox_cursors (
        tournament_id,
        next_sequence,
        updated_at
    )
    SELECT locked_wave.tournament_id,
        2,
        sqlc.arg(created_at)
    FROM locked_wave
    INNER JOIN published_projection
        ON published_projection.tournament_id = locked_wave.tournament_id
        AND published_projection.roster_id = locked_wave.roster_id
    WHERE NOT EXISTS (SELECT 1 FROM existing)
    ON CONFLICT (tournament_id) DO UPDATE
    SET next_sequence = tournament_outbox_cursors.next_sequence + 1,
        updated_at = EXCLUDED.updated_at
    RETURNING next_sequence - 1 AS sequence
),
allocated_ordinal AS (
    INSERT INTO projection_outbox_cursors (
        projection_revision_id,
        tournament_id,
        roster_id,
        next_ordinal,
        updated_at
    )
    SELECT published_projection.id,
        published_projection.tournament_id,
        published_projection.roster_id,
        2,
        sqlc.arg(created_at)
    FROM published_projection
    INNER JOIN allocated_sequence ON true
    ON CONFLICT (projection_revision_id) DO UPDATE
    SET next_ordinal = projection_outbox_cursors.next_ordinal + 1,
        updated_at = EXCLUDED.updated_at
    WHERE projection_outbox_cursors.next_ordinal < 32768
    RETURNING next_ordinal - 1 AS projection_ordinal
),
inserted AS (
    INSERT INTO outbox_events (
        id,
        tournament_id,
        roster_id,
        projection_revision_id,
        projection_revision,
        sequence,
        projection_ordinal,
        terminal,
        idempotency_key,
        audience,
        principal_id,
        topic,
        payload,
        created_at,
        available_at
    )
    SELECT sqlc.arg(id),
        locked_wave.tournament_id,
        locked_wave.roster_id,
        published_projection.id,
        published_projection.revision_number,
        allocated_sequence.sequence,
        allocated_ordinal.projection_ordinal,
        false,
        sqlc.arg(idempotency_key),
        sqlc.arg(audience),
        sqlc.arg(principal_id),
        'wave.disclosed',
        sqlc.arg(payload),
        sqlc.arg(created_at),
        sqlc.arg(created_at)
    FROM locked_wave
    JOIN allocated_sequence ON true
    JOIN published_projection ON true
    JOIN allocated_ordinal ON true
    ON CONFLICT (idempotency_key) DO NOTHING
    RETURNING id,
        tournament_id,
        roster_id,
        projection_revision_id,
        projection_revision,
        sequence,
        projection_ordinal,
        terminal,
        idempotency_key,
        audience,
        principal_id,
        topic,
        payload,
        created_at,
        available_at,
        claimed_by,
        claim_token,
        claimed_until,
        attempt_count,
    last_error,
    published_at
),
resolved AS MATERIALIZED (
    SELECT inserted.id,
        inserted.tournament_id,
        inserted.roster_id,
        inserted.projection_revision_id,
        inserted.projection_revision,
        inserted.sequence,
        inserted.projection_ordinal,
        inserted.terminal,
        inserted.idempotency_key,
        inserted.audience,
        inserted.principal_id,
        inserted.topic,
        inserted.payload,
        inserted.created_at,
        inserted.available_at,
        inserted.claimed_by,
        inserted.claim_token,
        inserted.claimed_until,
        inserted.attempt_count,
        inserted.last_error,
        inserted.published_at
    FROM inserted
    UNION ALL
    SELECT existing.id,
        existing.tournament_id,
        existing.roster_id,
        existing.projection_revision_id,
        existing.projection_revision,
        existing.sequence,
        existing.projection_ordinal,
        existing.terminal,
        existing.idempotency_key,
        existing.audience,
        existing.principal_id,
        existing.topic,
        existing.payload,
        existing.created_at,
        existing.available_at,
        existing.claimed_by,
        existing.claim_token,
        existing.claimed_until,
        existing.attempt_count,
        existing.last_error,
        existing.published_at
    FROM existing
),
inserted_source AS (
    INSERT INTO outbox_wave_sources (
        outbox_event_id,
        tournament_id,
        roster_id,
        wave_id,
        wave_revision_id,
        wave_revision,
        projection_revision_id,
        projection_revision,
        projection_ordinal,
        created_at
    )
    SELECT inserted.id AS outbox_event_id,
        locked_wave.tournament_id AS source_tournament_id,
        locked_wave.roster_id AS source_roster_id,
        locked_wave.id AS source_wave_id,
        locked_wave.revision_id AS source_wave_revision_id,
        locked_wave.revision AS source_wave_revision,
        published_projection.id AS source_projection_revision_id,
        published_projection.revision_number AS source_projection_revision,
        inserted.projection_ordinal AS source_projection_ordinal,
        inserted.created_at AS source_created_at
    FROM inserted
    JOIN locked_wave ON true
    JOIN published_projection ON true
    ON CONFLICT (outbox_event_id) DO NOTHING
    RETURNING outbox_event_id,
        tournament_id,
        roster_id,
        wave_id,
        wave_revision_id,
        wave_revision,
        projection_revision_id,
        projection_revision,
        projection_ordinal
),
verified_source AS (
    SELECT inserted_source.outbox_event_id,
        inserted_source.projection_ordinal
    FROM inserted_source
    WHERE inserted_source.tournament_id = sqlc.arg(tournament_id)
        AND inserted_source.roster_id = sqlc.arg(roster_id)
        AND inserted_source.wave_id = sqlc.arg(wave_id)
        AND inserted_source.wave_revision_id = sqlc.arg(expected_wave_revision_id)
        AND inserted_source.wave_revision = sqlc.arg(expected_wave_revision)
    UNION ALL
    SELECT outbox_source.outbox_event_id,
        outbox_source.projection_ordinal
    FROM outbox_wave_sources AS outbox_source
    WHERE NOT EXISTS (
            SELECT 1
            FROM inserted_source
            WHERE inserted_source.outbox_event_id = outbox_source.outbox_event_id
        )
        AND outbox_source.tournament_id = sqlc.arg(tournament_id)
        AND outbox_source.roster_id = sqlc.arg(roster_id)
        AND outbox_source.wave_id = sqlc.arg(wave_id)
        AND outbox_source.wave_revision_id = sqlc.arg(expected_wave_revision_id)
        AND outbox_source.wave_revision = sqlc.arg(expected_wave_revision)
)
SELECT outbox_event.id,
    outbox_event.tournament_id,
    outbox_event.roster_id,
    outbox_event.projection_revision_id,
    outbox_event.projection_revision,
    outbox_event.sequence,
    outbox_event.projection_ordinal,
    outbox_event.terminal,
    outbox_event.idempotency_key,
    outbox_event.audience,
    outbox_event.principal_id,
    outbox_event.topic,
    outbox_event.payload,
    outbox_event.created_at,
    outbox_event.available_at,
    outbox_event.claimed_by,
    outbox_event.claim_token,
    outbox_event.claimed_until,
    outbox_event.attempt_count,
    outbox_event.last_error,
    outbox_event.published_at
FROM resolved AS outbox_event
INNER JOIN verified_source
    ON verified_source.outbox_event_id = outbox_event.id
    AND verified_source.projection_ordinal = outbox_event.projection_ordinal
WHERE outbox_event.tournament_id = sqlc.arg(tournament_id)
    AND outbox_event.roster_id = sqlc.arg(roster_id)
    AND outbox_event.projection_revision_id IS NOT NULL
    AND outbox_event.projection_revision >= 1
    AND NOT outbox_event.terminal
    AND outbox_event.audience = sqlc.arg(audience)
    AND outbox_event.principal_id IS NOT DISTINCT FROM sqlc.arg(principal_id)::UUID
    AND outbox_event.topic = 'wave.disclosed'
    AND outbox_event.payload = sqlc.arg(payload)::JSONB;

-- Stage genesis separates the logical node ID from the score revision ID.
-- Resolve only immutable, exact command/Series evidence, never mutable heads.
-- name: LockStageScoreGenesisNodes :many
SELECT node.id, node.tournament_id, node.roster_id, node.entity_id,
    node.revision_number, score.id AS score_revision_id
FROM series_score_revisions AS score
JOIN tournament_stage_playoff_evidence AS stage
    ON stage.command_id = score.command_id
    AND stage.tournament_id = score.tournament_id AND stage.roster_id = score.roster_id
JOIN result_projection_node_authorities AS authority
    ON authority.stage_command_id = stage.command_id
    AND authority.tournament_id = stage.tournament_id AND authority.roster_id = stage.roster_id
    AND authority.source_kind = 'stage_initialization'
JOIN result_projection_nodes AS node
    ON node.authority_id = authority.id
    AND node.tournament_id = score.tournament_id AND node.roster_id = score.roster_id
    AND node.entity_id = score.series_id AND node.artifact_kind = 'series_score'
    AND node.revision_number = score.revision_number AND node.previous_node_id IS NULL
    AND node.payload ->> 'schema' = 'result-projection-series-score-genesis-v1'
    AND node.payload ->> 'revision_id' = score.id::TEXT
    AND node.payload ->> 'stage_command_id' = stage.command_id::TEXT
    AND node.payload ->> 'series_id' = score.series_id::TEXT
WHERE score.id = sqlc.arg(score_revision_id)::UUID
    AND score.tournament_id = sqlc.arg(tournament_id)::UUID
    AND score.roster_id = sqlc.arg(roster_id)::UUID
    AND score.series_id = sqlc.arg(series_id)::UUID
    AND score.revision_number = 1 AND score.operation = 'initialize'
    AND score.previous_revision_id IS NULL AND score.result_event_id IS NULL
    AND score.first_participant_wins = 0 AND score.second_participant_wins = 0
    AND score.source_projection_revision_id = stage.source_projection_revision_id
    AND score.source_projection_revision = stage.source_projection_revision
    AND score.series_id IN (stage.first_semifinal_series_id, stage.second_semifinal_series_id)
FOR KEY SHARE OF score, stage, authority, node;

-- name: CreateFinalChampionOutboxEvent :one
WITH locked_idempotency AS MATERIALIZED (
    SELECT pg_advisory_xact_lock(
        hashtextextended(sqlc.arg(idempotency_key)::UUID::TEXT, 0)
    )
),
existing AS MATERIALIZED (
    SELECT outbox_event.id,
        outbox_event.tournament_id,
        outbox_event.roster_id,
        outbox_event.projection_revision_id,
        outbox_event.projection_revision,
        outbox_event.sequence,
        outbox_event.projection_ordinal,
        outbox_event.terminal,
        outbox_event.idempotency_key,
        outbox_event.audience,
        outbox_event.principal_id,
        outbox_event.topic,
        outbox_event.payload,
        outbox_event.created_at,
        outbox_event.available_at,
        outbox_event.claimed_by,
        outbox_event.claim_token,
        outbox_event.claimed_until,
        outbox_event.attempt_count,
        outbox_event.last_error,
        outbox_event.published_at
    FROM locked_idempotency
    CROSS JOIN outbox_events AS outbox_event
    WHERE outbox_event.idempotency_key = sqlc.arg(idempotency_key)::UUID
),
target_projection AS MATERIALIZED (
    SELECT projection_revision.id,
        projection_revision.tournament_id,
        projection_revision.roster_id,
        projection_revision.revision_number
    FROM projection_revisions AS projection_revision
    WHERE projection_revision.id = sqlc.arg(projection_revision_id)::UUID
        AND projection_revision.tournament_id = sqlc.arg(tournament_id)
        AND projection_revision.roster_id = sqlc.arg(roster_id)
        AND projection_revision.revision_number = sqlc.arg(projection_revision)::BIGINT
        AND projection_revision.state IN ('published', 'superseded')
    FOR SHARE
),
allocated_sequence AS (
    INSERT INTO tournament_outbox_cursors (
        tournament_id,
        next_sequence,
        updated_at
    )
    SELECT target_projection.tournament_id,
        2,
        sqlc.arg(created_at)
    FROM target_projection
    WHERE NOT EXISTS (SELECT 1 FROM existing)
    ON CONFLICT (tournament_id) DO UPDATE
    SET next_sequence = tournament_outbox_cursors.next_sequence + 1,
        updated_at = EXCLUDED.updated_at
    RETURNING next_sequence - 1 AS sequence
),
allocated_ordinal AS (
    INSERT INTO projection_outbox_cursors (
        projection_revision_id,
        tournament_id,
        roster_id,
        next_ordinal,
        updated_at
    )
    SELECT target_projection.id,
        target_projection.tournament_id,
        target_projection.roster_id,
        2,
        sqlc.arg(created_at)
    FROM target_projection
    INNER JOIN allocated_sequence ON true
    ON CONFLICT (projection_revision_id) DO UPDATE
    SET next_ordinal = projection_outbox_cursors.next_ordinal + 1,
        updated_at = EXCLUDED.updated_at
    WHERE projection_outbox_cursors.tournament_id = EXCLUDED.tournament_id
        AND projection_outbox_cursors.roster_id = EXCLUDED.roster_id
        AND projection_outbox_cursors.next_ordinal < 32768
    RETURNING next_ordinal - 1 AS projection_ordinal
),
inserted AS (
    INSERT INTO outbox_events (
        id,
        tournament_id,
        roster_id,
        projection_revision_id,
        projection_revision,
        sequence,
        projection_ordinal,
        terminal,
        idempotency_key,
        audience,
        topic,
        payload,
        created_at,
        available_at
    )
    SELECT sqlc.arg(id),
        target_projection.tournament_id,
        target_projection.roster_id,
        target_projection.id,
        target_projection.revision_number,
        allocated_sequence.sequence,
        allocated_ordinal.projection_ordinal::SMALLINT,
        true,
        sqlc.arg(idempotency_key)::UUID,
        'all',
        'tournament.champion.published',
        sqlc.arg(payload),
        sqlc.arg(created_at),
        sqlc.arg(created_at)
    FROM target_projection
    INNER JOIN allocated_sequence ON true
    INNER JOIN allocated_ordinal ON true
    ON CONFLICT (idempotency_key) DO NOTHING
    RETURNING id,
        tournament_id,
        roster_id,
        projection_revision_id,
        projection_revision,
        sequence,
        projection_ordinal,
        terminal,
        idempotency_key,
        audience,
        principal_id,
        topic,
        payload,
        created_at,
        available_at,
        claimed_by,
        claim_token,
        claimed_until,
        attempt_count,
        last_error,
        published_at
),
resolved AS MATERIALIZED (
    SELECT inserted.id,
        inserted.tournament_id,
        inserted.roster_id,
        inserted.projection_revision_id,
        inserted.projection_revision,
        inserted.sequence,
        inserted.projection_ordinal,
        inserted.terminal,
        inserted.idempotency_key,
        inserted.audience,
        inserted.principal_id,
        inserted.topic,
        inserted.payload,
        inserted.created_at,
        inserted.available_at,
        inserted.claimed_by,
        inserted.claim_token,
        inserted.claimed_until,
        inserted.attempt_count,
        inserted.last_error,
        inserted.published_at
    FROM inserted
    UNION ALL
    SELECT existing.id,
        existing.tournament_id,
        existing.roster_id,
        existing.projection_revision_id,
        existing.projection_revision,
        existing.sequence,
        existing.projection_ordinal,
        existing.terminal,
        existing.idempotency_key,
        existing.audience,
        existing.principal_id,
        existing.topic,
        existing.payload,
        existing.created_at,
        existing.available_at,
        existing.claimed_by,
        existing.claim_token,
        existing.claimed_until,
        existing.attempt_count,
        existing.last_error,
        existing.published_at
    FROM existing
),
source AS (
    INSERT INTO outbox_champion_sources (
        outbox_event_id,
        tournament_id,
        roster_id,
        final_series_id,
        final_result_revision_id,
        champion_artifact_id,
        projection_revision_id,
        projection_revision,
        projection_ordinal,
        created_at
    )
    SELECT resolved.id,
        resolved.tournament_id,
        resolved.roster_id,
        sqlc.arg(final_series_id),
        sqlc.arg(final_result_revision_id),
        sqlc.arg(champion_artifact_id),
        resolved.projection_revision_id,
        resolved.projection_revision,
        resolved.projection_ordinal,
        resolved.created_at
    FROM resolved
    ON CONFLICT (outbox_event_id) DO NOTHING
    RETURNING outbox_event_id,
        tournament_id,
        roster_id,
        final_series_id,
        final_result_revision_id,
        champion_artifact_id,
        projection_revision_id,
        projection_revision,
        projection_ordinal
),
verified_source AS (
    SELECT source.outbox_event_id,
        source.projection_ordinal
    FROM source
    WHERE source.tournament_id = sqlc.arg(tournament_id)
        AND source.roster_id = sqlc.arg(roster_id)
        AND source.final_series_id = sqlc.arg(final_series_id)
        AND source.final_result_revision_id = sqlc.arg(final_result_revision_id)
        AND source.champion_artifact_id = sqlc.arg(champion_artifact_id)
        AND source.projection_revision_id = sqlc.arg(projection_revision_id)::UUID
        AND source.projection_revision = sqlc.arg(projection_revision)::BIGINT
    UNION ALL
    SELECT champion_source.outbox_event_id,
        champion_source.projection_ordinal
    FROM outbox_champion_sources AS champion_source
    WHERE NOT EXISTS (
            SELECT 1
            FROM source
            WHERE source.outbox_event_id = champion_source.outbox_event_id
        )
        AND champion_source.tournament_id = sqlc.arg(tournament_id)
        AND champion_source.roster_id = sqlc.arg(roster_id)
        AND champion_source.final_series_id = sqlc.arg(final_series_id)
        AND champion_source.final_result_revision_id = sqlc.arg(final_result_revision_id)
        AND champion_source.champion_artifact_id = sqlc.arg(champion_artifact_id)
        AND champion_source.projection_revision_id = sqlc.arg(projection_revision_id)::UUID
        AND champion_source.projection_revision = sqlc.arg(projection_revision)::BIGINT
)
SELECT outbox_event.id,
    outbox_event.tournament_id,
    outbox_event.roster_id,
    outbox_event.projection_revision_id,
    outbox_event.projection_revision,
    outbox_event.sequence,
    outbox_event.projection_ordinal,
    outbox_event.terminal,
    outbox_event.idempotency_key,
    outbox_event.audience,
    outbox_event.principal_id,
    outbox_event.topic,
    outbox_event.payload,
    outbox_event.created_at,
    outbox_event.available_at,
    outbox_event.claimed_by,
    outbox_event.claim_token,
    outbox_event.claimed_until,
    outbox_event.attempt_count,
    outbox_event.last_error,
    outbox_event.published_at
FROM resolved AS outbox_event
INNER JOIN verified_source
    ON verified_source.outbox_event_id = outbox_event.id
    AND verified_source.projection_ordinal = outbox_event.projection_ordinal
WHERE outbox_event.tournament_id = sqlc.arg(tournament_id)
    AND outbox_event.roster_id = sqlc.arg(roster_id)
    AND outbox_event.projection_revision_id = sqlc.arg(projection_revision_id)::UUID
    AND outbox_event.projection_revision = sqlc.arg(projection_revision)::BIGINT
    AND outbox_event.terminal
    AND outbox_event.audience = 'all'
    AND outbox_event.principal_id IS NULL
    AND outbox_event.topic = 'tournament.champion.published'
    AND outbox_event.payload = sqlc.arg(payload)::JSONB;

-- name: GetLatestProjectionCutoff :one
SELECT id,
    tournament_id,
    roster_id,
    sequence_number,
    previous_cutoff_id,
    source_kind,
    official_result_revision_id,
    golden_position_commit_id,
    stage_progression_command_id,
    reason,
    cutoff_at,
    created_at
FROM projection_cutoffs
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY sequence_number DESC
LIMIT 1;

-- name: CreateProjectionCutoff :one
INSERT INTO projection_cutoffs (
    id,
    tournament_id,
    roster_id,
    sequence_number,
    previous_cutoff_id,
    source_kind,
    official_result_revision_id,
    golden_position_commit_id,
    stage_progression_command_id,
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
    sqlc.arg(stage_progression_command_id),
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
    stage_progression_command_id,
    reason,
    cutoff_at,
    created_at;

-- name: CreateProjectionRevision :one
INSERT INTO projection_revisions (
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

-- name: CreateProjectionArtifact :one
INSERT INTO projection_artifacts (
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

-- name: CreateProjectionArtifactMember :one
INSERT INTO projection_artifact_members (
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

-- name: CreateProjectionDependency :one
INSERT INTO projection_dependencies (
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

-- name: LinkProjectionArtifact :one
INSERT INTO projection_revision_artifacts (
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

-- name: LockProjectionRevisionSet :many
SELECT id
FROM projection_revisions
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY revision_number
FOR UPDATE;

-- name: SupersedeProjectionRevisionCAS :one
UPDATE projection_revisions
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

-- name: PublishProjectionRevisionCAS :one
UPDATE projection_revisions
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

-- name: GetProjectionRevisionScoped :one
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
FROM projection_revisions
WHERE id = sqlc.arg(id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id);

-- name: GetCurrentProjectionRevision :one
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
FROM projection_revisions
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND state = 'published';

-- name: GetProjectionCutoffByID :one
SELECT id,
    tournament_id,
    roster_id,
    sequence_number,
    previous_cutoff_id,
    source_kind,
    official_result_revision_id,
    golden_position_commit_id,
    stage_progression_command_id,
    reason,
    cutoff_at,
    created_at
FROM projection_cutoffs
WHERE id = sqlc.arg(id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id);

-- name: ListProjectionRevisionArtifacts :many
SELECT revision_id,
    tournament_id,
    roster_id,
    artifact_kind,
    artifact_id,
    change_kind,
    created_at
FROM projection_revision_artifacts
WHERE revision_id = sqlc.arg(revision_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY artifact_kind;

-- name: GetProjectionArtifactScoped :one
SELECT id,
    tournament_id,
    roster_id,
    produced_by_revision_id,
    artifact_kind,
    artifact_key,
    payload,
    payload_digest,
    created_at
FROM projection_artifacts
WHERE id = sqlc.arg(id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id);

-- name: LockFinalPublicationArtifactMembership :many
SELECT membership.revision_id,
    membership.tournament_id,
    membership.roster_id,
    membership.artifact_id,
    membership.artifact_kind,
    membership.change_kind,
    artifact.produced_by_revision_id,
    producer.revision_number AS producer_revision,
    artifact.payload_digest
FROM projection_revision_artifacts AS membership
JOIN projection_artifacts AS artifact
    ON artifact.id = membership.artifact_id
    AND artifact.tournament_id = membership.tournament_id
    AND artifact.roster_id = membership.roster_id
    AND artifact.artifact_kind = membership.artifact_kind
JOIN projection_revisions AS producer
    ON producer.id = artifact.produced_by_revision_id
    AND producer.tournament_id = membership.tournament_id
    AND producer.roster_id = membership.roster_id
WHERE membership.revision_id = sqlc.arg(revision_id)
    AND membership.tournament_id = sqlc.arg(tournament_id)
    AND membership.roster_id = sqlc.arg(roster_id)
ORDER BY membership.artifact_kind
FOR KEY SHARE OF membership, artifact, producer;

-- name: ListProjectionArtifactMembers :many
SELECT artifact_id,
    tournament_id,
    roster_id,
    artifact_kind,
    participant_id,
    position,
    score,
    created_at
FROM projection_artifact_members
WHERE artifact_id = sqlc.arg(artifact_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY position;

-- name: ListProjectionArtifactDependencies :many
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
FROM projection_dependencies
WHERE artifact_id = sqlc.arg(artifact_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY created_at, id;

-- name: ListProjectionRevisions :many
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
FROM projection_revisions
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
ORDER BY revision_number;

-- name: GetCurrentStandingsArtifact :one
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
    AND revision_artifact.artifact_kind = 'standings';

-- name: LockFinalProjectionAggregate :one
SELECT tournament.state AS tournament_state,
    tournament.revision AS tournament_revision,
    final_series.format AS series_format,
    final_series.state AS series_state,
    final_series.first_participant_id,
    final_series.second_participant_id,
    final_series.first_participant_wins,
    final_series.second_participant_wins,
    final_series.winner_id,
    final_series.current_score_revision_id,
    final_series.current_result_revision_id,
    final_series.revision AS series_revision
FROM tournaments AS tournament
INNER JOIN rosters AS roster
    ON roster.tournament_id = tournament.id
INNER JOIN series AS final_series
    ON final_series.tournament_id = tournament.id
    AND final_series.roster_id = roster.id
INNER JOIN tournament_stage_playoff_finals AS stage
    ON stage.tournament_id = tournament.id
    AND stage.roster_id = roster.id
    AND stage.final_series_id = final_series.id
WHERE tournament.id = sqlc.arg(tournament_id)
    AND roster.id = sqlc.arg(roster_id)
    AND final_series.id = sqlc.arg(series_id)
FOR UPDATE OF tournament, roster, final_series, stage;

-- name: LockFinalProjectionAttempts :many
SELECT slot.slot_number,
    attempt.id AS attempt_id,
    attempt.attempt_number,
    attempt.state AS attempt_state,
    attempt.result_reason AS attempt_result_reason,
    attempt.winner_id AS attempt_winner_id,
    attempt.result_revision_id AS attempt_result_revision_id,
    attempt.revision AS attempt_revision,
    result_head.current_revision_id AS head_revision_id,
    result_head.revision AS head_revision,
    result_revision.revision_number AS result_revision_number,
    result_revision.result_event_id,
    result_revision.result_state,
    result_revision.result_reason,
    result_revision.winner_id AS result_winner_id
FROM game_slots AS slot
INNER JOIN game_attempts AS attempt
    ON attempt.slot_id = slot.id
    AND attempt.series_id = slot.series_id
    AND attempt.roster_id = slot.roster_id
INNER JOIN official_result_heads AS result_head
    ON result_head.entity_kind = 'game_attempt'
    AND result_head.entity_id = attempt.id
    AND result_head.series_id = attempt.series_id
    AND result_head.roster_id = attempt.roster_id
INNER JOIN official_result_revisions AS result_revision
    ON result_revision.id = result_head.current_revision_id
    AND result_revision.entity_kind = result_head.entity_kind
    AND result_revision.entity_id = result_head.entity_id
    AND result_revision.series_id = result_head.series_id
    AND result_revision.roster_id = result_head.roster_id
WHERE slot.series_id = sqlc.arg(series_id)
    AND slot.roster_id = sqlc.arg(roster_id)
ORDER BY slot.slot_number,
    attempt.attempt_number,
    attempt.id
FOR UPDATE OF attempt, result_head;

-- name: LockFinalProjectionSeriesResultHead :one
SELECT result_head.current_revision_id,
    result_head.revision AS head_revision,
    result_revision.result_event_id,
    result_revision.result_state,
    result_revision.result_reason,
    result_revision.winner_id,
    result_revision.revision_number
FROM official_result_heads AS result_head
INNER JOIN official_result_revisions AS result_revision
    ON result_revision.id = result_head.current_revision_id
    AND result_revision.entity_kind = result_head.entity_kind
    AND result_revision.entity_id = result_head.entity_id
    AND result_revision.series_id = result_head.series_id
    AND result_revision.roster_id = result_head.roster_id
WHERE result_head.entity_kind = 'series'
    AND result_head.entity_id = sqlc.arg(series_id)
    AND result_head.series_id = sqlc.arg(series_id)
    AND result_head.roster_id = sqlc.arg(roster_id)
FOR UPDATE OF result_head;

-- name: LockFinalProjectionScoreHead :one
SELECT score_head.current_revision_id,
    score_head.revision AS head_revision,
    score_revision.result_event_id,
    score_revision.previous_revision_id,
    score_revision.revision_number,
    score_revision.first_participant_wins,
    score_revision.second_participant_wins
FROM series_score_heads AS score_head
INNER JOIN series_score_revisions AS score_revision
    ON score_revision.id = score_head.current_revision_id
    AND score_revision.series_id = score_head.series_id
    AND score_revision.roster_id = score_head.roster_id
WHERE score_head.series_id = sqlc.arg(series_id)
    AND score_head.roster_id = sqlc.arg(roster_id)
FOR UPDATE OF score_head;

-- name: LockFinalProjectionResultCommits :many
SELECT result_commit.id AS commit_id,
    result_commit.result_event_id,
    result_commit.attempt_id,
    result_commit.game_result_revision_id,
    result_commit.series_score_revision_id,
    result_commit.series_result_revision_id,
    result_commit.outbox_event_id,
    result_commit.projection_evidence_id,
    projection_evidence.artifact_kinds,
    projection_evidence.payload_digest,
    outbox_event.sequence AS outbox_sequence,
    outbox_event.projection_revision_id,
    outbox_event.projection_ordinal,
    outbox_event.claim_token,
    outbox_event.published_at
FROM result_commits AS result_commit
INNER JOIN result_projection_evidence AS projection_evidence
    ON projection_evidence.id = result_commit.projection_evidence_id
    AND projection_evidence.tournament_id = result_commit.tournament_id
    AND projection_evidence.roster_id = result_commit.roster_id
    AND projection_evidence.series_id = result_commit.series_id
    AND projection_evidence.result_event_id = result_commit.result_event_id
INNER JOIN outbox_events AS outbox_event
    ON outbox_event.id = result_commit.outbox_event_id
    AND outbox_event.tournament_id = result_commit.tournament_id
    AND outbox_event.roster_id = result_commit.roster_id
INNER JOIN outbox_result_sources AS outbox_source
    ON outbox_source.outbox_event_id = outbox_event.id
    AND outbox_source.tournament_id = result_commit.tournament_id
    AND outbox_source.roster_id = result_commit.roster_id
    AND outbox_source.series_id = result_commit.series_id
    AND outbox_source.result_event_id = result_commit.result_event_id
    AND outbox_source.projection_evidence_id = result_commit.projection_evidence_id
WHERE result_commit.tournament_id = sqlc.arg(tournament_id)
    AND result_commit.roster_id = sqlc.arg(roster_id)
    AND result_commit.series_id = sqlc.arg(series_id)
ORDER BY outbox_event.sequence,
    outbox_event.id
FOR UPDATE OF outbox_event;

-- name: CompleteTournamentFromFinalProjectionCAS :one
UPDATE tournaments
SET state = 'completed',
    paused_from_state = NULL,
    revision = revision + 1,
    updated_at = sqlc.arg(completed_at),
    finished_at = sqlc.arg(completed_at)
WHERE id = sqlc.arg(tournament_id)
    AND state = 'playoffs'
    AND revision = sqlc.arg(expected_revision)
RETURNING id,
    preset,
    state,
    paused_from_state,
    revision,
    created_at,
    updated_at,
    started_at,
    finished_at;
