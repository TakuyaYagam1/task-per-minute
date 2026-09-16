package postgres

import (
	executionrecovery "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/recovery"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
)

// ExecutionRecoveryPostgres keeps the root adapter package compatible while
// the implementation lives in the execution recovery child package.
type ExecutionRecoveryPostgres = executionrecovery.ExecutionRecoveryPostgres

func NewExecutionRecoveryPostgres(
	tx *TxManager,
	deadlines *RecoveryPostgres,
	terminal *RecoveryTerminalPostgres,
) *ExecutionRecoveryPostgres {
	return executionrecovery.NewExecutionRecoveryPostgresWithDependencies(
		tx,
		deadlines,
		terminal,
		resultauthority.FinalizeProjection,
	)
}
