package postgres

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
)

type TxManager = db.TxManager

var (
	ErrReadOnlySnapshotWrite     = db.ErrReadOnlySnapshotWrite
	ErrSnapshotInsideTransaction = db.ErrSnapshotInsideTransaction

	_ playerusecase.ManagementTransactionManager = (*TxManager)(nil)
	_ playerusecase.SessionTransactionManager    = (*TxManager)(nil)
)

func NewTxManager(pool *pgxpool.Pool) *TxManager {
	return db.NewTxManager(pool)
}
