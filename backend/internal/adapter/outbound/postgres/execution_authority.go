package postgres

import authorityrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/authority"

type ExecutionAuthorityPostgres = authorityrepo.ExecutionAuthorityPostgres

func NewExecutionAuthorityPostgres(tx *TxManager) *ExecutionAuthorityPostgres {
	return authorityrepo.NewExecutionAuthorityPostgres(tx)
}
