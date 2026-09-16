package postgres

import (
	resultpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	wavestartrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution/wavestart"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/result"
)

type TournamentAdminResultPostgres = resultrepo.TournamentAdminResultPostgres

func NewTournamentAdminResultPostgres(
	tx *TxManager,
	results *resultpostgres.ResultPostgres,
) *TournamentAdminResultPostgres {
	return resultrepo.NewTournamentAdminResultPostgresWithDependencies(
		tx,
		results,
		resultauthority.FinalizeProjection,
		wavestartrepo.EnsurePreStartSwissRoundProofForCommand,
	)
}
