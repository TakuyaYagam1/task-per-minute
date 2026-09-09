package authority

import (
	"context"
	"time"

	"github.com/google/uuid"

	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
)

type Clock interface {
	Now() time.Time
}

// TimeSource supplies the same authoritative time used by the durable lease
// compare-and-set. Controllers must not plan or cache leases on host time.
type TimeSource interface {
	AuthorityTime(ctx context.Context) (time.Time, error)
}

// Repository retains every command result for exact retries and owns the
// atomic compare-and-set boundary. CommitAuthority must revalidate revision,
// stamp, and absent/live/expired state using authoritative transaction time.
type Repository interface {
	FindAuthorityCommand(
		ctx context.Context,
		tournamentID uuid.UUID,
		commandID uuid.UUID,
	) (*authoritydomain.Lease, error)
	LoadAuthority(
		ctx context.Context,
		tournamentID uuid.UUID,
	) (*authoritydomain.Lease, error)
	CommitAuthority(
		ctx context.Context,
		condition CommitCondition,
		lease authoritydomain.Lease,
	) (*authoritydomain.Lease, bool, error)
}
