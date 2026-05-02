package v1

import (
	"bytes"
	"encoding/json"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
	adminusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
)

type updateTaskRequest struct {
	Category    *openapi.TaskCategory   `json:"category,omitempty"`
	Description *string                 `json:"description,omitempty"`
	Difficulty  *openapi.TaskDifficulty `json:"difficulty,omitempty"`
	Flag        *string                 `json:"flag,omitempty"`
	Hints       *[]string               `json:"hints,omitempty"`
	TaskURL     nullableString          `json:"task_url"`
	TimeLimit   *int32                  `json:"time_limit,omitempty"`
	Title       *string                 `json:"title,omitempty"`
}

type nullableString struct {
	Set   bool
	Value *string
}

func (s *nullableString) UnmarshalJSON(data []byte) error {
	s.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		s.Value = nil
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	s.Value = &value
	return nil
}

func createTaskInput(body openapi.CreateTaskRequest) adminusecase.TaskInput {
	return adminusecase.TaskInput{
		Title:       body.Title,
		Description: body.Description,
		Category:    domain.Category(body.Category),
		Difficulty:  domain.Difficulty(body.Difficulty),
		TimeLimit:   int(body.TimeLimit),
		Flag:        body.Flag,
		Hints:       cloneHints(body.Hints),
		TaskURL:     body.TaskUrl,
	}
}

func updateTaskInput(existing *domain.Task, body updateTaskRequest) adminusecase.TaskInput {
	input := taskInputFromDomain(existing)
	mergeTaskUpdate(&input, body)
	return input
}

func taskInputFromDomain(task *domain.Task) adminusecase.TaskInput {
	return adminusecase.TaskInput{
		Title:         task.Title,
		Description:   task.Description,
		Category:      task.Category,
		Difficulty:    task.Difficulty,
		TimeLimit:     task.TimeLimit,
		Flag:          task.Flag,
		Hints:         cloneHints(task.Hints),
		TaskURL:       task.TaskURL,
		SourceFileURL: task.SourceFileURL,
	}
}

func mergeTaskUpdate(input *adminusecase.TaskInput, body updateTaskRequest) {
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
	if body.Hints != nil {
		input.Hints = cloneHints(*body.Hints)
	}
	if body.TaskURL.Set {
		input.TaskURL = body.TaskURL.Value
	}
}

func cloneHints(hints []string) []string {
	return append([]string(nil), hints...)
}
