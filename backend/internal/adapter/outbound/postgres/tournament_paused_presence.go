package postgres

import reconnect "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/reconnect"

type TournamentPausedPresencePostgres = reconnect.TournamentPausedPresencePostgres

func NewTournamentPausedPresencePostgres(tx *TxManager) *TournamentPausedPresencePostgres {
	return reconnect.NewTournamentPausedPresencePostgres(tx, NewTournamentAdminExecutionPostgres(tx))
}
