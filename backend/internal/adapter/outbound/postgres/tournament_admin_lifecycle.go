package postgres

import lifecyclepostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/lifecycle"

// TournamentAdminLifecyclePostgres keeps the historical root-package name
// while the lifecycle implementation lives in the child package.
type TournamentAdminLifecyclePostgres = lifecyclepostgres.TournamentAdminLifecyclePostgres

func NewTournamentAdminLifecyclePostgres(tx *TxManager) *TournamentAdminLifecyclePostgres {
	return lifecyclepostgres.NewTournamentAdminLifecyclePostgres(tx)
}
