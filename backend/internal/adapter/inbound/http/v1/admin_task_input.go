package v1

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
)

func createTaskInput(body api.CreateTaskRequest) taskusecase.CreateInput {
	input := taskusecase.CreateInput{
		Title:       body.Title,
		Description: body.Description,
		Category:    domain.Category(body.Category),
		Difficulty:  domain.Difficulty(body.Difficulty),
		TimeLimit:   int(body.TimeLimit),
		Flag:        body.Flag,
		Enabled:     body.Enabled,
		Hints:       hintsFromNullable(body.Hints),
		TaskURL:     body.TaskUrl,
	}
	if body.Kind != nil {
		input.Kind = domain.TaskKind(*body.Kind)
	}
	return input
}

func updateTaskInput(existing *domain.Task, body api.UpdateTaskRequest) taskusecase.UpdateInput {
	input := taskInputFromDomain(existing)
	mergeTaskUpdate(&input, body)
	return input
}

func clearSourceFileRequested(body api.UpdateTaskRequest) bool {
	return body.ClearSourceFile != nil && *body.ClearSourceFile
}

func taskInputFromDomain(task *domain.Task) taskusecase.UpdateInput {
	return taskusecase.UpdateInput{
		Title:         task.Title,
		Description:   task.Description,
		Category:      task.Category,
		Difficulty:    task.Difficulty,
		TimeLimit:     task.TimeLimit,
		Flag:          task.Flag,
		Kind:          task.Kind,
		Enabled:       task.Enabled,
		Hints:         cloneHints(task.Hints),
		TaskURL:       task.TaskURL,
		SourceFileURL: task.SourceFileURL,
	}
}

func mergeTaskUpdate(input *taskusecase.UpdateInput, body api.UpdateTaskRequest) {
	if body.Title != nil {
		input.Title = *body.Title
	}
	if body.Description != nil {
		input.Description = *body.Description
	}
	if body.Category != nil {
		input.Category = domain.Category(*body.Category)
	}
	if body.Difficulty != nil {
		input.Difficulty = domain.Difficulty(*body.Difficulty)
	}
	if body.TimeLimit != nil {
		input.TimeLimit = int(*body.TimeLimit)
	}
	if body.Flag != nil {
		input.Flag = *body.Flag
	}
	if body.Kind != nil {
		input.Kind = domain.TaskKind(*body.Kind)
	}
	if body.Enabled != nil {
		input.Enabled = *body.Enabled
	}
	if body.Hints != nil {
		input.Hints = hintsFromNullable(*body.Hints)
	}
	if clearSourceFileRequested(body) {
		input.SourceFileURL = nil
	}
	if value, set := body.TaskUrl.Value(); set {
		input.TaskURL = value
	}
}

func cloneHints(hints []string) []string {
	return append([]string(nil), hints...)
}

func hintsFromNullable(hints []*string) []string {
	out := make([]string, len(hints))
	for i, hint := range hints {
		if hint != nil {
			out[i] = *hint
		}
	}
	return out
}
