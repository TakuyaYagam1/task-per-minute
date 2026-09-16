package postgres

import "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/leaderboard"

type LeaderboardPostgres = leaderboard.LeaderboardPostgres

func NewLeaderboardPostgres(tx *TxManager) *LeaderboardPostgres {
	return leaderboard.NewLeaderboardPostgres(tx)
}
