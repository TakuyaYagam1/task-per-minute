-- name: AddSolvedTask :exec
INSERT INTO player_task_history (player_id, task_id, solved_at)
VALUES ($1, $2, $3) ON CONFLICT (player_id, task_id) DO NOTHING;
-- name: ListSolvedTaskIDs :many
SELECT task_id
FROM player_task_history
WHERE player_id = $1
ORDER BY solved_at DESC;
-- name: SelectUnsolvedTaskByDifficulty :one
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
WHERE difficulty = $2
  AND btrim(hint_1) <> ''
  AND btrim(hint_2) <> ''
  AND btrim(hint_3) <> ''
  AND id NOT IN (
    SELECT task_id
    FROM player_task_history
    WHERE player_id = $1
  )
ORDER BY RANDOM()
LIMIT 1;
-- name: SelectAnyTaskByDifficulty :one
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
ORDER BY RANDOM()
LIMIT 1;
