package postgres

import contentpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/configuration"

type TournamentContentPostgres = contentpostgres.TournamentContentPostgres

func NewTournamentContentPostgres(tx *TxManager) *TournamentContentPostgres {
	return contentpostgres.NewTournamentContentPostgres(tx)
}
