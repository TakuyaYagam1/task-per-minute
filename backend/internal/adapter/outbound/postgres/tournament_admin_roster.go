package postgres

import rosterpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/roster"

type TournamentAdminRosterPostgres = rosterpostgres.TournamentAdminRosterPostgres

func NewTournamentAdminRosterPostgres(tx *TxManager) *TournamentAdminRosterPostgres {
	return rosterpostgres.NewTournamentAdminRosterPostgres(tx)
}
