package postgres

import tournamentcancellation "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/cancellation"

type TournamentCancellationPostgres = tournamentcancellation.TournamentCancellationPostgres

func NewTournamentCancellationPostgres(tx *TxManager) *TournamentCancellationPostgres {
	return tournamentcancellation.NewTournamentCancellationPostgres(tx)
}
