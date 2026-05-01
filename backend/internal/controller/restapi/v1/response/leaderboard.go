package response

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
)

func Leaderboard(entries []leaderboardusecase.Entry) openapi.LeaderboardResponse {
	out := make([]openapi.LeaderboardEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, openapi.LeaderboardEntry{
			Rank:             IntToInt32(entry.Rank),
			Username:         entry.Username,
			TasksSolved:      IntToInt32(entry.TasksSolved),
			TotalSolveTimeMs: entry.TotalSolveTimeMs,
		})
	}
	return openapi.LeaderboardResponse{Entries: out}
}
