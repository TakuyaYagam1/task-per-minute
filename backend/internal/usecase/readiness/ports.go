package readiness

import (
	"context"
	"time"
)

type Clock interface {
	Now() time.Time
}

// ReadinessRepository owns the participant-scoped compare-and-set. It must
// compare the current Wave and ready-window revisions before changing one
// participant and appending the command event.
type ReadinessRepository interface {
	LoadReadinessAuthority(ctx context.Context, scope ReadinessScope) (ReadinessAuthority, error)
	CommitReadiness(ctx context.Context, commit ReadinessCommit) (*ReadinessRecord, bool, error)
}

// ReadyWindowRepository owns one compare-and-set that revalidates the Wave,
// exact plan, projection and artifact revisions before opening the window.
type ReadyWindowRepository interface {
	LoadReadyWindowAuthority(ctx context.Context, scope ReadyWindowScope) (ReadyWindowAuthority, error)
	CommitReadyWindow(ctx context.Context, record ReadyWindowRecord) (*ReadyWindowRecord, bool, error)
}
