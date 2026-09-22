-- Normal draft deadlines are scanned from the immutable current draft
-- revision. Swiss BO1 drafts are linked through their planned Wave, while
-- playoff final BO3 drafts are linked through their immutable final-stage
-- record. The tournament and Series predicates keep paused, cancelled, and
-- already materialized rows out of the automatic-action path.
-- name: GetSwissDraftDeadlineIdentity :one
SELECT series.tournament_id,
    series.roster_id
FROM drafts AS draft
INNER JOIN series
    ON series.id = draft.series_id
    AND series.roster_id = draft.roster_id
WHERE draft.id = sqlc.arg(draft_id);

-- The caller takes the Tournament -> Roster/Projection prefix first. This
-- query then locks the draft scope and its current immutable revision, so
-- pause/cancel, final activation, and participant actions serialize before
-- CAS.
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
    AND tournament.state = 'swiss'
    AND series.format = 'bo1'
    AND series.state = 'planned'
    AND draft.format = 'bo1'
    AND wave.state = 'planned'
FOR UPDATE OF series, wave, draft;

-- Final BO3 drafts have no Swiss Wave link before activation. Lock their
-- immutable final-stage authority separately because PostgreSQL does not
-- permit row-locking clauses on a UNION query.
-- name: LockFinalDraftDeadlineCommit :one
SELECT tournament.id AS tournament_id,
    tournament.state AS tournament_state,
    series.id AS series_id,
    series.roster_id,
    series.format AS series_format,
    series.state AS series_state,
    draft.id AS draft_id,
    draft.format AS draft_format,
    final_stage.draft_id AS final_draft_id,
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
INNER JOIN tournament_stage_playoff_finals AS final_stage
    ON final_stage.draft_id = draft.id
    AND final_stage.final_series_id = series.id
    AND final_stage.tournament_id = series.tournament_id
    AND final_stage.roster_id = series.roster_id
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
    AND tournament.state = 'playoffs'
    AND series.format = 'bo3'
    AND series.state = 'planned'
    AND draft.format = 'bo3'
FOR UPDATE OF series, draft, final_stage;

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
LEFT JOIN wave_series
    ON wave_series.series_id = series.id
    AND wave_series.tournament_id = series.tournament_id
    AND wave_series.roster_id = series.roster_id
LEFT JOIN waves AS wave
    ON wave.id = wave_series.wave_id
    AND wave.tournament_id = series.tournament_id
    AND wave.roster_id = series.roster_id
    AND wave.state = 'planned'
LEFT JOIN swiss_wave_links AS swiss_link
    ON swiss_link.wave_id = wave.id
    AND swiss_link.tournament_id = series.tournament_id
    AND swiss_link.roster_id = series.roster_id
LEFT JOIN swiss_rounds AS round
    ON round.id = swiss_link.round_id
    AND round.roster_id = swiss_link.roster_id
INNER JOIN tournaments AS tournament
    ON tournament.id = series.tournament_id
INNER JOIN category_revisions AS category
    ON category.id = draft.category_revision_id
    AND category.series_id = draft.series_id
    AND category.roster_id = draft.roster_id
    AND category.mode = 'draft'
LEFT JOIN tournament_stage_playoff_finals AS final_stage
    ON final_stage.draft_id = draft.id
    AND final_stage.final_series_id = series.id
    AND final_stage.tournament_id = series.tournament_id
    AND final_stage.roster_id = series.roster_id
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
WHERE current_revision.state = 'active'
    AND current_revision.absolute_deadline IS NOT NULL
    AND current_revision.absolute_deadline <= sqlc.arg(observed_at)::TIMESTAMPTZ
    AND (
        (
            tournament.state = 'swiss'
            AND series.format = 'bo1'
            AND series.state = 'planned'
            AND draft.format = 'bo1'
            AND wave.id IS NOT NULL
            AND wave.state = 'planned'
            AND swiss_link.wave_id IS NOT NULL
            AND round.id IS NOT NULL
        )
        OR (
            tournament.state = 'playoffs'
            AND series.format = 'bo3'
            AND series.state = 'planned'
            AND draft.format = 'bo3'
            AND final_stage.draft_id = draft.id
        )
    )
ORDER BY current_revision.absolute_deadline,
    current_revision.draft_id
LIMIT sqlc.arg(batch_size);
