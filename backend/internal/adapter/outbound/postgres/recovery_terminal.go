package postgres

import (
	terminalrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/recovery/terminal"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	wavestartrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution/wavestart"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

// RecoveryTerminalPostgres preserves the root adapter contract while the
// terminal implementation lives in its recovery child package.
type RecoveryTerminalPostgres = terminalrepo.RecoveryTerminalPostgres

var _ recovery.DeadlineTerminalStore = (*RecoveryTerminalPostgres)(nil)

func NewRecoveryTerminalPostgres(
	tx *TxManager,
	authoritySource recovery.DeadlineAuthoritySource,
	clock recovery.Clock,
) *RecoveryTerminalPostgres {
	return terminalrepo.NewRecoveryTerminalPostgresWithDependencies(
		tx,
		authoritySource,
		clock,
		wavestartrepo.EnsurePreStartSwissRoundProofForCommand,
		resultauthority.FinalizeProjection,
	)
}
