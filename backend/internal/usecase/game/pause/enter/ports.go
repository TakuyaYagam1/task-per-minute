package enter

import (
	"context"
	"time"

	"github.com/google/uuid"

	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
)

type PauseClock interface {
	Now() time.Time
}

// NormalPauseRepository participates in the caller transaction. Load locks the
// complete authority. Commit revalidates every expectation and publishes the
// complete graph and command result atomically or makes no write.
type NormalPauseRepository interface {
	FindNormalPauseCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*NormalPauseRecord, error)
	LoadNormalPauseAuthority(ctx context.Context, scope pausedomain.GraphScope) (NormalPauseAuthority, error)
	CommitNormalPause(ctx context.Context, expected PauseGraphRevisions, record NormalPauseRecord) (*NormalPauseRecord, bool, error)
}

type TransactionManager interface {
	Do(ctx context.Context, fn func(context.Context) error) error
}
