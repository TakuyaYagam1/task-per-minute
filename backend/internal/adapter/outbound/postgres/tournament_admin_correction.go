package postgres

import (
	admincorrection "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/correction"
)

// TournamentAdminCorrectionPostgres keeps the historical root-package API
// while the implementation lives in the tournament admin capability.
type TournamentAdminCorrectionPostgres = admincorrection.TournamentAdminCorrectionPostgres

func NewTournamentAdminCorrectionPostgres(tx *TxManager) *TournamentAdminCorrectionPostgres {
	return admincorrection.NewTournamentAdminCorrectionPostgres(tx)
}
