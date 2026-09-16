package postgres

import "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/task"

type PrivateTaskAvailabilityPostgres = task.PrivateTaskAvailabilityPostgres

func NewPrivateTaskAvailabilityPostgres(tx *TxManager) *PrivateTaskAvailabilityPostgres {
	return task.NewPrivateTaskAvailabilityPostgres(tx)
}
