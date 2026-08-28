-- name: ListArenaAuditPage :many
SELECT audit.id AS audit_event_id,
    audit.tournament_id,
    audit.roster_id,
    audit.series_id,
    audit.result_event_id,
    audit.actor_kind,
    audit.actor_id,
    audit.action AS event_type,
    jsonb_strip_nulls(jsonb_build_object(
        'attempt_id', audit.payload -> 'attempt_id',
        'entity_id', audit.payload -> 'entity_id',
        'entity_kind', audit.payload -> 'entity_kind',
        'previous_revision_id', audit.payload -> 'previous_revision_id',
        'projection_revision_id', audit.payload -> 'projection_revision_id',
        'reason', audit.payload -> 'reason',
        'result_reason', audit.payload -> 'result_reason',
        'revision_number', audit.payload -> 'revision_number',
        'series_id', audit.payload -> 'series_id',
        'source_projection_revision_id', audit.payload -> 'source_projection_revision_id',
        'state', audit.payload -> 'state',
        'tournament_id', audit.payload -> 'tournament_id',
        'winner_id', audit.payload -> 'winner_id'
    )) AS redacted_payload,
    audit.occurred_at,
    audit.created_at,
    result_event.result_state,
    result_event.result_reason,
    result_event.winner_id,
    revision.id AS official_result_revision_id,
    revision.entity_kind,
    revision.entity_id,
    revision.revision_number,
    head.current_revision_id = revision.id AS is_current
FROM arena_audit_events AS audit
INNER JOIN arena_result_events AS result_event
    ON result_event.id = audit.result_event_id
INNER JOIN arena_official_result_revisions AS revision
    ON revision.result_event_id = result_event.id
LEFT JOIN arena_official_result_heads AS head
    ON head.entity_kind = revision.entity_kind
    AND head.entity_id = revision.entity_id
WHERE audit.tournament_id = sqlc.arg(tournament_id)
    AND (
        sqlc.narg(entity_kind)::TEXT IS NULL
        OR revision.entity_kind = sqlc.narg(entity_kind)::TEXT
    )
    AND (
        sqlc.narg(entity_id)::UUID IS NULL
        OR revision.entity_id = sqlc.narg(entity_id)::UUID
    )
    AND (
        sqlc.narg(event_type)::TEXT IS NULL
        OR audit.action = sqlc.narg(event_type)::TEXT
    )
    AND (
        sqlc.narg(actor_kind)::TEXT IS NULL
        OR audit.actor_kind = sqlc.narg(actor_kind)::TEXT
    )
    AND (
        sqlc.narg(actor_id)::UUID IS NULL
        OR audit.actor_id = sqlc.narg(actor_id)::UUID
    )
    AND (
        sqlc.narg(result_reason)::TEXT IS NULL
        OR result_event.result_reason = sqlc.narg(result_reason)::TEXT
    )
    AND (
        sqlc.narg(occurred_from)::TIMESTAMPTZ IS NULL
        OR audit.occurred_at >= sqlc.narg(occurred_from)::TIMESTAMPTZ
    )
    AND (
        sqlc.narg(occurred_to)::TIMESTAMPTZ IS NULL
        OR audit.occurred_at <= sqlc.narg(occurred_to)::TIMESTAMPTZ
    )
    AND (
        sqlc.narg(cursor_occurred_at)::TIMESTAMPTZ IS NULL
        OR (
            audit.occurred_at,
            audit.id,
            revision.id
        ) < (
            sqlc.narg(cursor_occurred_at)::TIMESTAMPTZ,
            sqlc.narg(cursor_audit_event_id)::UUID,
            sqlc.narg(cursor_revision_id)::UUID
        )
    )
ORDER BY audit.occurred_at DESC, audit.id DESC, revision.id DESC
LIMIT sqlc.arg(page_limit);
