package reconnect

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type GameRevision struct {
	Ordinal    int
	ID         domain.OfficialResultRevisionID
	GameID     uuid.UUID
	WinnerID   uuid.UUID
	Reason     domain.GameResultReason
	RecordedAt time.Time
}

type SeriesRevision struct {
	Ordinal            int
	ID                 domain.OfficialResultRevisionID
	SeriesID           uuid.UUID
	PreviousRevisionID *domain.OfficialResultRevisionID
	State              domain.SeriesState
	WinnerID           *uuid.UUID
	ScoreRevisionID    domain.SeriesScoreRevisionID
	Reason             domain.GameResultReason
	RecordedAt         time.Time
}
