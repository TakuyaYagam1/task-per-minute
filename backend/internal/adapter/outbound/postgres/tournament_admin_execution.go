package postgres

import (
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	executionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution"
)

// TournamentAdminExecutionPostgres remains as a compatibility alias while
// production composition lives in the execution capability.
type TournamentAdminExecutionPostgres = executionrepo.Repository

func NewTournamentAdminExecutionPostgres(tx *TxManager) *TournamentAdminExecutionPostgres {
	return executionrepo.NewRepository(tx, resultauthority.FinalizeProjection)
}
