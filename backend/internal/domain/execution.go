package domain

import (
	"time"

	"github.com/google/uuid"
)

type FailedAttemptScope struct {
	TournamentID        uuid.UUID
	WaveID              uuid.UUID
	SeriesID            uuid.UUID
	SlotID              uuid.UUID
	GameID              uuid.UUID
	AssignmentID        uuid.UUID
	AssignmentAttemptID uuid.UUID
}

func (s FailedAttemptScope) IsValid() bool {
	return s.TournamentID != uuid.Nil && s.WaveID != uuid.Nil && s.SeriesID != uuid.Nil &&
		s.SlotID != uuid.Nil && s.GameID != uuid.Nil && s.AssignmentID != uuid.Nil &&
		s.AssignmentAttemptID != uuid.Nil
}

type ReadyWindowSourceRevisions struct {
	WaveRevisionID       WaveRevisionID
	WaveRevision         int64
	ProjectionRevisionID uuid.UUID
	ProjectionRevision   int64
	ArtifactRevisionID   uuid.UUID
	ArtifactRevision     int64
}

func (r ReadyWindowSourceRevisions) IsValid() bool {
	return !r.WaveRevisionID.IsZero() && r.WaveRevision >= 1 &&
		r.ProjectionRevisionID != uuid.Nil && r.ProjectionRevision >= 1 &&
		r.ArtifactRevisionID != uuid.Nil && r.ArtifactRevision >= 1
}

const (
	ReadyWindowDuration = 30 * time.Second
	ReconnectCycleLimit = 2
)

func IsValidReadyWindowInterval(openedAt, deadline time.Time) bool {
	return IsValidServerTime(openedAt) && IsValidServerTime(deadline) &&
		deadline.Equal(openedAt.Add(ReadyWindowDuration))
}
