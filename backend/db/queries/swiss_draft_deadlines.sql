-- Swiss BO1 draft deadlines are scanned from the immutable current draft
-- revision.  The Swiss Wave link excludes playoff drafts, while the
-- tournament and Series predicates keep paused, cancelled, and already
-- materialized rows out of the automatic-action path.
-- name: GetSwissDraftDeadlineIdentity :one
SELECT series.tournament_id,
    series.roster_id
FROM drafts AS draft
INNER JOIN series
    ON series.id = draft.series_id
    AND series.roster_id = draft.roster_id
WHERE draft.id = sqlc.arg(draft_id);

-- The caller takes the Tournament -> Roster/Projection prefix first. This
-- query then locks only the Swiss draft scope and its current immutable
-- revision, so pause/cancel and participant actions serialize before CAS.
-- name: LockSwissDraftDeadlineCommit :one
SELECT tournament.id AS tournament_id,
    tournament.state AS tournament_state,
    series.id AS series_id,
    series.roster_id,
    series.format AS series_format,
    series.state AS series_state,
    wave.id AS wave_id,
    wave.state AS wave_state,
    draft.id AS draft_id,
    draft.format AS draft_format,
    revision.id AS revision_id,
    revision.revision,
    revision.service_epoch,
    revision.state AS revision_state,
    revision.turn_number,
    revision.absolute_deadline
FROM tournaments AS tournament
INNER JOIN series
    ON series.tournament_id = tournament.id
    AND series.tournament_id = sqlc.arg(tournament_id)
    AND series.roster_id = sqlc.arg(roster_id)
INNER JOIN drafts AS draft
    ON draft.series_id = series.id
    AND draft.roster_id = series.roster_id
INNER JOIN wave_series AS member
    ON member.series_id = series.id
    AND member.tournament_id = series.tournament_id
    AND member.roster_id = series.roster_id
INNER JOIN waves AS wave
    ON wave.id = member.wave_id
    AND wave.tournament_id = series.tournament_id
    AND wave.roster_id = series.roster_id
INNER JOIN swiss_wave_links AS link
    ON link.wave_id = wave.id
    AND link.tournament_id = series.tournament_id
    AND link.roster_id = series.roster_id
INNER JOIN LATERAL (
    SELECT current_revision.id,
        current_revision.revision,
        current_revision.service_epoch,
        current_revision.state,
        current_revision.turn_number,
        current_revision.absolute_deadline
    FROM draft_revisions AS current_revision
    WHERE current_revision.draft_id = draft.id
        AND current_revision.series_id = draft.series_id
        AND current_revision.roster_id = draft.roster_id
    ORDER BY current_revision.revision DESC,
        current_revision.id DESC
    LIMIT 1
    FOR UPDATE OF current_revision
) AS revision ON TRUE
WHERE draft.id = sqlc.arg(draft_id)
    AND series.id = draft.series_id
    AND series.roster_id = draft.roster_id
FOR UPDATE OF series, wave, draft;

-- name: ListDueSwissDraftDeadlines :many
SELECT current_revision.draft_id,
    current_revision.id AS draft_revision_id,
    current_revision.revision AS draft_revision,
    current_revision.service_epoch,
    current_revision.turn_number,
    current_revision.absolute_deadline
FROM drafts AS draft
INNER JOIN series
    ON series.id = draft.series_id
    AND series.roster_id = draft.roster_id
    AND series.format = 'bo1'
    AND series.state = 'planned'
INNER JOIN wave_series
    ON wave_series.series_id = series.id
    AND wave_series.tournament_id = series.tournament_id
    AND wave_series.roster_id = series.roster_id
INNER JOIN waves AS wave
    ON wave.id = wave_series.wave_id
    AND wave.tournament_id = series.tournament_id
    AND wave.roster_id = series.roster_id
    AND wave.state = 'planned'
INNER JOIN swiss_wave_links AS swiss_link
    ON swiss_link.wave_id = wave.id
    AND swiss_link.tournament_id = series.tournament_id
    AND swiss_link.roster_id = series.roster_id
INNER JOIN swiss_rounds AS round
    ON round.id = swiss_link.round_id
    AND round.roster_id = swiss_link.roster_id
INNER JOIN tournaments AS tournament
    ON tournament.id = series.tournament_id
    AND tournament.state = 'swiss'
INNER JOIN category_revisions AS category
    ON category.id = draft.category_revision_id
    AND category.series_id = draft.series_id
    AND category.roster_id = draft.roster_id
    AND category.mode = 'draft'
INNER JOIN LATERAL (
    SELECT revision.draft_id,
        revision.id,
        revision.revision,
        revision.service_epoch,
        revision.turn_number,
        revision.state,
        revision.absolute_deadline
    FROM draft_revisions AS revision
    WHERE revision.draft_id = draft.id
        AND revision.series_id = draft.series_id
        AND revision.roster_id = draft.roster_id
    ORDER BY revision.revision DESC,
        revision.id DESC
    LIMIT 1
) AS current_revision ON TRUE
WHERE draft.format = 'bo1'
    AND current_revision.state = 'active'
    AND current_revision.absolute_deadline IS NOT NULL
    AND current_revision.absolute_deadline <= sqlc.arg(observed_at)::TIMESTAMPTZ
ORDER BY current_revision.absolute_deadline,
    current_revision.draft_id
LIMIT sqlc.arg(batch_size);
