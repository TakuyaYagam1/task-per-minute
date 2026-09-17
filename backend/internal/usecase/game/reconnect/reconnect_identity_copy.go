package reconnect

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func reconnectCloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func reconnectCloneSeriesScoreRevisionIDPointer(value *domain.SeriesScoreRevisionID) *domain.SeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func reconnectCloneOfficialResultRevisionIDPointer(value *domain.OfficialResultRevisionID) *domain.OfficialResultRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
