-- name: LockTournamentCancellationAuthority :one
SELECT tournament.id,
    tournament.state,
    tournament.paused_from_state,
    tournament.revision,
    tournament.updated_at,
    tournament.finished_at,
    roster.id AS roster_id,
    projection_revision.id AS projection_revision_id,
    projection_revision.revision_number AS projection_revision
FROM tournaments AS tournament
INNER JOIN rosters AS roster ON roster.tournament_id = tournament.id
INNER JOIN projection_revisions AS projection_revision
    ON projection_revision.tournament_id = tournament.id
    AND projection_revision.roster_id = roster.id
    AND projection_revision.state = 'published'
WHERE tournament.id = sqlc.arg(tournament_id)
    AND tournament.deleted_at IS NULL
FOR UPDATE OF tournament, projection_revision;

-- name: FindTournamentCancellation :one
SELECT cancellation.command_id,
    cancellation.tournament_id,
    cancellation.roster_id,
    cancellation.source_revision,
    cancellation.resulting_revision,
    cancellation.source_state,
    cancellation.actor_id,
    cancellation.reason,
    cancellation.audit_event_id,
    cancellation.outbox_event_id,
    cancellation.cancelled_at,
    cancellation.created_at,
    tournament.state AS tournament_state,
    tournament.paused_from_state,
    tournament.revision AS tournament_revision,
    tournament.updated_at AS tournament_updated_at,
    tournament.finished_at AS tournament_finished_at
FROM tournament_cancellations AS cancellation
INNER JOIN tournaments AS tournament ON tournament.id = cancellation.tournament_id
WHERE cancellation.tournament_id = sqlc.arg(tournament_id)
    AND cancellation.command_id = sqlc.arg(command_id);

-- name: GetTournamentCancellationAudit :one
SELECT cancellation.command_id,
    cancellation.tournament_id,
    cancellation.roster_id,
    cancellation.source_revision,
    cancellation.resulting_revision,
    cancellation.actor_id,
    cancellation.reason,
    cancellation.audit_event_id,
    cancellation.cancelled_at,
    source.projection_revision_id AS source_projection_revision_id,
    source.projection_revision AS source_projection_revision
FROM tournament_cancellations AS cancellation
INNER JOIN outbox_tournament_cancellation_sources AS source
    ON source.cancellation_command_id = cancellation.command_id
    AND source.tournament_id = cancellation.tournament_id
    AND source.roster_id = cancellation.roster_id
    AND source.outbox_event_id = cancellation.outbox_event_id
WHERE cancellation.tournament_id = sqlc.arg(tournament_id);

-- name: CancelTournamentForCancellationCAS :one
UPDATE tournaments
SET state = 'cancelled',
    paused_from_state = NULL,
    revision = revision + 1,
    updated_at = sqlc.arg(cancelled_at),
    finished_at = sqlc.arg(cancelled_at)
WHERE id = sqlc.arg(tournament_id)
    AND revision = sqlc.arg(expected_revision)
    AND state = sqlc.arg(expected_state)
    AND state NOT IN ('completed', 'cancelled')
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

-- name: CreateTournamentCancellationAuditEvent :one
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
    NULL,
    NULL,
    'operator',
    sqlc.arg(actor_id),
    'tournament.cancelled',
    jsonb_build_object(
        'reason', sqlc.arg(reason)::TEXT,
        'source_revision', sqlc.arg(source_revision)::BIGINT,
        'resulting_revision', sqlc.arg(resulting_revision)::BIGINT,
        'source_projection_revision_id', sqlc.arg(source_projection_revision_id)::UUID,
        'source_projection_revision', sqlc.arg(source_projection_revision)::BIGINT
    ),
    sqlc.arg(cancelled_at),
    sqlc.arg(cancelled_at)
)
RETURNING id;

-- name: LockTournamentCancellationOutboxIdempotency :exec
SELECT pg_advisory_xact_lock(
    hashtextextended(sqlc.arg(idempotency_key)::TEXT, 0)
);

-- name: CreateTournamentCancellationOutboxEvent :one
WITH existing AS MATERIALIZED (
    SELECT outbox_event.id,
        outbox_event.tournament_id,
        outbox_event.roster_id,
        outbox_event.sequence,
        outbox_event.projection_revision_id,
        outbox_event.projection_revision,
        outbox_event.projection_ordinal,
        outbox_event.terminal,
        outbox_event.audience,
        outbox_event.principal_id,
        outbox_event.topic,
        outbox_event.payload,
        outbox_event.created_at,
        outbox_event.available_at
    FROM outbox_events AS outbox_event
    WHERE outbox_event.idempotency_key = sqlc.arg(idempotency_key)
),
authoritative_projection AS MATERIALIZED (
    SELECT projection_revision.id,
        projection_revision.tournament_id,
        projection_revision.roster_id,
        projection_revision.revision_number
    FROM projection_revisions AS projection_revision
    WHERE projection_revision.id = sqlc.arg(projection_revision_id)
        AND projection_revision.tournament_id = sqlc.arg(tournament_id)
        AND projection_revision.roster_id = sqlc.arg(roster_id)
        AND projection_revision.revision_number = sqlc.arg(projection_revision)
        AND projection_revision.state = 'published'
    FOR SHARE
),
allocated_sequence AS (
    INSERT INTO tournament_outbox_cursors (
        tournament_id,
        next_sequence,
        updated_at
    )
    SELECT authoritative_projection.tournament_id,
        2,
        sqlc.arg(cancelled_at)
    FROM authoritative_projection
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
    SELECT authoritative_projection.id,
        authoritative_projection.tournament_id,
        authoritative_projection.roster_id,
        2,
        sqlc.arg(cancelled_at)
    FROM authoritative_projection
    INNER JOIN allocated_sequence ON true
    ON CONFLICT (projection_revision_id) DO UPDATE
    SET next_ordinal = projection_outbox_cursors.next_ordinal + 1,
        updated_at = EXCLUDED.updated_at
    WHERE projection_outbox_cursors.tournament_id = EXCLUDED.tournament_id
        AND projection_outbox_cursors.roster_id = EXCLUDED.roster_id
        AND projection_outbox_cursors.next_ordinal < 32768
    RETURNING next_ordinal - 1 AS projection_ordinal
),
created AS (
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
    SELECT
        sqlc.arg(id),
        authoritative_projection.tournament_id,
        authoritative_projection.roster_id,
        authoritative_projection.id,
        authoritative_projection.revision_number,
        allocated_sequence.sequence,
        allocated_ordinal.projection_ordinal,
        true,
        sqlc.arg(idempotency_key),
        'all',
        NULL,
        'tournament.cancelled',
        jsonb_build_object('state', 'cancelled'),
        sqlc.arg(cancelled_at),
        sqlc.arg(cancelled_at)
    FROM authoritative_projection
    INNER JOIN allocated_sequence ON true
    INNER JOIN allocated_ordinal ON true
    ON CONFLICT (idempotency_key) DO NOTHING
    RETURNING id,
        tournament_id,
        roster_id,
        sequence,
        projection_revision_id,
        projection_revision,
        projection_ordinal
),
existing_verified AS MATERIALIZED (
    SELECT existing.id,
        existing.tournament_id,
        existing.roster_id,
        existing.sequence,
        existing.projection_revision_id,
        existing.projection_revision,
        existing.projection_ordinal
    FROM existing
    INNER JOIN authoritative_projection
        ON authoritative_projection.id = existing.projection_revision_id
        AND authoritative_projection.tournament_id = existing.tournament_id
        AND authoritative_projection.roster_id = existing.roster_id
        AND authoritative_projection.revision_number = existing.projection_revision
    INNER JOIN outbox_tournament_cancellation_sources AS source
        ON source.outbox_event_id = existing.id
        AND source.tournament_id = existing.tournament_id
        AND source.roster_id = existing.roster_id
        AND source.cancellation_command_id = sqlc.arg(cancellation_command_id)
        AND source.projection_revision_id = existing.projection_revision_id
        AND source.projection_revision = existing.projection_revision
        AND source.projection_ordinal = existing.projection_ordinal
        AND source.created_at = existing.created_at
    WHERE existing.id = sqlc.arg(id)
        AND existing.tournament_id = sqlc.arg(tournament_id)
        AND existing.roster_id = sqlc.arg(roster_id)
        AND existing.terminal
        AND existing.audience = 'all'
        AND existing.principal_id IS NULL
        AND existing.topic = 'tournament.cancelled'
        AND existing.payload = jsonb_build_object('state', 'cancelled')
        AND existing.created_at = sqlc.arg(cancelled_at)
        AND existing.available_at = sqlc.arg(cancelled_at)
),
resolved AS MATERIALIZED (
    SELECT created.id,
        created.tournament_id,
        created.roster_id,
        created.sequence,
        created.projection_revision_id,
        created.projection_revision,
        created.projection_ordinal
    FROM created
    UNION ALL
    SELECT existing_verified.id,
        existing_verified.tournament_id,
        existing_verified.roster_id,
        existing_verified.sequence,
        existing_verified.projection_revision_id,
        existing_verified.projection_revision,
        existing_verified.projection_ordinal
    FROM existing_verified
    WHERE NOT EXISTS (SELECT 1 FROM created)
)
SELECT outbox_event.id,
    outbox_event.sequence,
    outbox_event.projection_revision_id,
    outbox_event.projection_revision,
    outbox_event.projection_ordinal
FROM resolved AS outbox_event;

-- name: CreateTournamentCancellation :one
WITH created AS (
    INSERT INTO tournament_cancellations (
    command_id,
    tournament_id,
    roster_id,
    source_revision,
    resulting_revision,
    source_state,
    actor_id,
    reason,
    audit_event_id,
    outbox_event_id,
    cancelled_at,
    created_at
    )
    VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(source_revision),
    sqlc.arg(resulting_revision),
    sqlc.arg(source_state),
    sqlc.arg(actor_id),
    sqlc.arg(reason),
    sqlc.arg(audit_event_id),
    sqlc.arg(outbox_event_id),
    sqlc.arg(cancelled_at),
    sqlc.arg(cancelled_at)
    )
    RETURNING command_id,
    tournament_id,
    roster_id,
    source_revision,
    resulting_revision,
    source_state,
    actor_id,
    reason,
    audit_event_id,
    outbox_event_id,
    cancelled_at,
    created_at
),
source AS (
    INSERT INTO outbox_tournament_cancellation_sources (
        outbox_event_id,
        tournament_id,
        roster_id,
        cancellation_command_id,
        projection_revision_id,
        projection_revision,
        projection_ordinal,
        created_at
    )
    SELECT outbox_event_id,
        tournament_id,
        roster_id,
        command_id,
        sqlc.arg(projection_revision_id),
        sqlc.arg(projection_revision),
        sqlc.arg(projection_ordinal),
        created_at
    FROM created
    RETURNING outbox_event_id
)
SELECT created.command_id,
    created.tournament_id,
    created.roster_id,
    created.source_revision,
    created.resulting_revision,
    created.source_state,
    created.actor_id,
    created.reason,
    created.audit_event_id,
    created.outbox_event_id,
    created.cancelled_at,
    created.created_at
FROM created
CROSS JOIN source;
