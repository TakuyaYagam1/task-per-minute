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
    source_file_url,
    kind,
    enabled
  )
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
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
  kind,
  enabled,
  current_version,
  created_at,
  updated_at;
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
  kind,
  enabled,
  current_version,
  created_at,
  updated_at
FROM tasks
WHERE id = $1
  AND deleted_at IS NULL;
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
  kind,
  enabled,
  current_version,
  created_at,
  updated_at
FROM tasks
WHERE deleted_at IS NULL
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
  source_file_url = $12,
  kind = $13,
  enabled = $14
WHERE id = $1
  AND deleted_at IS NULL
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
  kind,
  enabled,
  current_version,
  created_at,
  updated_at;
-- name: DeleteTask :exec
UPDATE tasks
SET enabled = false,
  deleted_at = clock_timestamp()
WHERE tasks.id = $1
  AND tasks.deleted_at IS NULL;

-- name: LockTaskForContentMutation :one
SELECT task.id
FROM tasks AS task
WHERE task.id = $1
  AND task.deleted_at IS NULL
FOR UPDATE;

-- name: TaskReferencedByTournament :one
SELECT EXISTS (
  SELECT 1
  FROM assignment_plan_edges AS edge
  WHERE edge.task_id = $1
  UNION ALL
  SELECT 1
  FROM tournament_content_configurations AS configuration
  JOIN task_pool_version_memberships AS membership
    ON membership.task_pool_revision_id IN (
      configuration.normal_pool_revision_id,
      configuration.golden_pool_revision_id
    )
  WHERE configuration.state = 'published'
    AND membership.task_id = $1
  ) AS exists;
