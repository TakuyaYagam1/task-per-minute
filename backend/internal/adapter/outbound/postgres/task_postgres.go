package postgres

import "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/task"

type TaskPostgres = task.TaskPostgres

func NewTaskPostgres(tx *TxManager) *TaskPostgres {
	return task.NewTaskPostgres(tx)
}
