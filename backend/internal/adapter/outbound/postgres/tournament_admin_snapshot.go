package postgres

import snapshotpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/snapshot"

type TournamentAdminSnapshotPostgres = snapshotpostgres.TournamentAdminSnapshotPostgres

func NewTournamentAdminSnapshotPostgres(tx *TxManager) *TournamentAdminSnapshotPostgres {
	return snapshotpostgres.NewTournamentAdminSnapshotPostgres(tx)
}
