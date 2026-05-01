-- name: CreateDuelPlayerTask :exec
INSERT INTO duel_player_tasks (duel_id, player_id, task_id)
VALUES ($1, $2, $3);
-- name: GetDuelPlayerTask :one
SELECT duel_id,
    player_id,
    task_id,
    solved,
    solved_at
FROM duel_player_tasks
WHERE duel_id = $1
    AND player_id = $2;
-- name: GetPlayerTask :one
SELECT t.id,
    t.title,
    t.description,
    t.category,
    t.difficulty,
    t.time_limit,
    t.flag,
    t.hint_1,
    t.hint_2,
    t.hint_3,
    t.task_url,
    t.source_file_url,
    t.created_at
FROM duel_player_tasks dpt
    JOIN tasks t ON t.id = dpt.task_id
WHERE dpt.duel_id = $1
    AND dpt.player_id = $2;
-- name: MarkDuelPlayerTaskSolved :exec
UPDATE duel_player_tasks
SET solved = TRUE,
    solved_at = $3
WHERE duel_id = $1
    AND player_id = $2;
