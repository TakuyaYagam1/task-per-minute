package postgres

import replaypostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/replay"

type TournamentAdminReplayPostgres = replaypostgres.TournamentAdminReplayPostgres

func NewTournamentAdminReplayPostgres(tx *TxManager) *TournamentAdminReplayPostgres {
	return replaypostgres.NewTournamentAdminReplayPostgres(tx)
}
