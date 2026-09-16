package postgres

import (
	"context"

	terminalprojection "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/terminalprojection"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

// persistTerminalSwissPoints remains a private root bridge for compatibility
// with root-package settlement workflows.
func persistTerminalSwissPoints(ctx context.Context, q *sqlc.Queries, in ResultSettlementInput, commit sqlc.LockTerminalProjectionCommitRow) error {
	return terminalprojection.PersistTerminalSwissPoints(ctx, q, in, commit)
}

// materializeTerminalSwissStandings remains a private root bridge for root
// transaction workflows that still use the migration-era helper name.
func materializeTerminalSwissStandings(ctx context.Context, tx *TxManager, in ResultSettlementInput) (bool, error) {
	return terminalprojection.MaterializeTerminalSwissStandings(ctx, tx, in)
}
