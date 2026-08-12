package leaderboard

import (
	"context"
	"time"
)

type Clock interface {
	Now() time.Time
}

type StatsRepository interface {
	TopStats(ctx context.Context, limit int32) ([]PlayerStats, error)
}

type WinStore interface {
	IncrementWin(ctx context.Context, username string) error
}
