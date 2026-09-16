package attempt

import (
	"context"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type AttemptClock interface {
	Now() time.Time
}

type AttemptRepository interface {
	LoadFailedAttemptAuthority(
		ctx context.Context,
		scope domain.FailedAttemptScope,
	) (AttemptAuthority, error)
	CommitFailedAttempt(
		ctx context.Context,
		record AttemptRecord,
	) (*AttemptRecord, bool, error)
}
