package postgres

import snapshot "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/snapshot"

var ErrTournamentSnapshotInvalid = snapshot.ErrTournamentSnapshotInvalid

type TournamentSnapshotPostgres = snapshot.TournamentSnapshotPostgres

func NewTournamentSnapshotPostgres(tx *TxManager) *TournamentSnapshotPostgres {
	return snapshot.NewTournamentSnapshotPostgres(tx)
}
