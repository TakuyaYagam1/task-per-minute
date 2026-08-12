package response

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
)

func Leaderboard(entries []leaderboardusecase.Entry) api.LeaderboardResponse {
	out := make([]api.LeaderboardEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, api.LeaderboardEntry{
			Rank:               IntToInt32(entry.Rank),
			Username:           entry.Username,
			Wins:               IntToInt32(entry.Wins),
			AverageSolveTimeMs: entry.AverageSolveTimeMs,
		})
	}
	return api.LeaderboardResponse{Entries: out}
}
