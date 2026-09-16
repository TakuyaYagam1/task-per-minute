package postgres

import progressionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/progression"

// TournamentProgressionPostgres keeps the historical root-package name while
// the durable progression implementation lives in the child package.
type TournamentProgressionPostgres = progressionrepo.TournamentProgressionPostgres

func NewTournamentProgressionPostgres(tx *TxManager) *TournamentProgressionPostgres {
	return progressionrepo.NewTournamentProgressionPostgres(tx)
}
