package correction

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Clock interface {
	Now() time.Time
}

type StageRepository interface {
	LoadCorrectionStage(ctx context.Context, tournamentID uuid.UUID) (StageSnapshot, error)
	CommitCorrectionStage(ctx context.Context, commit StageCommit) (bool, error)
}

type TransactionManager interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}
