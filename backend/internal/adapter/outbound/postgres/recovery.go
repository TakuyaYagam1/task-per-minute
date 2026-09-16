package postgres

import (
	recoveryrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/recovery"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

type RecoveryPostgres = recoveryrepo.RecoveryPostgres

func NewRecoveryPostgres(tx *TxManager, sink recovery.DeadlineArmSink) *RecoveryPostgres {
	return recoveryrepo.NewRecoveryPostgres(tx, sink)
}
