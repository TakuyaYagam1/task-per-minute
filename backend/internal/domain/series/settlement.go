package series

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type ScoreRevision struct {
	ID                    domain.SeriesScoreRevisionID
	SeriesID              uuid.UUID
	FirstParticipantID    uuid.UUID
	SecondParticipantID   uuid.UUID
	PreviousRevisionID    *domain.SeriesScoreRevisionID
	Ordinal               int
	Format                domain.SeriesFormat
	ScoreBefore           domain.SeriesScore
	ScoreAfter            domain.SeriesScore
	GameResultRevisionIDs []domain.OfficialResultRevisionID
	RecordedAt            time.Time
}

type SettlementEvidence struct {
	AuditEventID             uuid.UUID
	OutboxEventID            uuid.UUID
	ProjectionRevisionID     uuid.UUID
	SourceProjectionRevision int64
	ProjectionRevision       int64
	RecordedAt               time.Time
}
