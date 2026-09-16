package postgres

import assignmentadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"

func requiredJSONObject(value map[string]any) ([]byte, error) {
	return assignmentadapter.RequiredJSONObject(value)
}
