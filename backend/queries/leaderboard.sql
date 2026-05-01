-- name: TotalSolveTimePerPlayer :many
-- Aggregates total solve time (ms) across all duels each player has won.
-- Ordered ASC: faster total time → higher rank. Used as a Redis-leaderboard
-- tiebreaker when two players share the same win count.
SELECT p.id AS player_id,
    p.username AS username,
    COALESCE(
        SUM(
            (
                EXTRACT(
                    EPOCH
                    FROM dpt.solved_at - d.started_at
                ) * 1000
            )::BIGINT
        ),
        0
    )::BIGINT AS total_solve_time_ms
FROM players p
    JOIN duels d ON d.winner_id = p.id
    AND d.status = 'finished'
    JOIN duel_player_tasks dpt ON dpt.duel_id = d.id
    AND dpt.player_id = p.id
    AND dpt.solved = TRUE
GROUP BY p.id,
    p.username
ORDER BY total_solve_time_ms ASC,
    p.username ASC;
-- name: TotalSolveTimeForPlayers :many
-- Batch variant used by the leaderboard usecase for Redis usernames.
SELECT p.id AS player_id,
    p.username AS username,
    COALESCE(
        SUM(
            (
                EXTRACT(
                    EPOCH
                    FROM dpt.solved_at - d.started_at
                ) * 1000
            )::BIGINT
        ),
        0
    )::BIGINT AS total_solve_time_ms
FROM players p
    LEFT JOIN duels d ON d.winner_id = p.id
    AND d.status = 'finished'
    LEFT JOIN duel_player_tasks dpt ON dpt.duel_id = d.id
    AND dpt.player_id = p.id
    AND dpt.solved = TRUE
WHERE p.username = ANY($1::text [])
GROUP BY p.id,
    p.username
ORDER BY total_solve_time_ms ASC,
    p.username ASC;
