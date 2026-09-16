package postgres

import (
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	executionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution"
	reconnect "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/reconnect"
)

type TournamentPausedPresencePostgres = reconnect.TournamentPausedPresencePostgres

func NewTournamentPausedPresencePostgres(tx *TxManager) *TournamentPausedPresencePostgres {
	return reconnect.NewTournamentPausedPresencePostgres(
		tx,
		executionrepo.NewRepository(tx, resultauthority.FinalizeProjection),
	)
}
