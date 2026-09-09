-- name: LockResultAttempt :one
SELECT attempt.id,
    attempt.slot_id,
    slot.slot_number AS slot_position,
    attempt.series_id,
    attempt.roster_id,
    attempt.attempt_number,
    attempt.state,
    attempt.result_reason,
    attempt.winner_id,
    attempt.result_revision_id,
    attempt.revision,
    attempt.created_at,
    attempt.updated_at,
    attempt.started_at,
    attempt.finished_at
FROM game_attempts AS attempt
INNER JOIN series AS series ON series.id = attempt.series_id
INNER JOIN game_slots AS slot
    ON slot.id = attempt.slot_id
    AND slot.series_id = attempt.series_id
    AND slot.roster_id = attempt.roster_id
WHERE attempt.id = sqlc.arg(attempt_id)
    AND attempt.series_id = sqlc.arg(series_id)
    AND attempt.roster_id = sqlc.arg(roster_id)
    AND series.tournament_id = sqlc.arg(tournament_id)
FOR UPDATE OF attempt;

-- The result writer reads this after the Series lock. It persists an exact
-- source identity, rather than deriving a source from an eventual projection.
-- name: LockResultSourceProjection :one
SELECT revision.id,
    revision.revision_number,
    revision.state,
    revision.created_at
FROM projection_revisions AS revision
WHERE revision.tournament_id = sqlc.arg(tournament_id)
    AND revision.roster_id = sqlc.arg(roster_id)
    AND revision.state = 'published'
ORDER BY revision.revision_number DESC, revision.id DESC
LIMIT 1
FOR UPDATE OF revision;

-- AllocateResultEventSequence advances the durable event cursor only after the
-- caller has locked the exact attempt. A separate UPDATE statement avoids a
-- stale READ COMMITTED snapshot over result_events.
-- name: AllocateResultEventSequence :one
UPDATE game_attempts AS attempt
SET result_event_sequence = attempt.result_event_sequence + 1
FROM series AS series
WHERE attempt.id = sqlc.arg(attempt_id)
    AND attempt.series_id = sqlc.arg(series_id)
    AND attempt.roster_id = sqlc.arg(roster_id)
    AND series.id = attempt.series_id
    AND series.tournament_id = sqlc.arg(tournament_id)
RETURNING attempt.result_event_sequence;

-- AllocateSubmissionEventSequence advances the independent immutable
-- submission stream under the same exact attempt lock.
-- name: AllocateSubmissionEventSequence :one
UPDATE game_attempts AS attempt
SET submission_event_sequence = attempt.submission_event_sequence + 1
FROM series AS series
WHERE attempt.id = sqlc.arg(attempt_id)
    AND attempt.series_id = sqlc.arg(series_id)
    AND attempt.roster_id = sqlc.arg(roster_id)
    AND series.id = attempt.series_id
    AND series.tournament_id = sqlc.arg(tournament_id)
RETURNING attempt.submission_event_sequence;

-- name: LockResultSeries :one
SELECT series.id,
    series.tournament_id,
    series.roster_id,
    series.first_participant_id,
    series.second_participant_id,
    series.format,
    series.state,
    series.first_participant_wins,
    series.second_participant_wins,
    series.winner_id,
    series.current_score_revision_id,
    series.current_result_revision_id,
    series.revision,
    series.created_at,
    series.updated_at,
    series.started_at,
    series.finished_at,
    score_head.current_revision_id AS score_head_revision_id,
    score_head.revision AS score_head_revision
FROM series AS series
INNER JOIN series_score_heads AS score_head
    ON score_head.series_id = series.id
WHERE series.id = sqlc.arg(series_id)
    AND series.tournament_id = sqlc.arg(tournament_id)
    AND series.roster_id = sqlc.arg(roster_id)
FOR UPDATE OF series, score_head;

-- name: GetResultSubmissionEventByIdempotencyKey :one
SELECT id,
    tournament_id,
    roster_id,
    series_id,
    attempt_id,
    assignment_id,
    participant_id,
    server_sequence,
    idempotency_key,
    status,
    decision_reason,
    payload_digest,
    intent_digest,
    submitted_at,
    received_at,
    created_at
FROM submission_events
WHERE idempotency_key = sqlc.arg(idempotency_key);

-- name: CreateResultSubmissionEvent :one
INSERT INTO submission_events (
    id,
    tournament_id,
    roster_id,
    series_id,
    attempt_id,
    assignment_id,
    participant_id,
    server_sequence,
    idempotency_key,
    status,
    decision_reason,
    payload_digest,
    intent_digest,
    submitted_at,
    received_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(attempt_id),
    sqlc.arg(assignment_id),
    sqlc.arg(participant_id),
    sqlc.arg(server_sequence),
    sqlc.arg(idempotency_key),
    sqlc.arg(status),
    sqlc.narg(decision_reason)::TEXT,
    sqlc.arg(payload_digest),
    sqlc.arg(intent_digest),
    sqlc.arg(submitted_at),
    sqlc.arg(received_at),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    series_id,
    attempt_id,
    assignment_id,
    participant_id,
    server_sequence,
    idempotency_key,
    status,
    decision_reason,
    payload_digest,
    intent_digest,
    submitted_at,
    received_at,
    created_at;

-- name: CreateResultEvent :one
INSERT INTO result_events (
    id,
    tournament_id,
    roster_id,
    series_id,
    attempt_id,
    submission_event_id,
    server_sequence,
    idempotency_key,
    result_state,
    result_reason,
    winner_id,
    occurred_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(attempt_id),
    sqlc.arg(submission_event_id),
    sqlc.arg(server_sequence),
    sqlc.arg(idempotency_key),
    sqlc.arg(result_state),
    sqlc.arg(result_reason),
    sqlc.arg(winner_id),
    sqlc.arg(occurred_at),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    series_id,
    attempt_id,
    submission_event_id,
    server_sequence,
    idempotency_key,
    result_state,
    result_reason,
    winner_id,
    occurred_at,
    created_at;

-- name: CreateOfficialResultRevision :one
INSERT INTO official_result_revisions (
    id,
    tournament_id,
    roster_id,
    entity_kind,
    entity_id,
    series_id,
    game_attempt_id,
    result_event_id,
    previous_revision_id,
    revision_number,
    command_id,
    actor_kind,
    actor_id,
    source_projection_revision_id,
    source_projection_revision,
    result_state,
    result_reason,
    winner_id,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(entity_kind),
    sqlc.arg(entity_id),
    sqlc.arg(series_id),
    sqlc.arg(game_attempt_id),
    sqlc.arg(result_event_id),
    sqlc.arg(previous_revision_id),
    sqlc.arg(revision_number),
    sqlc.arg(command_id),
    sqlc.arg(actor_kind),
    sqlc.narg(actor_id),
    sqlc.arg(source_projection_revision_id),
    sqlc.arg(source_projection_revision),
    sqlc.arg(result_state),
    sqlc.arg(result_reason),
    sqlc.arg(winner_id),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    entity_kind,
    entity_id,
    series_id,
    game_attempt_id,
    result_event_id,
    previous_revision_id,
    revision_number,
    command_id,
    actor_kind,
    actor_id,
    source_projection_revision_id,
    source_projection_revision,
    result_state,
    result_reason,
    winner_id,
    created_at;

-- name: CreateSeriesScoreRevision :one
INSERT INTO series_score_revisions (
    id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    previous_revision_id,
    revision_number,
    operation,
    command_id,
    actor_kind,
    actor_id,
    command_attempt_id,
    source_projection_revision_id,
    source_projection_revision,
    first_participant_wins,
    second_participant_wins,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(result_event_id),
    sqlc.arg(previous_revision_id),
    sqlc.arg(revision_number),
    sqlc.arg(operation),
    sqlc.arg(command_id),
    sqlc.arg(actor_kind),
    sqlc.narg(actor_id),
    sqlc.narg(command_attempt_id),
    sqlc.arg(source_projection_revision_id),
    sqlc.arg(source_projection_revision),
    sqlc.arg(first_participant_wins),
    sqlc.arg(second_participant_wins),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    previous_revision_id,
    revision_number,
    operation,
    command_id,
    actor_kind,
    actor_id,
    command_attempt_id,
    source_projection_revision_id,
    source_projection_revision,
    first_participant_wins,
    second_participant_wins,
    created_at;

-- name: CreateSeriesScoreRevisionAttempt :exec
INSERT INTO series_score_revision_attempts (
    score_revision_id,
    tournament_id,
    roster_id,
    series_id,
    position,
    slot_id,
    slot_position,
    game_attempt_id,
    attempt_number,
    game_result_revision_id,
    result_event_id,
    result_state,
    result_reason,
    winner_id,
    occurred_at,
    created_at
)
VALUES (
    sqlc.arg(score_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(position),
    sqlc.arg(slot_id),
    sqlc.arg(slot_position),
    sqlc.arg(game_attempt_id),
    sqlc.arg(attempt_number),
    sqlc.arg(game_result_revision_id),
    sqlc.arg(result_event_id),
    sqlc.arg(result_state),
    sqlc.arg(result_reason),
    sqlc.narg(winner_id),
    sqlc.arg(occurred_at),
    sqlc.arg(created_at)
);

-- name: LockSeriesScoreRevisionAttempts :many
SELECT attempt.score_revision_id,
    attempt.tournament_id,
    attempt.roster_id,
    attempt.series_id,
    attempt.position,
    attempt.slot_id,
    attempt.slot_position,
    attempt.game_attempt_id,
    attempt.attempt_number,
    attempt.game_result_revision_id,
    attempt.result_event_id,
    attempt.result_state,
    attempt.result_reason,
    attempt.winner_id,
    attempt.occurred_at,
    attempt.created_at
FROM series_score_revision_attempts AS attempt
WHERE attempt.score_revision_id = sqlc.arg(score_revision_id)
    AND attempt.tournament_id = sqlc.arg(tournament_id)
    AND attempt.roster_id = sqlc.arg(roster_id)
    AND attempt.series_id = sqlc.arg(series_id)
ORDER BY attempt.position
FOR UPDATE OF attempt;

-- name: CreateSeriesScoreRevisionAdjudication :exec
INSERT INTO series_score_revision_adjudications (
    score_revision_id,
    tournament_id,
    roster_id,
    series_id,
    operator_forfeit_commit_id,
    command_id,
    actor_id,
    anchor_attempt_id,
    forfeiting_participant_id,
    winner_id,
    source_projection_revision_id,
    source_projection_revision,
    created_at
)
VALUES (
    sqlc.arg(score_revision_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(operator_forfeit_commit_id),
    sqlc.arg(command_id),
    sqlc.arg(actor_id),
    sqlc.arg(anchor_attempt_id),
    sqlc.arg(forfeiting_participant_id),
    sqlc.arg(winner_id),
    sqlc.arg(source_projection_revision_id),
    sqlc.arg(source_projection_revision),
    sqlc.arg(created_at)
);

-- name: CreateResultAuditEvent :one
INSERT INTO audit_events (
    id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    actor_kind,
    actor_id,
    action,
    payload,
    occurred_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(result_event_id),
    sqlc.arg(actor_kind),
    sqlc.arg(actor_id),
    sqlc.arg(action),
    sqlc.arg(payload),
    sqlc.arg(occurred_at),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    actor_kind,
    actor_id,
    action,
    payload,
    occurred_at,
    created_at;

-- name: CreateResultProjectionEvidence :one
INSERT INTO result_projection_evidence (
    id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    artifact_kinds,
    payload_digest,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(result_event_id),
    sqlc.arg(artifact_kinds),
    sqlc.arg(payload_digest),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    artifact_kinds,
    payload_digest,
    created_at;

-- name: CreateResultOutboxEvent :one
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
allocated_sequence AS (
    INSERT INTO tournament_outbox_cursors (
        tournament_id,
        next_sequence,
        updated_at
    )
    SELECT tournament.id,
        2,
        sqlc.arg(created_at)
    FROM tournaments AS tournament
    WHERE tournament.id = sqlc.arg(tournament_id)
        AND NOT EXISTS (SELECT 1 FROM existing)
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
    SELECT sqlc.arg(projection_revision_id)::UUID,
        sqlc.arg(tournament_id),
        sqlc.arg(roster_id),
        2,
        sqlc.arg(created_at)
    FROM allocated_sequence
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
        idempotency_key,
        topic,
        payload,
        created_at,
        available_at
    )
    SELECT sqlc.arg(id),
        sqlc.arg(tournament_id),
        sqlc.arg(roster_id),
        sqlc.arg(projection_revision_id)::UUID,
        sqlc.arg(projection_revision)::BIGINT,
        allocated_sequence.sequence,
        allocated_ordinal.projection_ordinal::SMALLINT,
        sqlc.arg(idempotency_key)::UUID,
        sqlc.arg(topic),
        sqlc.arg(payload),
        sqlc.arg(created_at),
        sqlc.arg(created_at)
    FROM allocated_sequence
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
    INSERT INTO outbox_result_sources (
        outbox_event_id,
        tournament_id,
        roster_id,
        series_id,
        result_event_id,
        projection_evidence_id,
        projection_revision_id,
        projection_revision,
        projection_ordinal,
        created_at
    )
    SELECT resolved.id,
        resolved.tournament_id,
        resolved.roster_id,
        sqlc.arg(series_id),
        sqlc.arg(result_event_id),
        sqlc.arg(projection_evidence_id),
        resolved.projection_revision_id,
        resolved.projection_revision,
        resolved.projection_ordinal,
        resolved.created_at
    FROM resolved
    ON CONFLICT (outbox_event_id) DO NOTHING
    RETURNING outbox_event_id,
        tournament_id,
        roster_id,
        series_id,
        result_event_id,
        projection_evidence_id,
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
        AND source.series_id = sqlc.arg(series_id)
        AND source.result_event_id = sqlc.arg(result_event_id)
        AND source.projection_evidence_id = sqlc.arg(projection_evidence_id)
        AND source.projection_revision_id = sqlc.arg(projection_revision_id)::UUID
        AND source.projection_revision = sqlc.arg(projection_revision)::BIGINT
    UNION ALL
    SELECT outbox_source.outbox_event_id,
        outbox_source.projection_ordinal
    FROM outbox_result_sources AS outbox_source
    WHERE NOT EXISTS (
            SELECT 1
            FROM source
            WHERE source.outbox_event_id = outbox_source.outbox_event_id
        )
        AND outbox_source.tournament_id = sqlc.arg(tournament_id)
        AND outbox_source.roster_id = sqlc.arg(roster_id)
        AND outbox_source.series_id = sqlc.arg(series_id)
        AND outbox_source.result_event_id = sqlc.arg(result_event_id)
        AND outbox_source.projection_evidence_id = sqlc.arg(projection_evidence_id)
        AND outbox_source.projection_revision_id = sqlc.arg(projection_revision_id)::UUID
        AND outbox_source.projection_revision = sqlc.arg(projection_revision)::BIGINT
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
    AND NOT outbox_event.terminal
    AND outbox_event.audience = 'all'
    AND outbox_event.principal_id IS NULL
    AND outbox_event.topic = sqlc.arg(topic)
    AND outbox_event.payload = sqlc.arg(payload)::JSONB;

-- name: CreateResultCommit :one
INSERT INTO result_commits (
    id,
    tournament_id,
    roster_id,
    series_id,
    attempt_id,
    result_event_id,
    game_result_revision_id,
    series_score_revision_id,
    series_result_revision_id,
    audit_event_id,
    outbox_event_id,
    projection_evidence_id,
    idempotency_key,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(attempt_id),
    sqlc.arg(result_event_id),
    sqlc.arg(game_result_revision_id),
    sqlc.arg(series_score_revision_id),
    sqlc.arg(series_result_revision_id),
    sqlc.arg(audit_event_id),
    sqlc.arg(outbox_event_id),
    sqlc.arg(projection_evidence_id),
    sqlc.arg(idempotency_key),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    series_id,
    attempt_id,
    result_event_id,
    game_result_revision_id,
    series_score_revision_id,
    series_result_revision_id,
    audit_event_id,
    outbox_event_id,
    projection_evidence_id,
    idempotency_key,
    created_at;

-- name: CreateOfficialResultHead :one
INSERT INTO official_result_heads (
    entity_kind,
    entity_id,
    series_id,
    roster_id,
    game_attempt_id,
    current_revision_id,
    revision,
    updated_at
)
VALUES (
    sqlc.arg(entity_kind),
    sqlc.arg(entity_id),
    sqlc.arg(series_id),
    sqlc.arg(roster_id),
    sqlc.arg(game_attempt_id),
    sqlc.arg(current_revision_id),
    1,
    sqlc.arg(updated_at)
)
RETURNING entity_kind,
    entity_id,
    series_id,
    roster_id,
    game_attempt_id,
    current_revision_id,
    revision,
    updated_at;

-- name: AdvanceSeriesScoreHeadCAS :one
UPDATE series_score_heads
SET current_revision_id = sqlc.arg(current_revision_id),
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE series_id = sqlc.arg(series_id)
    AND roster_id = sqlc.arg(roster_id)
    AND current_revision_id = sqlc.arg(expected_revision_id)
    AND revision = sqlc.arg(expected_revision)
RETURNING series_id,
    roster_id,
    current_revision_id,
    revision,
    updated_at;

-- name: SettleGameAttemptCAS :one
UPDATE game_attempts AS attempt
SET state = sqlc.arg(result_state),
    result_reason = sqlc.arg(result_reason),
    winner_id = sqlc.arg(winner_id),
    result_revision_id = sqlc.arg(result_revision_id),
    revision = attempt.revision + 1,
    updated_at = sqlc.arg(settled_at)::TIMESTAMPTZ,
    finished_at = sqlc.arg(settled_at)
FROM series AS series
WHERE attempt.id = sqlc.arg(attempt_id)
    AND attempt.series_id = sqlc.arg(series_id)
    AND attempt.roster_id = sqlc.arg(roster_id)
    AND series.id = attempt.series_id
    AND series.tournament_id = sqlc.arg(tournament_id)
    AND attempt.revision = sqlc.arg(expected_revision)
    AND attempt.state = sqlc.arg(expected_state)
RETURNING attempt.id,
    attempt.slot_id,
    attempt.series_id,
    attempt.roster_id,
    attempt.attempt_number,
    attempt.state,
    attempt.result_reason,
    attempt.winner_id,
    attempt.result_revision_id,
    attempt.revision,
    attempt.created_at,
    attempt.updated_at,
    attempt.started_at,
    attempt.finished_at;

-- name: SettleSeriesCAS :one
UPDATE series
SET state = sqlc.arg(next_state),
    first_participant_wins = sqlc.arg(first_participant_wins),
    second_participant_wins = sqlc.arg(second_participant_wins),
    winner_id = sqlc.arg(winner_id),
    current_score_revision_id = sqlc.arg(score_revision_id),
    current_result_revision_id = sqlc.arg(result_revision_id),
    revision = revision + 1,
    updated_at = sqlc.arg(settled_at)::TIMESTAMPTZ,
    finished_at = CASE
        WHEN sqlc.arg(next_state)::VARCHAR IN ('completed', 'cancelled')
            THEN sqlc.arg(settled_at)::TIMESTAMPTZ
        ELSE NULL
    END
WHERE id = sqlc.arg(series_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND revision = sqlc.arg(expected_revision)
    AND state = sqlc.arg(expected_state)
    AND current_score_revision_id = sqlc.arg(expected_score_revision_id)
RETURNING id,
    tournament_id,
    roster_id,
    first_participant_id,
    second_participant_id,
    format,
    state,
    first_participant_wins,
    second_participant_wins,
    winner_id,
    current_score_revision_id,
    current_result_revision_id,
    revision,
    created_at,
    updated_at,
    started_at,
    finished_at;

-- name: GetResultCommitByID :one
SELECT id,
    tournament_id,
    roster_id,
    series_id,
    attempt_id,
    result_event_id,
    game_result_revision_id,
    series_score_revision_id,
    series_result_revision_id,
    audit_event_id,
    outbox_event_id,
    projection_evidence_id,
    idempotency_key,
    created_at
FROM result_commits
WHERE id = sqlc.arg(id);

-- name: GetResultCommitByIdempotencyKey :one
SELECT id,
    tournament_id,
    roster_id,
    series_id,
    attempt_id,
    result_event_id,
    game_result_revision_id,
    series_score_revision_id,
    series_result_revision_id,
    audit_event_id,
    outbox_event_id,
    projection_evidence_id,
    idempotency_key,
    created_at
FROM result_commits
WHERE idempotency_key = sqlc.arg(idempotency_key);

-- name: GetCurrentResultCommitForAttempt :one
SELECT result_commit.id,
    result_commit.tournament_id,
    result_commit.roster_id,
    result_commit.series_id,
    result_commit.attempt_id,
    result_commit.result_event_id,
    result_commit.game_result_revision_id,
    result_commit.series_score_revision_id,
    result_commit.series_result_revision_id,
    result_commit.audit_event_id,
    result_commit.outbox_event_id,
    result_commit.projection_evidence_id,
    result_commit.idempotency_key,
    result_commit.created_at
FROM official_result_heads AS result_head
INNER JOIN result_commits AS result_commit
    ON result_commit.game_result_revision_id = result_head.current_revision_id
WHERE result_head.entity_kind = 'game_attempt'
    AND result_head.entity_id = sqlc.arg(attempt_id)
    AND result_commit.tournament_id = sqlc.arg(tournament_id)
    AND result_commit.roster_id = sqlc.arg(roster_id)
    AND result_commit.series_id = sqlc.arg(series_id);

-- name: GetResultEventByID :one
SELECT id,
    tournament_id,
    roster_id,
    series_id,
    attempt_id,
    submission_event_id,
    server_sequence,
    idempotency_key,
    result_state,
    result_reason,
    winner_id,
    occurred_at,
    created_at
FROM result_events
WHERE id = sqlc.arg(id);

-- name: GetOfficialResultRevisionByID :one
SELECT id,
    tournament_id,
    roster_id,
    entity_kind,
    entity_id,
    series_id,
    game_attempt_id,
    result_event_id,
    previous_revision_id,
    revision_number,
    command_id,
    actor_kind,
    actor_id,
    source_projection_revision_id,
    source_projection_revision,
    result_state,
    result_reason,
    winner_id,
    created_at
FROM official_result_revisions
WHERE id = sqlc.arg(id);

-- name: GetSeriesScoreRevisionByID :one
SELECT id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    previous_revision_id,
    revision_number,
    operation,
    command_id,
    actor_kind,
    actor_id,
    command_attempt_id,
    source_projection_revision_id,
    source_projection_revision,
    first_participant_wins,
    second_participant_wins,
    created_at
FROM series_score_revisions
WHERE id = sqlc.arg(id);

-- name: GetResultAuditEventByID :one
SELECT id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    actor_kind,
    actor_id,
    action,
    payload,
    occurred_at,
    created_at
FROM audit_events
WHERE id = sqlc.arg(id);

-- name: GetResultOutboxEventByID :one
SELECT id,
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
FROM outbox_events
WHERE id = sqlc.arg(id);

-- name: GetResultProjectionEvidenceByID :one
SELECT id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    artifact_kinds,
    payload_digest,
    created_at
FROM result_projection_evidence
WHERE id = sqlc.arg(id);

-- name: ListResultHistory :many
SELECT result_commit.id AS commit_id,
    result_commit.tournament_id,
    result_commit.roster_id,
    result_commit.series_id,
    result_commit.attempt_id,
    result_event.id AS result_event_id,
    result_event.server_sequence,
    result_event.result_state,
    result_event.result_reason,
    result_event.winner_id,
    game_revision.id AS game_revision_id,
    game_revision.revision_number AS game_revision_number,
    score_revision.id AS score_revision_id,
    score_revision.revision_number AS score_revision_number,
    score_revision.first_participant_wins,
    score_revision.second_participant_wins,
    result_commit.series_result_revision_id,
    result_commit.created_at
FROM result_commits AS result_commit
INNER JOIN result_events AS result_event
    ON result_event.id = result_commit.result_event_id
INNER JOIN official_result_revisions AS game_revision
    ON game_revision.id = result_commit.game_result_revision_id
INNER JOIN series_score_revisions AS score_revision
    ON score_revision.id = result_commit.series_score_revision_id
WHERE result_commit.tournament_id = sqlc.arg(tournament_id)
    AND result_commit.roster_id = sqlc.arg(roster_id)
    AND result_commit.series_id = sqlc.arg(series_id)
    AND result_commit.attempt_id = sqlc.arg(attempt_id)
ORDER BY game_revision.revision_number;
