package leaderboard

import (
	"context"
)

type StatsRepository interface {
	TopStats(ctx context.Context, limit int32) ([]PlayerStats, error)
}

type PageRepository interface {
	LeaderboardPage(ctx context.Context, query PageQuery) (PageRows, error)
}

type PageReader interface {
	Page(ctx context.Context, query PageQuery) (PageResult, error)
}
