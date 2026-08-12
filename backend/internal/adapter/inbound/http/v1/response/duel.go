package response

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func DuelDetail(duel *domain.Duel, tasks []*domain.DuelPlayerTask) api.DuelDetailResponse {
	playerTasks := make([]api.DuelPlayerTaskResponse, 0, len(tasks))
	for _, task := range tasks {
		playerTasks = append(playerTasks, api.DuelPlayerTaskResponse{
			PlayerId: task.PlayerID,
			TaskId:   task.TaskID,
			Solved:   task.Solved,
			SolvedAt: task.SolvedAt,
		})
	}

	return api.DuelDetailResponse{
		Duel: api.DuelResponse{
			Id:         duel.ID,
			Player1Id:  duel.Player1ID,
			Player2Id:  duel.Player2ID,
			Status:     api.DuelStatus(duel.Status),
			WinnerId:   UUIDPtr(duel.WinnerID),
			Deadline:   duel.Deadline,
			StartedAt:  duel.StartedAt,
			FinishedAt: duel.FinishedAt,
		},
		PlayerTasks: playerTasks,
	}
}
