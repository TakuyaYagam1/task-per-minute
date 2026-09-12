-- name: LockSwissDraftCompletion :one
SELECT target.id AS series_id,
    target.tournament_id,
    target.roster_id,
    target.state AS series_state,
    target.first_participant_id,
    target.second_participant_id,
    category.id AS category_revision_id,
    category.revision AS category_revision,
    category.created_at AS category_created_at,
    category.source_pool_revision_id,
    clock_timestamp()::TIMESTAMPTZ AS completed_at
FROM drafts AS draft
INNER JOIN series AS target
    ON target.id = draft.series_id AND target.roster_id = draft.roster_id
INNER JOIN category_revisions AS category
    ON category.id = draft.category_revision_id
    AND category.series_id = target.id AND category.roster_id = target.roster_id
INNER JOIN wave_series AS member
    ON member.series_id = target.id AND member.roster_id = target.roster_id
    AND member.tournament_id = target.tournament_id
INNER JOIN swiss_wave_links AS link
    ON link.wave_id = member.wave_id AND link.roster_id = target.roster_id
    AND link.tournament_id = target.tournament_id
WHERE draft.id = sqlc.arg(draft_id)
    AND draft.format = 'bo1' AND target.format = 'bo1'
    AND category.mode = 'draft'
FOR UPDATE OF draft, target, category, member, link;

-- name: HasWavePendingDrafts :one
SELECT EXISTS (
    SELECT 1
    FROM wave_series AS member
    INNER JOIN drafts AS draft
        ON draft.series_id = member.series_id AND draft.roster_id = member.roster_id
    INNER JOIN LATERAL (
        SELECT revision.state
        FROM draft_revisions AS revision
        WHERE revision.draft_id = draft.id
        ORDER BY revision.revision DESC
        LIMIT 1
    ) AS current_revision ON true
    WHERE member.wave_id = sqlc.arg(wave_id)
        AND current_revision.state NOT IN ('completed', 'superseded')
);
