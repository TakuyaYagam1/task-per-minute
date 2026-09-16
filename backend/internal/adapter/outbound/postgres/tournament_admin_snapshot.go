package postgres

import (
	"context"

	snapshotpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/snapshot"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

type TournamentAdminSnapshotPostgres struct {
	delegate *snapshotpostgres.TournamentAdminSnapshotPostgres
}

func NewTournamentAdminSnapshotPostgres(tx *TxManager) *TournamentAdminSnapshotPostgres {
	return &TournamentAdminSnapshotPostgres{
		delegate: snapshotpostgres.NewTournamentAdminSnapshotPostgres(tx),
	}
}

func (r *TournamentAdminSnapshotPostgres) GetOperatorSnapshot(
	ctx context.Context,
	query tournamentadmin.SnapshotQuery,
) (tournamentadmin.OperatorSnapshotView, error) {
	if r == nil || r.delegate == nil {
		return tournamentadmin.OperatorSnapshotView{}, domain.ErrValidation
	}
	return r.delegate.GetOperatorSnapshot(ctx, query)
}

func (r *TournamentAdminSnapshotPostgres) loadOperatorSnapshot(
	ctx context.Context,
	query tournamentadmin.SnapshotQuery,
) (tournamentadmin.OperatorSnapshotView, error) {
	if r == nil || r.delegate == nil {
		return tournamentadmin.OperatorSnapshotView{}, domain.ErrValidation
	}
	return r.delegate.LoadOperatorSnapshot(ctx, query)
}

var _ tournamentadmin.SnapshotPort = (*TournamentAdminSnapshotPostgres)(nil)
