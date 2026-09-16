package postgres

import (
	runtime "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/golden/runtime"
)

// GoldenRuntimePostgres is kept as a root-package facade for the Golden
// runtime repository.
type GoldenRuntimePostgres = runtime.GoldenRuntimePostgres

// NewGoldenRuntimePostgres constructs the Golden runtime repository facade.
func NewGoldenRuntimePostgres(tx *TxManager) *GoldenRuntimePostgres {
	return runtime.NewGoldenRuntimePostgres(tx)
}
