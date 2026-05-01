package response

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
)

func Player(player *domain.Player) openapi.PlayerResponse {
	return openapi.PlayerResponse{
		Id:        player.ID,
		Username:  player.Username,
		Status:    openapi.PlayerStatus(player.Status),
		CreatedAt: player.CreatedAt,
	}
}

func PlayerMe(me *playerusecase.PlayerWithActiveDuel) openapi.PlayerMeResponse {
	resp := openapi.PlayerMeResponse{
		Player: Player(me.Player),
	}
	if me.ActiveDuel != nil {
		resp.ActiveDuel = &openapi.ActiveDuelInfo{
			Id:        me.ActiveDuel.ID,
			Status:    openapi.ActiveDuelInfoStatusActive,
			StartedAt: me.ActiveDuel.StartedAt,
			Deadline:  me.ActiveDuel.Deadline,
		}
	}
	return resp
}
