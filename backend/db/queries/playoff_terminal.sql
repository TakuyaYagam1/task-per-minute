-- The terminal playoff coordinator owns only normalized stage evidence and
-- graph creation. Result settlement and final projection publication retain
-- their existing repositories and execute in the caller's outer transaction.

-- name: LockPostseasonFinalNormalPool :one
SELECT pool.id
FROM tournament_content_configurations AS configuration
INNER JOIN tournament_category_pool_revisions AS category_pool
    ON category_pool.configuration_id = configuration.id
    AND category_pool.id = sqlc.arg(category_pool_id)
    AND category_pool.format = 'bo3'
INNER JOIN task_pool_revisions AS pool
    ON pool.id = configuration.normal_pool_revision_id
    AND pool.kind = 'normal'
WHERE configuration.tournament_id = sqlc.arg(tournament_id)
    AND configuration.revision = sqlc.arg(content_revision)
    AND configuration.state = 'published'
FOR KEY SHARE OF configuration, category_pool, pool;

-- name: LockPostseasonSemifinalAuthority :many
SELECT evidence.command_id,
    evidence.tournament_id,
    evidence.roster_id,
    evidence.published_projection_revision_id,
    evidence.published_projection_revision,
    evidence.bracket_node_id,
    semifinal.position,
    semifinal.series_id,
    semifinal.bracket_artifact_id,
    series.first_participant_id,
    series.second_participant_id,
    series.format,
    series.state,
    series.first_participant_wins,
    series.second_participant_wins,
    series.winner_id,
    series.current_score_revision_id,
    series.current_result_revision_id,
    series.revision AS series_revision,
    score_head.current_revision_id AS score_head_revision_id,
    score_head.revision AS score_head_revision,
    result_head.current_revision_id AS result_head_revision_id,
    result_head.revision AS result_head_revision,
    result.id AS completed_result_revision_id,
    result.created_at AS completed_at
FROM tournament_stage_playoff_evidence AS evidence
INNER JOIN tournament_stage_playoff_semifinals AS semifinal
    ON semifinal.command_id = evidence.command_id
    AND semifinal.tournament_id = evidence.tournament_id
INNER JOIN series
    ON series.id = semifinal.series_id
    AND series.tournament_id = evidence.tournament_id
    AND series.roster_id = evidence.roster_id
INNER JOIN series_score_heads AS score_head
    ON score_head.series_id = series.id
    AND score_head.roster_id = series.roster_id
INNER JOIN official_result_heads AS result_head
    ON result_head.entity_kind = 'series'
    AND result_head.entity_id = series.id
    AND result_head.series_id = series.id
    AND result_head.roster_id = series.roster_id
INNER JOIN official_result_revisions AS result
    ON result.id = result_head.current_revision_id
    AND result.tournament_id = evidence.tournament_id
    AND result.roster_id = evidence.roster_id
    AND result.entity_kind = 'series'
    AND result.entity_id = series.id
    AND result.series_id = series.id
    AND result.revision_number = result_head.revision
    AND result.result_state = 'completed'
    AND result.winner_id = series.winner_id
WHERE evidence.tournament_id = sqlc.arg(tournament_id)
    AND series.state = 'completed'
    AND series.current_score_revision_id = score_head.current_revision_id
    AND series.current_result_revision_id = result_head.current_revision_id
    AND score_head.revision > 0
    AND result_head.revision > 0
    AND EXISTS (
        SELECT 1
        FROM tournament_stage_playoff_semifinals AS settled
        WHERE settled.command_id = evidence.command_id
            AND settled.tournament_id = evidence.tournament_id
            AND settled.roster_id = evidence.roster_id
            AND settled.series_id = sqlc.arg(series_id)
    )
    AND NOT EXISTS (
        SELECT 1
        FROM tournament_stage_playoff_semifinals AS required_semifinal
        INNER JOIN series AS required_series
            ON required_series.id = required_semifinal.series_id
            AND required_series.tournament_id = evidence.tournament_id
            AND required_series.roster_id = evidence.roster_id
        LEFT JOIN series_score_heads AS required_score_head
            ON required_score_head.series_id = required_series.id
            AND required_score_head.roster_id = required_series.roster_id
        LEFT JOIN official_result_heads AS required_result_head
            ON required_result_head.entity_kind = 'series'
            AND required_result_head.entity_id = required_series.id
            AND required_result_head.series_id = required_series.id
            AND required_result_head.roster_id = required_series.roster_id
        WHERE required_semifinal.command_id = evidence.command_id
            AND required_semifinal.tournament_id = evidence.tournament_id
            AND required_semifinal.roster_id = evidence.roster_id
            AND (
                required_series.state <> 'completed'
                OR required_series.current_score_revision_id IS NULL
                OR required_series.current_result_revision_id IS NULL
                OR required_score_head.current_revision_id IS NULL
                OR required_result_head.current_revision_id IS NULL
                OR required_series.current_score_revision_id <> required_score_head.current_revision_id
                OR required_series.current_result_revision_id <> required_result_head.current_revision_id
                OR required_score_head.revision IS NULL
                OR required_score_head.revision < 1
                OR required_result_head.revision IS NULL
                OR required_result_head.revision < 1
            )
    )
ORDER BY semifinal.position
FOR UPDATE OF evidence, semifinal, series, score_head, result_head, result;

-- name: LockPostseasonFinalStage :one
SELECT stage.command_id,
    stage.tournament_id,
    stage.roster_id,
    stage.final_series_id,
    stage.category_revision_id,
    stage.draft_id,
    stage.draft_initial_revision_id,
    stage.first_participant_id,
    stage.second_participant_id,
    evidence.published_projection_revision_id,
    evidence.published_projection_revision,
    evidence.bracket_node_id,
    final_series.state AS series_state,
    final_series.revision AS series_revision,
    final_series.current_score_revision_id,
    final_series.current_result_revision_id,
    draft_revision.id AS current_draft_revision_id,
    draft_revision.revision AS current_draft_revision,
    draft_revision.state AS current_draft_state,
    draft_revision.created_at AS current_draft_created_at,
    score_head.current_revision_id AS score_head_revision_id,
    score_head.revision AS score_head_revision,
    stage.created_at
FROM tournament_stage_playoff_finals AS stage
INNER JOIN tournament_stage_playoff_evidence AS evidence
    ON evidence.command_id = stage.command_id
    AND evidence.tournament_id = stage.tournament_id
    AND evidence.roster_id = stage.roster_id
INNER JOIN series AS final_series
    ON final_series.id = stage.final_series_id
    AND final_series.tournament_id = stage.tournament_id
    AND final_series.roster_id = stage.roster_id
INNER JOIN LATERAL (
    SELECT revision.id,
        revision.revision,
        revision.state,
        revision.created_at
    FROM draft_revisions AS revision
    WHERE revision.draft_id = stage.draft_id
    ORDER BY revision.revision DESC
    LIMIT 1
    FOR UPDATE
) AS draft_revision ON true
LEFT JOIN series_score_heads AS score_head
    ON score_head.series_id = final_series.id
    AND score_head.roster_id = final_series.roster_id
WHERE stage.tournament_id = sqlc.arg(tournament_id)
    AND stage.final_series_id = sqlc.arg(series_id)
FOR UPDATE OF stage, evidence, final_series;

-- name: LockPostseasonFinalAdvancements :many
SELECT advancement.position,
    advancement.semifinal_series_id,
    advancement.winner_id,
    advancement.loser_id,
    advancement.score_revision_id,
    advancement.result_revision_id
FROM tournament_stage_playoff_final_advancements AS advancement
WHERE advancement.command_id = sqlc.arg(command_id)
    AND advancement.tournament_id = sqlc.arg(tournament_id)
ORDER BY advancement.position
FOR KEY SHARE;

-- name: LockPostseasonFinalInitialization :one
SELECT initialization.command_id,
    initialization.tournament_id,
    initialization.roster_id,
    initialization.final_series_id,
    initialization.draft_id,
    initialization.completed_draft_revision_id,
    initialization.initial_score_revision_id,
    initialization.first_slot_id,
    initialization.first_game_id,
    initialization.first_wave_id,
    initialization.first_wave_revision_id,
    initialization.first_assignment_id,
    initialization.created_at
FROM tournament_stage_playoff_final_initializations AS initialization
WHERE initialization.command_id = sqlc.arg(command_id)
    AND initialization.tournament_id = sqlc.arg(tournament_id)
FOR UPDATE;

-- name: LockPostseasonFinalScoreHistory :many
SELECT score_revision.id,
    score_revision.tournament_id,
    score_revision.roster_id,
    score_revision.series_id,
    score_revision.result_event_id,
    score_revision.previous_revision_id,
    score_revision.revision_number,
    score_revision.operation,
    score_revision.first_participant_wins,
    score_revision.second_participant_wins,
    score_revision.created_at
FROM series_score_revisions AS score_revision
WHERE score_revision.series_id = sqlc.arg(series_id)
    AND score_revision.roster_id = sqlc.arg(roster_id)
ORDER BY score_revision.revision_number
FOR KEY SHARE;

-- name: LockPostseasonFinalProgressions :many
SELECT progression.command_id,
    progression.tournament_id,
    progression.roster_id,
    progression.final_series_id,
    progression.source_score_revision_id,
    progression.source_game_result_revision_id,
    progression.next_position,
    progression.next_slot_id,
    progression.next_game_id,
    progression.next_wave_id,
    progression.next_wave_revision_id,
    progression.next_assignment_id,
    progression.created_at
FROM tournament_stage_playoff_final_progressions AS progression
WHERE progression.command_id = sqlc.arg(command_id)
    AND progression.tournament_id = sqlc.arg(tournament_id)
ORDER BY progression.next_position
FOR UPDATE;

-- name: CreatePostseasonFinalAdvancement :exec
INSERT INTO tournament_stage_playoff_final_advancements (
    command_id,
    tournament_id,
    roster_id,
    position,
    semifinal_series_id,
    winner_id,
    loser_id,
    score_revision_id,
    result_revision_id,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(position),
    sqlc.arg(semifinal_series_id),
    sqlc.arg(winner_id),
    sqlc.arg(loser_id),
    sqlc.arg(score_revision_id),
    sqlc.arg(result_revision_id),
    sqlc.arg(created_at)
);

-- name: CreatePostseasonFinalStage :exec
INSERT INTO tournament_stage_playoff_finals (
    command_id,
    tournament_id,
    roster_id,
    final_series_id,
    category_revision_id,
    draft_id,
    draft_initial_revision_id,
    first_participant_id,
    second_participant_id,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(final_series_id),
    sqlc.arg(category_revision_id),
    sqlc.arg(draft_id),
    sqlc.arg(draft_initial_revision_id),
    sqlc.arg(first_participant_id),
    sqlc.arg(second_participant_id),
    sqlc.arg(created_at)
);

-- name: CreatePostseasonInitialScoreHead :exec
INSERT INTO series_score_heads (
    series_id,
    roster_id,
    current_revision_id,
    revision,
    updated_at
)
VALUES (
    sqlc.arg(series_id),
    sqlc.arg(roster_id),
    sqlc.arg(initial_score_revision_id),
    1,
    sqlc.arg(updated_at)
);

-- name: ActivatePostseasonFinalSeriesCAS :one
UPDATE series
SET state = 'active',
    current_score_revision_id = sqlc.arg(initial_score_revision_id),
    revision = revision + 1,
    updated_at = sqlc.arg(activated_at),
    started_at = sqlc.arg(activated_at)
WHERE id = sqlc.arg(series_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND state = 'planned'
    AND revision = sqlc.arg(expected_series_revision)
    AND current_score_revision_id = sqlc.arg(initial_score_revision_id)
    AND current_result_revision_id IS NULL
RETURNING revision;

-- name: LockPostseasonFinalGenesis :one
SELECT score.id
FROM tournament_stage_playoff_finals AS stage
JOIN tournament_stage_playoff_evidence AS evidence ON evidence.command_id = stage.command_id
    AND evidence.tournament_id = stage.tournament_id AND evidence.roster_id = stage.roster_id
JOIN series AS final_series ON final_series.id = stage.final_series_id
    AND final_series.tournament_id = stage.tournament_id AND final_series.roster_id = stage.roster_id
JOIN series_score_heads AS head ON head.series_id = final_series.id AND head.roster_id = stage.roster_id
JOIN series_score_revisions AS score ON score.id = head.current_revision_id
    AND score.series_id = final_series.id AND score.roster_id = stage.roster_id
WHERE stage.final_series_id = sqlc.arg(series_id)
    AND stage.tournament_id = sqlc.arg(tournament_id)
    AND score.id = sqlc.arg(initial_score_revision_id)
    AND head.revision = 1 AND score.revision_number = 1
    AND score.operation = 'initialize' AND score.result_event_id IS NULL
    AND score.previous_revision_id IS NULL AND score.command_attempt_id IS NULL
    AND score.first_participant_wins = 0 AND score.second_participant_wins = 0
    AND score.command_id = stage.command_id
    AND score.source_projection_revision_id = evidence.published_projection_revision_id
    AND score.source_projection_revision = evidence.published_projection_revision
    AND final_series.current_score_revision_id = score.id
    AND final_series.current_result_revision_id IS NULL
FOR UPDATE OF head, score;

-- name: CreatePostseasonPlannedWave :exec
INSERT INTO waves (
    id,
    tournament_id,
    roster_id,
    revision_id,
    revision,
    state,
    replaces_wave_id,
    created_at,
    updated_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(revision_id),
    1,
    'planned',
    NULL,
    sqlc.arg(created_at),
    sqlc.arg(created_at)
);

-- name: CreatePostseasonFinalInitialization :exec
INSERT INTO tournament_stage_playoff_final_initializations (
    command_id,
    tournament_id,
    roster_id,
    final_series_id,
    draft_id,
    completed_draft_revision_id,
    initial_score_revision_id,
    first_slot_id,
    first_game_id,
    first_wave_id,
    first_wave_revision_id,
    first_assignment_id,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(final_series_id),
    sqlc.arg(draft_id),
    sqlc.arg(completed_draft_revision_id),
    sqlc.arg(initial_score_revision_id),
    sqlc.arg(first_slot_id),
    sqlc.arg(first_game_id),
    sqlc.arg(first_wave_id),
    sqlc.arg(first_wave_revision_id),
    sqlc.arg(first_assignment_id),
    sqlc.arg(created_at)
);

-- name: CreatePostseasonFinalProgression :exec
INSERT INTO tournament_stage_playoff_final_progressions (
    command_id,
    tournament_id,
    roster_id,
    final_series_id,
    source_score_revision_id,
    source_game_result_revision_id,
    next_position,
    next_slot_id,
    next_game_id,
    next_wave_id,
    next_wave_revision_id,
    next_assignment_id,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(final_series_id),
    sqlc.arg(source_score_revision_id),
    sqlc.arg(source_game_result_revision_id),
    sqlc.arg(next_position),
    sqlc.arg(next_slot_id),
    sqlc.arg(next_game_id),
    sqlc.arg(next_wave_id),
    sqlc.arg(next_wave_revision_id),
    sqlc.arg(next_assignment_id),
    sqlc.arg(created_at)
);
