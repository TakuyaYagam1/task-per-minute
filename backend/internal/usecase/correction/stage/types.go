package stage

import (
	"context"
	"time"

	"github.com/google/uuid"

	cutoffusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction/cutoff"
	planusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction/plan"
)

type Plan = planusecase.Plan
type CutoffEvent = cutoffusecase.CutoffEvent

type Clock interface {
	Now() time.Time
}

type TransactionManager interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

type StageRepository interface {
	LoadCorrectionStage(ctx context.Context, tournamentID uuid.UUID) (StageSnapshot, error)
	CommitCorrectionStage(ctx context.Context, commit StageCommit) (bool, error)
}
