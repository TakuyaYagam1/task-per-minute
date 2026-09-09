package task

import "github.com/TakuyaYagam1/task-per-minute/internal/domain"

// CreateInput carries the explicit admin intent for a new task. Enabled is
// optional so the use case can own the default rather than relying on a
// transport default.
type CreateInput struct {
	Title         string
	Description   string
	Category      domain.Category
	Difficulty    domain.Difficulty
	TimeLimit     int
	Flag          string
	Kind          domain.TaskKind
	Enabled       *bool
	Hints         []string
	TaskURL       *string
	SourceFileURL *string
}

// UpdateInput is a fully materialized task head. HTTP handlers merge a patch
// onto the current head before invoking the use case, so Enabled is explicit.
type UpdateInput struct {
	Title         string
	Description   string
	Category      domain.Category
	Difficulty    domain.Difficulty
	TimeLimit     int
	Flag          string
	Kind          domain.TaskKind
	Enabled       bool
	Hints         []string
	TaskURL       *string
	SourceFileURL *string
}
