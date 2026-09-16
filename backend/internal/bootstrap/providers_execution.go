package bootstrap

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	executionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution"
)

func provideTournamentExecutionRepository(
	tx *postgres.TxManager,
) *executionrepo.Repository {
	return executionrepo.NewRepository(tx, resultauthority.FinalizeProjection)
}
