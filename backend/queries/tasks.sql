-- name: CreateTask :one
INSERT INTO tasks (
    title,
    description,
    category,
    difficulty,
    time_limit,
    flag,
    hint_1,
    hint_2,
    hint_3,
    task_url,
    source_file_url
  )
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING id,
  title,
  description,
  category,
  difficulty,
  time_limit,
  flag,
  hint_1,
  hint_2,
  hint_3,
  task_url,
  source_file_url,
  created_at;
-- name: GetTaskByID :one
SELECT id,
  title,
  description,
  category,
  difficulty,
  time_limit,
  flag,
  hint_1,
  hint_2,
  hint_3,
  task_url,
  source_file_url,
  created_at
FROM tasks
WHERE id = $1;
-- name: ListTasks :many
SELECT id,
  title,
  description,
  category,
  difficulty,
  time_limit,
  flag,
  hint_1,
  hint_2,
  hint_3,
  task_url,
  source_file_url,
  created_at
FROM tasks
ORDER BY created_at DESC,
  id DESC;
-- name: ListTasksByDifficulty :many
SELECT id,
  title,
  description,
  category,
  difficulty,
  time_limit,
  flag,
  hint_1,
  hint_2,
  hint_3,
  task_url,
  source_file_url,
  created_at
FROM tasks
WHERE difficulty = $1
  AND btrim(hint_1) <> ''
  AND btrim(hint_2) <> ''
  AND btrim(hint_3) <> ''
ORDER BY created_at DESC,
  id DESC;
-- name: UpdateTask :one
UPDATE tasks
SET title = $2,
  description = $3,
  category = $4,
  difficulty = $5,
  time_limit = $6,
  flag = $7,
  hint_1 = $8,
  hint_2 = $9,
  hint_3 = $10,
  task_url = $11,
  source_file_url = $12
WHERE id = $1
RETURNING id,
  title,
  description,
  category,
  difficulty,
  time_limit,
  flag,
  hint_1,
  hint_2,
  hint_3,
  task_url,
  source_file_url,
  created_at;
-- name: DeleteTask :exec
WITH deleted_history AS (
  DELETE FROM player_task_history
  WHERE task_id = $1
),
deleted_finished_duel_tasks AS (
  DELETE FROM duel_player_tasks dpt
  USING duels d
  WHERE dpt.duel_id = d.id
    AND dpt.task_id = $1
    AND d.status <> 'active'
)
DELETE FROM tasks
WHERE tasks.id = $1;
-- name: TaskInActiveDuel :one
SELECT EXISTS (
    SELECT 1
    FROM duel_player_tasks dpt
      JOIN duels d ON d.id = dpt.duel_id
    WHERE dpt.task_id = $1
      AND d.status = 'active'
  ) AS exists;
-- name: CountTasksByDifficulty :one
SELECT COUNT(*) AS count
FROM tasks
WHERE difficulty = $1
  AND btrim(hint_1) <> ''
  AND btrim(hint_2) <> ''
  AND btrim(hint_3) <> '';
-- name: CountSolvedTasksByDifficulty :one
SELECT COUNT(*) AS count
FROM player_task_history pth
  JOIN tasks t ON t.id = pth.task_id
WHERE pth.player_id = $1
  AND t.difficulty = $2
  AND btrim(t.hint_1) <> ''
  AND btrim(t.hint_2) <> ''
  AND btrim(t.hint_3) <> '';
