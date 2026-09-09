package postgres

import (
	"context"
	"fmt"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
)

type LeaderboardPostgres struct {
	tx *TxManager
}

func NewLeaderboardPostgres(tx *TxManager) *LeaderboardPostgres {
	return &LeaderboardPostgres{tx: tx}
}

var _ leaderboardusecase.StatsRepository = (*LeaderboardPostgres)(nil)

// TopStats returns players with a current, accepted tournament solve result.
func (r *LeaderboardPostgres) TopStats(ctx context.Context, limit int32) ([]leaderboardusecase.PlayerStats, error) {
	rows, err := r.tx.Querier(ctx).TopLeaderboardStats(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("LeaderboardPostgres - TopStats - Querier.TopLeaderboardStats: %w", err)
	}
	out := make([]leaderboardusecase.PlayerStats, 0, len(rows))
	for _, row := range rows {
		out = append(out, leaderboardusecase.PlayerStats{
			PlayerID:           row.PlayerID,
			Username:           row.Username,
			Wins:               int(row.Wins),
			AverageSolveTimeMs: row.AverageSolveTimeMs,
		})
	}
	return out, nil
}
