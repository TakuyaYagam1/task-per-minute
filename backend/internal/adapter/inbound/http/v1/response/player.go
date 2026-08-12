package response

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
)

func Player(player *domain.Player) api.PlayerResponse {
	return api.PlayerResponse{
		Id:        player.ID,
		Username:  player.Username,
		Status:    api.PlayerStatus(player.Status),
		CreatedAt: player.CreatedAt,
	}
}

func PlayerMe(me *playerusecase.PlayerWithActiveDuel) api.PlayerMeResponse {
	resp := api.PlayerMeResponse{
		Player: Player(me.Player),
	}
	if me.ActiveDuel != nil {
		resp.ActiveDuel = &api.ActiveDuelInfo{
			Id:        me.ActiveDuel.ID,
			Status:    api.ActiveDuelInfoStatusActive,
			StartedAt: me.ActiveDuel.StartedAt,
			Deadline:  me.ActiveDuel.Deadline,
		}
	}
	return resp
}
