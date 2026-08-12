-- name: CreateDuel :one
INSERT INTO duels (player1_id, player2_id, deadline)
VALUES ($1, $2, $3)
RETURNING id,
  player1_id,
  player2_id,
  status,
  winner_id,
  deadline,
  started_at,
  finished_at;
-- name: GetDuelByID :one
SELECT id,
  player1_id,
  player2_id,
  status,
  winner_id,
  deadline,
  started_at,
  finished_at
FROM duels
WHERE id = $1;
-- name: GetActiveDuelByPlayerID :one
SELECT id,
  player1_id,
  player2_id,
  status,
  winner_id,
  deadline,
  started_at,
  finished_at
FROM duels
WHERE status = 'active'
  AND (
    player1_id = $1
    OR player2_id = $1
  )
LIMIT 1;
-- name: UpdateDuelDeadline :one
UPDATE duels
SET deadline = $2
WHERE id = $1
  AND status = 'active'
RETURNING id,
  player1_id,
  player2_id,
  status,
  winner_id,
  deadline,
  started_at,
  finished_at;
-- name: FinishDuel :one
UPDATE duels
SET status = $4,
  winner_id = $2,
  finished_at = $3
WHERE id = $1
  AND status = 'active'
RETURNING id,
  player1_id,
  player2_id,
  status,
  winner_id,
  deadline,
  started_at,
  finished_at;
-- name: ListActiveDuels :many
SELECT id,
  player1_id,
  player2_id,
  status,
  winner_id,
  deadline,
  started_at,
  finished_at
FROM duels
WHERE status = 'active'
ORDER BY started_at ASC,
  id ASC;
