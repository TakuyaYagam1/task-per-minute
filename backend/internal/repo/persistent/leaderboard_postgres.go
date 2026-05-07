package persistent

import (
	"context"
	"fmt"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
)

type LeaderboardRow = usecase.LeaderboardPlayerStats

type LeaderboardPostgres struct {
	tx *TxManager
}

func NewLeaderboardPostgres(tx *TxManager) *LeaderboardPostgres {
	return &LeaderboardPostgres{tx: tx}
}

// TopStats returns players with at least one flag-solved duel win. Forfeits,
// disconnect draws, and any polluted Redis-only counters are intentionally not
// represented here.
func (r *LeaderboardPostgres) TopStats(ctx context.Context, limit int32) ([]LeaderboardRow, error) {
	rows, err := r.tx.Querier(ctx).TopLeaderboardStats(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("LeaderboardPostgres - TopStats - Querier.TopLeaderboardStats: %w", err)
	}
	out := make([]LeaderboardRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, LeaderboardRow{
			PlayerID:           row.PlayerID,
			Username:           row.Username,
			Wins:               int(row.Wins),
			AverageSolveTimeMs: row.AverageSolveTimeMs,
		})
	}
	return out, nil
}
