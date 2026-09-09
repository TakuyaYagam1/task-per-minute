package golden

import (
	"time"

	"github.com/google/uuid"
)

func prestartCloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func prestartCloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func prestartValidRevisionPredecessor(current uuid.UUID, revision int64, previous *uuid.UUID) bool {
	return revision == 1 && previous == nil ||
		revision > 1 && previous != nil && *previous != uuid.Nil && *previous != current
}
