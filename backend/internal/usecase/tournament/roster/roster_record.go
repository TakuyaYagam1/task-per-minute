package roster

import (
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrRosterNotFound = errors.New("tournament roster not found")

type RosterRosterRecord struct {
	ID                 uuid.UUID
	Revision           int64
	LockedAt           *time.Time
	ExecutionStartedAt *time.Time
}

func rosterValidServerTime(value time.Time) bool {
	return domain.IsValidServerTime(value)
}
