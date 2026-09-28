-- name: ListPublicTournamentCatalog :many
WITH classified AS (
    SELECT tournament.id AS tournament_id,
        tournament.public_id,
        tournament.name,
        LOWER(tournament.name) AS order_name,
        tournament.preset,
        tournament.state,
        tournament.paused_from_state,
        tournament.planned_roster_size,
        tournament.created_at,
        tournament.started_at,
        tournament.finished_at,
        roster.id AS roster_id,
        COUNT(participant.id)::BIGINT AS roster_size,
        CASE
            WHEN tournament.state IN ('swiss', 'golden', 'playoffs', 'technical_pause') THEN 'live'
            WHEN tournament.state IN ('registration', 'roster_locked') THEN 'upcoming'
            WHEN tournament.state IN ('completed', 'cancelled') THEN 'completed'
        END::VARCHAR AS public_group,
        CASE
            WHEN tournament.state IN ('swiss', 'golden', 'playoffs', 'technical_pause') THEN 0
            WHEN tournament.state IN ('registration', 'roster_locked') THEN 1
            WHEN tournament.state IN ('completed', 'cancelled') THEN 2
        END::INTEGER AS public_group_rank,
        CASE
            WHEN tournament.state = 'technical_pause' THEN tournament.paused_from_state
            ELSE tournament.state
        END::VARCHAR AS public_stage
    FROM tournaments AS tournament
    INNER JOIN rosters AS roster ON roster.tournament_id = tournament.id
    LEFT JOIN participants AS participant ON participant.roster_id = roster.id
    WHERE tournament.state <> 'draft'
        AND tournament.deleted_at IS NULL
    GROUP BY tournament.id,
        roster.id
), visible AS (
    SELECT classified.*
    FROM classified
    WHERE (
        sqlc.arg(search)::TEXT = ''
        OR strpos(classified.order_name, LOWER(sqlc.arg(search)::TEXT)) > 0
        OR strpos(LOWER(classified.public_id), LOWER(sqlc.arg(search)::TEXT)) > 0
    )
    AND (
        sqlc.arg(catalog_group)::TEXT = 'all'
        OR classified.public_group = sqlc.arg(catalog_group)::TEXT
    )
)
SELECT visible.tournament_id,
    visible.public_id,
    visible.name,
    visible.order_name,
    visible.preset,
    visible.state,
    visible.public_group,
    visible.public_stage,
    visible.planned_roster_size,
    visible.roster_size,
    visible.created_at,
    visible.started_at,
    visible.finished_at,
    NULL::TIMESTAMPTZ AS scheduled_at
FROM visible
WHERE (
    NOT sqlc.arg(has_cursor)::BOOLEAN
    OR (
        sqlc.arg(sort)::TEXT = 'activity'
        AND (
            visible.public_group_rank > sqlc.arg(cursor_group_rank)::INTEGER
            OR (
                visible.public_group_rank = sqlc.arg(cursor_group_rank)::INTEGER
                AND (
                    visible.created_at < sqlc.arg(cursor_created_at)::TIMESTAMPTZ
                    OR (
                        visible.created_at = sqlc.arg(cursor_created_at)::TIMESTAMPTZ
                        AND visible.tournament_id > sqlc.arg(cursor_id)::UUID
                    )
                )
            )
        )
    )
    OR (
        sqlc.arg(sort)::TEXT = 'name'
        AND (
            visible.order_name > LOWER(sqlc.arg(cursor_name)::TEXT)
            OR (
                visible.order_name = LOWER(sqlc.arg(cursor_name)::TEXT)
                AND (
                    visible.created_at < sqlc.arg(cursor_created_at)::TIMESTAMPTZ
                    OR (
                        visible.created_at = sqlc.arg(cursor_created_at)::TIMESTAMPTZ
                        AND visible.tournament_id > sqlc.arg(cursor_id)::UUID
                    )
                )
            )
        )
    )
    OR (
        sqlc.arg(sort)::TEXT = 'newest'
        AND (
            visible.created_at < sqlc.arg(cursor_created_at)::TIMESTAMPTZ
            OR (
                visible.created_at = sqlc.arg(cursor_created_at)::TIMESTAMPTZ
                AND visible.tournament_id > sqlc.arg(cursor_id)::UUID
            )
        )
    )
)
ORDER BY
    CASE WHEN sqlc.arg(sort)::TEXT = 'activity' THEN visible.public_group_rank END ASC NULLS LAST,
    CASE WHEN sqlc.arg(sort)::TEXT = 'activity' THEN visible.created_at END DESC NULLS LAST,
    CASE WHEN sqlc.arg(sort)::TEXT = 'name' THEN visible.order_name END ASC NULLS LAST,
    CASE WHEN sqlc.arg(sort)::TEXT IN ('name', 'newest') THEN visible.created_at END DESC NULLS LAST,
    visible.tournament_id ASC
    LIMIT sqlc.arg(page_limit)::INTEGER;

-- name: GetPublicTournamentCatalogItem :one
WITH classified AS (
    SELECT tournament.id AS tournament_id,
        tournament.public_id,
        tournament.name,
        LOWER(tournament.name) AS order_name,
        tournament.preset,
        tournament.state,
        tournament.paused_from_state,
        tournament.planned_roster_size,
        tournament.created_at,
        tournament.started_at,
        tournament.finished_at,
        roster.id AS roster_id,
        COUNT(participant.id)::BIGINT AS roster_size,
        CASE
            WHEN tournament.state IN ('swiss', 'golden', 'playoffs', 'technical_pause') THEN 'live'
            WHEN tournament.state IN ('registration', 'roster_locked') THEN 'upcoming'
            WHEN tournament.state IN ('completed', 'cancelled') THEN 'completed'
        END::VARCHAR AS public_group,
        CASE
            WHEN tournament.state IN ('swiss', 'golden', 'playoffs', 'technical_pause') THEN 0
            WHEN tournament.state IN ('registration', 'roster_locked') THEN 1
            WHEN tournament.state IN ('completed', 'cancelled') THEN 2
        END::INTEGER AS public_group_rank,
        CASE
            WHEN tournament.state = 'technical_pause' THEN tournament.paused_from_state
            ELSE tournament.state
        END::VARCHAR AS public_stage
    FROM tournaments AS tournament
    INNER JOIN rosters AS roster ON roster.tournament_id = tournament.id
    LEFT JOIN participants AS participant ON participant.roster_id = roster.id
    WHERE tournament.state <> 'draft'
        AND tournament.deleted_at IS NULL
        AND tournament.public_id = sqlc.arg(public_id)::VARCHAR
    GROUP BY tournament.id,
        roster.id
)
SELECT classified.tournament_id,
    classified.public_id,
    classified.name,
    classified.order_name,
    classified.preset,
    classified.state,
    classified.public_group,
    classified.public_stage,
    classified.planned_roster_size,
    classified.roster_size,
    classified.created_at,
    classified.started_at,
    classified.finished_at,
    NULL::TIMESTAMPTZ AS scheduled_at
FROM classified;
