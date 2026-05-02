package persistent

import (
	"context"
	"fmt"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
)

type LeaderboardRow = usecase.LeaderboardPlayerTime

type LeaderboardPostgres struct {
	tx *TxManager
}

func NewLeaderboardPostgres(tx *TxManager) *LeaderboardPostgres {
	return &LeaderboardPostgres{tx: tx}
}

// TotalSolveTimePerPlayer returns the cumulative solve time (ms) of every
// player who has won at least one finished duel, ordered ASC. Used as a
// tiebreaker for the Redis leaderboard (faster total time -> higher rank).
func (r *LeaderboardPostgres) TotalSolveTimePerPlayer(ctx context.Context) ([]LeaderboardRow, error) {
	rows, err := r.tx.Querier(ctx).TotalSolveTimePerPlayer(ctx)
	if err != nil {
		return nil, fmt.Errorf("LeaderboardPostgres - TotalSolveTimePerPlayer - Querier.TotalSolveTimePerPlayer: %w", err)
	}
	out := make([]LeaderboardRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, LeaderboardRow{
			PlayerID:         row.PlayerID,
			Username:         row.Username,
			TotalSolveTimeMs: row.TotalSolveTimeMs,
		})
	}
	return out, nil
}

func (r *LeaderboardPostgres) TotalSolveTimeForPlayers(ctx context.Context, usernames []string) ([]LeaderboardRow, error) {
	rows, err := r.tx.Querier(ctx).TotalSolveTimeForPlayers(ctx, usernames)
	if err != nil {
		return nil, fmt.Errorf("LeaderboardPostgres - TotalSolveTimeForPlayers - Querier.TotalSolveTimeForPlayers: %w", err)
	}
	out := make([]LeaderboardRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, LeaderboardRow{
			PlayerID:         row.PlayerID,
			Username:         row.Username,
			TotalSolveTimeMs: row.TotalSolveTimeMs,
		})
	}
	return out, nil
}
