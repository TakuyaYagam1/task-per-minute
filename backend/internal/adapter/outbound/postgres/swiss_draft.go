package postgres

import (
	"context"

	swissdraft "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/swiss/draft"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

func (r *TournamentAdminExecutionPostgres) materializeSwissDraftBO1(
	ctx context.Context,
	plan tournamentadmin.PairingPlan,
) error {
	return swissdraft.MaterializeSwissDraftBO1(ctx, r.tx, plan, createMaterializedSeriesPresence)
}
