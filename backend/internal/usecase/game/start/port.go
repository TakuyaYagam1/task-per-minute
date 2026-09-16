package start

import (
	"context"
	"time"
)

type Clock interface {
	Now() time.Time
}

type StartRepository interface {
	LoadWaveStartAuthority(ctx context.Context, scope StartScope) (StartAuthority, error)

	// ReadWaveStartTime returns the authoritative database timestamp used for
	// the Wave start and every derived Game deadline.
	ReadWaveStartTime(ctx context.Context) (time.Time, error)
	CommitWaveStart(ctx context.Context, record StartRecord) (*StartRecord, bool, error)
}
