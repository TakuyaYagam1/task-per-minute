package roster

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type RosterClock interface {
	Now() time.Time
}

type RosterPreflightEvidence struct {
	RosterID           uuid.UUID
	RosterRevision     int64
	CheckedInPlayerIDs []uuid.UUID
	Approved           bool
}

type RosterLockCommand struct {
	Preflight RosterPreflightEvidence
}

type RosterUnlockCommand struct {
	RosterID         uuid.UUID
	ExpectedRevision int64
	ActorID          uuid.UUID
	Reason           string
}

type RosterLockRepository interface {
	GetRosterSnapshot(ctx context.Context, id uuid.UUID) (*RosterRosterRecord, error)
	LockRosterAndReserveExpected(
		ctx context.Context,
		rosterID uuid.UUID,
		expectedRevision int64,
		expectedPlayerIDs []uuid.UUID,
		lockedAt time.Time,
	) (*RosterRosterRecord, bool, error)
	UnlockRosterAndReleaseExpected(
		ctx context.Context,
		rosterID uuid.UUID,
		expectedRevision int64,
		updatedAt time.Time,
	) (*RosterRosterRecord, bool, error)
}
