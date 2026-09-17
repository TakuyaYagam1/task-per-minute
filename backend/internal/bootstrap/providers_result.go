package bootstrap

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	wavestartrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution/wavestart"
	adminresultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/result"
	settlementrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/settlement"
)

func provideResultPostgres(
	tx *postgres.TxManager,
) *resultrepo.ResultPostgres {
	return resultauthority.NewResultPostgres(tx)
}

func provideTournamentAdminResultRepository(
	tx *postgres.TxManager,
	results *resultrepo.ResultPostgres,
) *adminresultrepo.TournamentAdminResultPostgres {
	return adminresultrepo.NewTournamentAdminResultPostgresWithDependencies(
		tx,
		results,
		resultauthority.FinalizeProjection,
		wavestartrepo.EnsurePreStartSwissRoundProofForCommand,
	)
}

func provideParticipantSettlementRepository(
	tx *postgres.TxManager,
	results *resultrepo.ResultPostgres,
) *settlementrepo.ParticipantSettlementRepository {
	return settlementrepo.NewParticipantSettlementRepositoryWithFinalizer(
		tx,
		results,
		resultauthority.FinalizeProjection,
	)
}
