package postgres

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
)

type TxManager = db.TxManager

var (
	ErrReadOnlySnapshotWrite     = db.ErrReadOnlySnapshotWrite
	ErrSnapshotInsideTransaction = db.ErrSnapshotInsideTransaction
)

func NewTxManager(pool *pgxpool.Pool) *TxManager {
	return db.NewTxManager(pool)
}
