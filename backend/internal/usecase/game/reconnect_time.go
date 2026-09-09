package game

import (
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func reconnectValidServerTime(value time.Time) bool {
	return domain.IsValidServerTime(value)
}

func reconnectCloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
