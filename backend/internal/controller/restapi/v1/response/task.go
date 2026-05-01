package response

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
)

func Task(task *domain.Task) openapi.TaskResponse {
	return openapi.TaskResponse{
		Id:            task.ID,
		Title:         task.Title,
		Description:   task.Description,
		Category:      openapi.TaskCategory(task.Category),
		Difficulty:    openapi.TaskDifficulty(task.Difficulty),
		TimeLimit:     IntToInt32(task.TimeLimit),
		Flag:          task.Flag,
		Hints:         append([]string(nil), task.Hints...),
		TaskUrl:       task.TaskURL,
		SourceFileUrl: task.SourceFileURL,
		CreatedAt:     task.CreatedAt,
	}
}

func Tasks(tasks []*domain.Task) []openapi.TaskResponse {
	out := make([]openapi.TaskResponse, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, Task(task))
	}
	return out
}
