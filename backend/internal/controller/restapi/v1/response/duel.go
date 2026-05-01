package response

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
)

func DuelDetail(duel *domain.Duel, tasks []*domain.DuelPlayerTask) openapi.DuelDetailResponse {
	playerTasks := make([]openapi.DuelPlayerTaskResponse, 0, len(tasks))
	for _, task := range tasks {
		playerTasks = append(playerTasks, openapi.DuelPlayerTaskResponse{
			PlayerId: task.PlayerID,
			TaskId:   task.TaskID,
			Solved:   task.Solved,
			SolvedAt: task.SolvedAt,
		})
	}

	return openapi.DuelDetailResponse{
		Duel: openapi.DuelResponse{
			Id:         duel.ID,
			Player1Id:  duel.Player1ID,
			Player2Id:  duel.Player2ID,
			Status:     openapi.DuelStatus(duel.Status),
			WinnerId:   UUIDPtr(duel.WinnerID),
			Deadline:   duel.Deadline,
			StartedAt:  duel.StartedAt,
			FinishedAt: duel.FinishedAt,
		},
		PlayerTasks: playerTasks,
	}
}
