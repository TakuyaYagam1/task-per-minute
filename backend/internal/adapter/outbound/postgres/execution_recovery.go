package postgres

import executionrecovery "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/recovery"

// ExecutionRecoveryPostgres keeps the root adapter package compatible while
// the implementation lives in the execution recovery child package.
type ExecutionRecoveryPostgres = executionrecovery.ExecutionRecoveryPostgres

func NewExecutionRecoveryPostgres(
	tx *TxManager,
	deadlines *RecoveryPostgres,
	terminal *RecoveryTerminalPostgres,
) *ExecutionRecoveryPostgres {
	if terminal == nil {
		return executionrecovery.NewExecutionRecoveryPostgresWithDependencies(
			tx,
			deadlines,
			nil,
			resultProjectionFinalizer,
		)
	}
	return executionrecovery.NewExecutionRecoveryPostgresWithDependencies(
		tx,
		deadlines,
		terminal.inner,
		resultProjectionFinalizer,
	)
}
