package response

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func Player(player *domain.Player) api.PlayerResponse {
	return api.PlayerResponse{
		Id:        player.ID,
		Username:  player.Username,
		CreatedAt: player.CreatedAt,
	}
}

func CurrentPlayer(player *domain.Player) api.CurrentPlayerResponse {
	return api.CurrentPlayerResponse{Player: Player(player)}
}
