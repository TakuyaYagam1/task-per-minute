package postgres

import (
	projectionpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
)

type ProjectionHealthPostgres = projectionpostgres.ProjectionHealthPostgres

func NewProjectionHealthPostgres(tx *TxManager) *ProjectionHealthPostgres {
	return projectionpostgres.NewProjectionHealthPostgres(tx)
}
