package postgres

import (
	"context"

	terminalprojection "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/terminalprojection"
)

// persistTerminalProjectionNodes remains a private root bridge for legacy
// result settlement callers while the implementation lives in the result
// terminal-projection child package.
func persistTerminalProjectionNodes(ctx context.Context, tx *TxManager, in ResultSettlementInput) error {
	return terminalprojection.PersistTerminalProjectionNodes(ctx, tx, in)
}
