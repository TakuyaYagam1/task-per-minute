package settlement

import (
	"context"

	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
)

type SettlementRepository interface {
	LoadConcurrentWinnerAuthority(
		ctx context.Context,
		scope gamedomain.SubmissionScope,
	) (SettlementAuthority, error)
	CommitConcurrentWinnerSettlement(
		ctx context.Context,
		settlement SettlementRecord,
	) (*SettlementRecord, bool, error)
}
