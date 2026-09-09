-- name: ClaimRealtimeOutboxEvents :many
WITH candidates AS MATERIALIZED (
    SELECT outbox_event.id
    FROM outbox_events AS outbox_event
    INNER JOIN projection_revisions AS projection_revision
        ON projection_revision.id = outbox_event.projection_revision_id
        AND projection_revision.tournament_id = outbox_event.tournament_id
        AND projection_revision.roster_id = outbox_event.roster_id
        AND projection_revision.revision_number = outbox_event.projection_revision
    WHERE outbox_event.published_at IS NULL
        AND projection_revision.state IN ('published', 'superseded')
        AND outbox_event.available_at <= sqlc.arg(claimed_at)
        AND (
            outbox_event.claimed_until IS NULL
            OR outbox_event.claimed_until <= sqlc.arg(claimed_at)
        )
        AND NOT EXISTS (
            SELECT 1
            FROM outbox_events AS predecessor
            WHERE predecessor.tournament_id = outbox_event.tournament_id
                AND predecessor.sequence < outbox_event.sequence
                AND predecessor.published_at IS NULL
        )
    ORDER BY outbox_event.tournament_id,
        outbox_event.sequence
    LIMIT sqlc.arg(batch_size)
    FOR UPDATE OF outbox_event SKIP LOCKED
),
claimed AS (
    UPDATE outbox_events AS outbox_event
    SET claimed_by = sqlc.arg(worker_id),
        claim_token = sqlc.arg(claim_token),
        claimed_until = sqlc.arg(claimed_until),
        attempt_count = outbox_event.attempt_count + 1
    FROM candidates
    WHERE outbox_event.id = candidates.id
    RETURNING outbox_event.id,
        outbox_event.idempotency_key,
        outbox_event.tournament_id,
        outbox_event.roster_id,
        outbox_event.projection_revision_id,
        outbox_event.projection_revision,
        outbox_event.sequence,
        outbox_event.projection_ordinal,
        outbox_event.terminal,
        outbox_event.audience,
        outbox_event.principal_id,
        outbox_event.topic,
        outbox_event.payload,
        outbox_event.created_at,
        outbox_event.attempt_count
)
SELECT claimed.id,
    claimed.idempotency_key,
    claimed.tournament_id,
    claimed.projection_revision_id,
    claimed.sequence,
    claimed.projection_revision,
    claimed.projection_ordinal,
    claimed.terminal,
    claimed.audience,
    claimed.principal_id,
    claimed.topic,
    claimed.payload,
    claimed.created_at,
    claimed.attempt_count
FROM claimed
INNER JOIN projection_revisions AS projection_revision
    ON projection_revision.id = claimed.projection_revision_id
    AND projection_revision.tournament_id = claimed.tournament_id
    AND projection_revision.roster_id = claimed.roster_id
    AND projection_revision.revision_number = claimed.projection_revision
ORDER BY claimed.tournament_id,
    claimed.sequence;

-- name: AcknowledgeRealtimeOutboxEvent :one
UPDATE outbox_events
SET published_at = GREATEST(sqlc.arg(acknowledged_at), created_at),
    claimed_by = NULL,
    claim_token = NULL,
    claimed_until = NULL
WHERE id = sqlc.arg(event_id)
    AND published_at IS NULL
    AND claimed_by = sqlc.arg(worker_id)
    AND claim_token = sqlc.arg(claim_token)
RETURNING id;

-- name: RetryRealtimeOutboxEvent :one
UPDATE outbox_events
SET available_at = sqlc.arg(available_at),
    claimed_by = NULL,
    claim_token = NULL,
    claimed_until = NULL,
    last_error = sqlc.arg(reason)
WHERE id = sqlc.arg(event_id)
    AND published_at IS NULL
    AND claimed_by = sqlc.arg(worker_id)
    AND claim_token = sqlc.arg(claim_token)
RETURNING id;

-- name: ReleaseRealtimeOutboxClaims :execrows
UPDATE outbox_events
SET claimed_by = NULL,
    claim_token = NULL,
    claimed_until = NULL
WHERE published_at IS NULL
    AND claimed_by = sqlc.arg(worker_id);

-- name: ListRealtimeOutboxAfter :many
SELECT outbox_event.id,
    outbox_event.idempotency_key,
    outbox_event.tournament_id,
    outbox_event.projection_revision_id,
    outbox_event.sequence,
    outbox_event.projection_revision,
    outbox_event.projection_ordinal,
    outbox_event.terminal,
    outbox_event.audience,
    outbox_event.principal_id,
    outbox_event.topic,
    outbox_event.payload,
    outbox_event.created_at,
    GREATEST(outbox_event.attempt_count, 1)::INTEGER AS attempt_count
FROM outbox_events AS outbox_event
INNER JOIN projection_revisions AS projection_revision
    ON projection_revision.id = outbox_event.projection_revision_id
    AND projection_revision.tournament_id = outbox_event.tournament_id
    AND projection_revision.roster_id = outbox_event.roster_id
    AND projection_revision.revision_number = outbox_event.projection_revision
WHERE outbox_event.tournament_id = sqlc.arg(tournament_id)
    AND outbox_event.sequence > sqlc.arg(after_sequence)
    AND projection_revision.state IN ('published', 'superseded')
    AND (
        outbox_event.audience = 'all'
        OR (
            outbox_event.audience = sqlc.arg(role)
            AND outbox_event.principal_id IS NOT DISTINCT FROM sqlc.narg(principal_id)::UUID
        )
    )
ORDER BY outbox_event.tournament_id,
    outbox_event.sequence
LIMIT sqlc.arg(batch_size);

-- name: GetRealtimeOutboxCursor :one
SELECT COALESCE(MAX(outbox_event.sequence), 0)::BIGINT AS sequence
FROM outbox_events AS outbox_event
INNER JOIN projection_revisions AS projection_revision
    ON projection_revision.id = outbox_event.projection_revision_id
    AND projection_revision.tournament_id = outbox_event.tournament_id
    AND projection_revision.roster_id = outbox_event.roster_id
    AND projection_revision.revision_number = outbox_event.projection_revision
WHERE outbox_event.tournament_id = sqlc.arg(tournament_id)
    AND projection_revision.state IN ('published', 'superseded');

-- name: GetRealtimeOutboxBacklog :one
SELECT COUNT(*) FILTER (
        WHERE published_at IS NULL
    )::BIGINT AS pending_count,
    MIN(created_at) FILTER (
        WHERE published_at IS NULL
    )::TIMESTAMPTZ AS oldest_pending_at
FROM outbox_events;

-- name: MarkRealtimeOutboxTerminal :one
UPDATE outbox_events
SET terminal = true
WHERE id = sqlc.arg(event_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND projection_revision_id = sqlc.arg(projection_revision_id)::UUID
    AND projection_ordinal = sqlc.arg(projection_ordinal)
    AND NOT terminal
    AND claim_token IS NULL
    AND published_at IS NULL
RETURNING id;

-- name: OpenRealtimeSubscription :one
WITH resumed AS MATERIALIZED (
    SELECT subscriber.id
    FROM realtime_subscribers AS subscriber
    WHERE subscriber.id = sqlc.narg(resume_id)::UUID
        AND subscriber.tournament_id = sqlc.arg(tournament_id)
        AND subscriber.role = sqlc.arg(role)
        AND subscriber.principal_id IS NOT DISTINCT FROM sqlc.narg(principal_id)::UUID
        AND (
            subscriber.closed_at IS NULL
            OR EXISTS (
                SELECT 1
                FROM realtime_delivery_receipts AS receipt
                INNER JOIN outbox_events AS outbox_event
                    ON outbox_event.id = receipt.event_id
                    AND outbox_event.tournament_id = receipt.tournament_id
                WHERE receipt.subscriber_id = subscriber.id
                    AND outbox_event.terminal
                    AND receipt.outcome = 'written'
            )
        )
    FOR UPDATE OF subscriber
),
created AS MATERIALIZED (
    INSERT INTO realtime_subscribers (
        id,
        instance_id,
        connection_id,
        connection_generation,
        tournament_id,
        role,
        principal_id,
        initial_sequence,
        last_acknowledged_sequence,
        snapshot_sequence,
        connected_at
    )
    SELECT sqlc.arg(subscriber_id),
        sqlc.arg(instance_id),
        sqlc.arg(connection_id),
        1,
        sqlc.arg(tournament_id),
        sqlc.arg(role),
        sqlc.narg(principal_id)::UUID,
        sqlc.arg(after_sequence),
        0,
        sqlc.arg(after_sequence),
        sqlc.arg(opened_at)
    WHERE sqlc.narg(resume_id)::UUID IS NULL
    RETURNING id,
        instance_id,
        connection_id,
        connection_generation,
        tournament_id,
        role,
        principal_id,
        initial_sequence,
        last_acknowledged_sequence,
        snapshot_sequence,
        connected_at,
        closed_at,
        close_reason
),
taken_over AS MATERIALIZED (
    UPDATE realtime_subscribers AS subscriber
    SET instance_id = sqlc.arg(instance_id),
        connection_id = sqlc.arg(connection_id),
        connection_generation = subscriber.connection_generation + 1,
        snapshot_sequence = GREATEST(
            subscriber.snapshot_sequence,
            subscriber.last_acknowledged_sequence,
            sqlc.arg(after_sequence)
        )
    FROM resumed
    WHERE subscriber.id = resumed.id
    RETURNING subscriber.id,
        subscriber.instance_id,
        subscriber.connection_id,
        subscriber.connection_generation,
        subscriber.tournament_id,
        subscriber.role,
        subscriber.principal_id,
        subscriber.initial_sequence,
        subscriber.last_acknowledged_sequence,
        subscriber.snapshot_sequence,
        subscriber.connected_at,
        subscriber.closed_at,
        subscriber.close_reason
),
subscriber AS MATERIALIZED (
    SELECT id,
        instance_id,
        connection_id,
        connection_generation,
        tournament_id,
        role,
        principal_id,
        initial_sequence,
        last_acknowledged_sequence,
        snapshot_sequence,
        connected_at,
        closed_at,
        close_reason
    FROM taken_over
    UNION ALL
    SELECT id,
        instance_id,
        connection_id,
        connection_generation,
        tournament_id,
        role,
        principal_id,
        initial_sequence,
        last_acknowledged_sequence,
        snapshot_sequence,
        connected_at,
        closed_at,
        close_reason
    FROM created
),
terminal_event AS MATERIALIZED (
    SELECT outbox_event.id,
        outbox_event.idempotency_key,
        outbox_event.tournament_id,
        outbox_event.projection_revision_id,
        outbox_event.sequence,
        outbox_event.projection_revision,
        outbox_event.projection_ordinal,
        outbox_event.terminal,
        outbox_event.audience,
        outbox_event.principal_id,
        outbox_event.topic,
        outbox_event.payload,
        outbox_event.created_at,
        GREATEST(outbox_event.attempt_count, 1)::INTEGER AS attempt_count
    FROM subscriber
    INNER JOIN outbox_events AS outbox_event
        ON outbox_event.tournament_id = subscriber.tournament_id
        AND outbox_event.terminal
    INNER JOIN projection_revisions AS projection_revision
        ON projection_revision.id = outbox_event.projection_revision_id
        AND projection_revision.tournament_id = outbox_event.tournament_id
        AND projection_revision.roster_id = outbox_event.roster_id
        AND projection_revision.revision_number = outbox_event.projection_revision
    WHERE projection_revision.state IN ('published', 'superseded')
        AND (
            outbox_event.audience = 'all'
            OR (
                outbox_event.audience = subscriber.role
                AND outbox_event.principal_id IS NOT DISTINCT FROM subscriber.principal_id
            )
        )
    ORDER BY outbox_event.sequence DESC
    LIMIT 1
)
SELECT subscriber.id AS subscriber_id,
    subscriber.instance_id,
    subscriber.connection_id,
    subscriber.connection_generation,
    subscriber.tournament_id,
    subscriber.role,
    subscriber.principal_id,
    subscriber.snapshot_sequence AS after_sequence,
    sqlc.arg(opened_at)::TIMESTAMPTZ AS opened_at,
    CASE
        WHEN terminal_event.id IS NULL THEN 'none'
        WHEN receipt.outcome = 'written' THEN 'written'
        ELSE 'pending'
    END AS terminal_state,
    terminal_event.id AS terminal_event_id,
    terminal_event.idempotency_key AS terminal_correlation_id,
    terminal_event.tournament_id AS terminal_tournament_id,
    terminal_event.projection_revision_id AS terminal_projection_revision_id,
    terminal_event.sequence AS terminal_sequence,
    terminal_event.projection_revision AS terminal_projection_revision,
    terminal_event.projection_ordinal AS terminal_projection_ordinal,
    terminal_event.terminal AS terminal_terminal,
    terminal_event.audience AS terminal_audience,
    terminal_event.principal_id AS terminal_principal_id,
    terminal_event.topic AS terminal_topic,
    terminal_event.payload AS terminal_payload,
    terminal_event.created_at AS terminal_created_at,
    terminal_event.attempt_count AS terminal_attempt_count
FROM subscriber
LEFT JOIN terminal_event ON true
LEFT JOIN realtime_delivery_receipts AS receipt
    ON receipt.subscriber_id = subscriber.id
    AND receipt.event_id = terminal_event.id;

-- name: CloseRealtimeSubscriber :one
UPDATE realtime_subscribers
SET closed_at = sqlc.arg(closed_at),
    close_reason = sqlc.arg(close_reason)
WHERE id = sqlc.arg(subscriber_id)
    AND instance_id = sqlc.arg(instance_id)
    AND connection_id = sqlc.arg(connection_id)
    AND connection_generation = sqlc.arg(connection_generation)
    AND closed_at IS NULL
    AND sqlc.arg(close_reason) IN ('tournament_terminal', 'terminal_already_written')
RETURNING id;

-- name: AbandonRealtimeDeliveriesForSubscriber :execrows
UPDATE realtime_delivery_receipts
SET claimed_by = NULL,
    claim_token = NULL,
    claimed_until = NULL,
    terminal_at = GREATEST(sqlc.arg(closed_at), enqueued_at),
    outcome = 'disconnected'
WHERE subscriber_id = sqlc.arg(subscriber_id)
    AND terminal_at IS NULL;

-- name: ClaimRealtimeDelivery :one
WITH locked_subscriber AS MATERIALIZED (
    SELECT subscriber.id,
        subscriber.instance_id,
        subscriber.connection_id,
        subscriber.connection_generation,
        subscriber.tournament_id,
        subscriber.role,
        subscriber.principal_id,
        subscriber.last_acknowledged_sequence
    FROM realtime_subscribers AS subscriber
    WHERE subscriber.id = sqlc.arg(subscriber_id)
        AND subscriber.instance_id = sqlc.arg(instance_id)
        AND subscriber.connection_id = sqlc.arg(connection_id)
        AND subscriber.connection_generation = sqlc.arg(connection_generation)
        AND subscriber.tournament_id = sqlc.arg(tournament_id)
        AND subscriber.role = sqlc.arg(role)
        AND subscriber.principal_id IS NOT DISTINCT FROM sqlc.narg(principal_id)::UUID
        AND subscriber.closed_at IS NULL
    FOR UPDATE OF subscriber
)
INSERT INTO realtime_delivery_receipts (
    subscriber_id,
    event_id,
    tournament_id,
    sequence,
    role,
    principal_id,
    enqueued_at,
    available_at,
    claimed_by,
    claim_token,
    claimed_until,
    attempt_count
)
SELECT subscriber.id AS subscriber_id,
    outbox_event.id AS event_id,
    outbox_event.tournament_id,
    outbox_event.sequence,
    subscriber.role,
    subscriber.principal_id,
    sqlc.arg(claimed_at),
    sqlc.arg(claimed_at),
    sqlc.arg(worker_id),
    sqlc.arg(claim_token),
    sqlc.arg(claimed_until),
    1
FROM locked_subscriber AS subscriber
INNER JOIN outbox_events AS outbox_event
    ON outbox_event.id = sqlc.arg(event_id)
    AND outbox_event.tournament_id = subscriber.tournament_id
    AND outbox_event.sequence = sqlc.arg(sequence)
INNER JOIN projection_revisions AS projection_revision
    ON projection_revision.id = outbox_event.projection_revision_id
    AND projection_revision.tournament_id = outbox_event.tournament_id
    AND projection_revision.roster_id = outbox_event.roster_id
    AND projection_revision.revision_number = outbox_event.projection_revision
WHERE (
        outbox_event.terminal
        OR subscriber.snapshot_sequence < outbox_event.sequence
    )
    AND projection_revision.state IN ('published', 'superseded')
    AND (
        outbox_event.audience = 'all'
        OR (
            outbox_event.audience = subscriber.role
            AND outbox_event.principal_id IS NOT DISTINCT FROM subscriber.principal_id
        )
    )
ON CONFLICT (subscriber_id, event_id) DO UPDATE
SET claimed_by = EXCLUDED.claimed_by,
    claim_token = EXCLUDED.claim_token,
    claimed_until = EXCLUDED.claimed_until,
    attempt_count = realtime_delivery_receipts.attempt_count + 1
WHERE realtime_delivery_receipts.terminal_at IS NULL
    AND realtime_delivery_receipts.available_at <= EXCLUDED.enqueued_at
    AND (
        realtime_delivery_receipts.claimed_until IS NULL
        OR realtime_delivery_receipts.claimed_until <= EXCLUDED.enqueued_at
    )
RETURNING subscriber_id;

-- name: AcknowledgeRealtimeDelivery :one
WITH locked_subscriber AS MATERIALIZED (
    SELECT subscriber.id,
        subscriber.instance_id,
        subscriber.connection_id,
        subscriber.connection_generation,
        subscriber.tournament_id
    FROM realtime_subscribers AS subscriber
    WHERE subscriber.id = sqlc.arg(subscriber_id)
        AND subscriber.instance_id = sqlc.arg(instance_id)
        AND subscriber.connection_id = sqlc.arg(connection_id)
        AND subscriber.connection_generation = sqlc.arg(connection_generation)
        AND subscriber.closed_at IS NULL
    FOR UPDATE OF subscriber
),
acknowledged AS (
    UPDATE realtime_delivery_receipts AS receipt
    SET claimed_by = NULL,
        claim_token = NULL,
        claimed_until = NULL,
        write_acknowledged_at = GREATEST(
            sqlc.arg(acknowledged_at)::TIMESTAMPTZ,
            receipt.enqueued_at
        ),
        terminal_at = GREATEST(
            sqlc.arg(acknowledged_at)::TIMESTAMPTZ,
            receipt.enqueued_at
        ),
        outcome = 'written'
    FROM locked_subscriber AS subscriber
    WHERE receipt.subscriber_id = subscriber.id
        AND receipt.tournament_id = subscriber.tournament_id
        AND receipt.event_id = sqlc.arg(event_id)
        AND receipt.sequence = sqlc.arg(sequence)
        AND receipt.terminal_at IS NULL
        AND receipt.claimed_by = sqlc.arg(worker_id)
        AND receipt.claim_token = sqlc.arg(claim_token)
    RETURNING receipt.subscriber_id,
        receipt.tournament_id,
        receipt.sequence
)
UPDATE realtime_subscribers AS subscriber
SET last_acknowledged_sequence = GREATEST(
    subscriber.last_acknowledged_sequence,
    acknowledged.sequence
),
    snapshot_sequence = GREATEST(
        subscriber.snapshot_sequence,
        acknowledged.sequence
    )
FROM acknowledged
WHERE subscriber.id = acknowledged.subscriber_id
    AND subscriber.tournament_id = acknowledged.tournament_id
RETURNING acknowledged.subscriber_id;

-- name: RetryRealtimeDelivery :one
WITH locked_subscriber AS MATERIALIZED (
    SELECT subscriber.id
    FROM realtime_subscribers AS subscriber
    WHERE subscriber.id = sqlc.arg(subscriber_id)
        AND subscriber.instance_id = sqlc.arg(instance_id)
        AND subscriber.connection_id = sqlc.arg(connection_id)
        AND subscriber.connection_generation = sqlc.arg(connection_generation)
        AND subscriber.closed_at IS NULL
    FOR UPDATE OF subscriber
)
UPDATE realtime_delivery_receipts AS receipt
SET available_at = sqlc.arg(available_at),
    claimed_by = NULL,
    claim_token = NULL,
    claimed_until = NULL,
    last_error = sqlc.arg(reason)
FROM locked_subscriber AS subscriber
WHERE receipt.subscriber_id = subscriber.id
    AND receipt.event_id = sqlc.arg(event_id)
    AND receipt.terminal_at IS NULL
    AND receipt.claimed_by = sqlc.arg(worker_id)
    AND receipt.claim_token = sqlc.arg(claim_token)
RETURNING receipt.subscriber_id;

-- name: GetRealtimeSubscriberCursor :one
SELECT subscriber.last_acknowledged_sequence
FROM realtime_subscribers AS subscriber
WHERE subscriber.id = sqlc.arg(subscriber_id)
    AND subscriber.instance_id = sqlc.arg(instance_id)
    AND subscriber.connection_id = sqlc.arg(connection_id)
    AND subscriber.connection_generation = sqlc.arg(connection_generation)
    AND subscriber.closed_at IS NULL;

-- name: GetRealtimeDeliveryReceipt :one
SELECT receipt.subscriber_id,
    receipt.event_id,
    receipt.tournament_id,
    receipt.sequence,
    receipt.role,
    receipt.principal_id,
    receipt.enqueued_at,
    receipt.available_at,
    receipt.claimed_by,
    receipt.claim_token,
    receipt.claimed_until,
    receipt.attempt_count,
    receipt.write_acknowledged_at,
    receipt.terminal_at,
    receipt.outcome,
    receipt.last_error
FROM realtime_delivery_receipts AS receipt
INNER JOIN realtime_subscribers AS subscriber
    ON subscriber.id = receipt.subscriber_id
    AND subscriber.tournament_id = receipt.tournament_id
WHERE receipt.subscriber_id = sqlc.arg(subscriber_id)
    AND subscriber.instance_id = sqlc.arg(instance_id)
    AND subscriber.connection_id = sqlc.arg(connection_id)
    AND subscriber.connection_generation = sqlc.arg(connection_generation)
    AND subscriber.closed_at IS NULL
    AND receipt.event_id = sqlc.arg(event_id);

-- name: DeleteExpiredRealtimeDeliveryReceipts :many
WITH closed_subscribers AS MATERIALIZED (
    SELECT subscriber.id,
        subscriber.tournament_id
    FROM realtime_subscribers AS subscriber
    WHERE subscriber.closed_at IS NOT NULL
        AND subscriber.closed_at < sqlc.arg(cutoff_at)
        AND EXISTS (
            SELECT 1
            FROM realtime_delivery_receipts AS receipt
            WHERE receipt.subscriber_id = subscriber.id
                AND receipt.tournament_id = subscriber.tournament_id
                AND receipt.terminal_at IS NOT NULL
                AND receipt.terminal_at < sqlc.arg(cutoff_at)
        )
    ORDER BY subscriber.closed_at,
        subscriber.id
    LIMIT sqlc.arg(batch_size)
    FOR UPDATE OF subscriber SKIP LOCKED
),
candidates AS MATERIALIZED (
    SELECT receipt.subscriber_id,
        receipt.event_id
    FROM closed_subscribers AS subscriber
    INNER JOIN realtime_delivery_receipts AS receipt
        ON receipt.subscriber_id = subscriber.id
        AND receipt.tournament_id = subscriber.tournament_id
    WHERE receipt.terminal_at IS NOT NULL
        AND receipt.terminal_at < sqlc.arg(cutoff_at)
    ORDER BY receipt.terminal_at,
        receipt.subscriber_id,
        receipt.event_id
    LIMIT sqlc.arg(batch_size)
    FOR UPDATE OF receipt SKIP LOCKED
)
DELETE FROM realtime_delivery_receipts AS receipt
USING candidates
WHERE receipt.subscriber_id = candidates.subscriber_id
    AND receipt.event_id = candidates.event_id
RETURNING receipt.subscriber_id,
    receipt.event_id;

-- name: DeleteExpiredRealtimeSubscribers :many
WITH candidates AS MATERIALIZED (
    SELECT subscriber.id
    FROM realtime_subscribers AS subscriber
    WHERE subscriber.closed_at IS NOT NULL
        AND subscriber.closed_at < sqlc.arg(cutoff_at)
        AND NOT EXISTS (
            SELECT 1
            FROM realtime_delivery_receipts AS receipt
            WHERE receipt.subscriber_id = subscriber.id
                AND receipt.tournament_id = subscriber.tournament_id
        )
    ORDER BY subscriber.closed_at,
        subscriber.id
    LIMIT sqlc.arg(batch_size)
    FOR UPDATE OF subscriber SKIP LOCKED
)
DELETE FROM realtime_subscribers AS subscriber
USING candidates
WHERE subscriber.id = candidates.id
RETURNING subscriber.id;
