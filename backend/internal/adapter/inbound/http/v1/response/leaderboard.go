package response

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
)

func Leaderboard(page leaderboardusecase.PageResult) api.LeaderboardResponse {
	out := make([]api.LeaderboardEntry, 0, len(page.Entries))
	for _, entry := range page.Entries {
		apiEntry := api.LeaderboardEntry{
			Rank:               IntToInt32(entry.Rank),
			Username:           entry.Username,
			Wins:               IntToInt32(entry.Wins),
			AverageSolveTimeMs: entry.AverageSolveTimeMs,
		}
		if entry.Avatar != nil {
			apiEntry.Avatar = &api.LeaderboardAvatar{
				PlayerId:    entry.Avatar.PlayerID,
				Version:     entry.Avatar.Version,
				ContentType: api.LeaderboardAvatarContentType(entry.Avatar.ContentType),
			}
		}
		out = append(out, apiEntry)
	}
	return api.LeaderboardResponse{
		Entries:    out,
		Page:       page.Page,
		PerPage:    page.PerPage,
		Total:      page.Total,
		TotalPages: page.TotalPages,
	}
}
