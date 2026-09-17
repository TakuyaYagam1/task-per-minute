package reconnect

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type AttemptGameResultRevision struct {
	Ordinal    int
	ID         domain.OfficialResultRevisionID
	GameID     uuid.UUID
	Reason     domain.GameResultReason
	RecordedAt time.Time
}

type WaveMemberRoute struct {
	ID       uuid.UUID
	WaveID   uuid.UUID
	SeriesID uuid.UUID
	SlotID   uuid.UUID
	GameID   uuid.UUID
	Category domain.Category
	RoutedAt time.Time
}
