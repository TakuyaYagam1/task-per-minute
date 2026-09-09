package leaderboard

import (
	"context"
)

type StatsRepository interface {
	TopStats(ctx context.Context, limit int32) ([]PlayerStats, error)
}
