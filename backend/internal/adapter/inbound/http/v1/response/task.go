package response

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func Task(task *domain.Task) api.TaskResponse {
	return api.TaskResponse{
		Id:            task.ID,
		Title:         task.Title,
		Description:   task.Description,
		Category:      api.TaskCategory(task.Category),
		Difficulty:    api.TaskDifficulty(task.Difficulty),
		TimeLimit:     IntToInt32(task.TimeLimit),
		Flag:          task.Flag,
		Hints:         nullableHints(task.Hints),
		TaskUrl:       task.TaskURL,
		SourceFileUrl: task.SourceFileURL,
		CreatedAt:     task.CreatedAt,
	}
}

func nullableHints(hints []string) []*string {
	normalized, _ := domain.NormalizeTaskHints(hints)
	out := make([]*string, len(normalized))
	for i, hint := range normalized {
		if hint == "" {
			continue
		}
		value := hint
		out[i] = &value
	}
	return out
}

func Tasks(tasks []*domain.Task) []api.TaskResponse {
	out := make([]api.TaskResponse, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, Task(task))
	}
	return out
}
