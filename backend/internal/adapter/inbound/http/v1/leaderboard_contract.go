package v1

import (
	"context"

	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
)

type LeaderboardService interface {
	Top50(ctx context.Context) ([]leaderboardusecase.Entry, error)
	Page(ctx context.Context, query leaderboardusecase.PageQuery) (leaderboardusecase.PageResult, error)
}
