package game

import (
	"crypto/sha256"
	"time"

	"github.com/google/uuid"

	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

type Scope struct {
	TournamentID uuid.UUID
	SeriesID     uuid.UUID
	SlotID       uuid.UUID
	GameID       uuid.UUID
}

func (s Scope) IsValid() bool {
	return s.TournamentID != uuid.Nil && s.SeriesID != uuid.Nil &&
		s.SlotID != uuid.Nil && s.GameID != uuid.Nil
}

type SubmissionScope struct {
	WaveID       uuid.UUID
	Game         Scope
	AssignmentID uuid.UUID
}

func (s SubmissionScope) IsValid() bool {
	return s.WaveID != uuid.Nil && s.AssignmentID != uuid.Nil && s.Game.IsValid()
}

type Started struct {
	Scope              Scope
	ParticipantIDs     [2]uuid.UUID
	Series             seriesdomain.Execution
	AssignmentID       uuid.UUID
	AssignmentRevision int64
	PlanRevisionID     uuid.UUID
	SnapshotID         uuid.UUID
	ContentDigest      [sha256.Size]byte
	DeadlineSeconds    int
	StartedAt          time.Time
	Deadline           time.Time
	DeliveryEnabled    bool
}

type Submission struct {
	Scope         SubmissionScope
	CommandID     uuid.UUID
	ParticipantID uuid.UUID
	Sequence      int64
	CommittedAt   time.Time
	Correct       bool
	SnapshotID    uuid.UUID
	TaskID        uuid.UUID
	ContentDigest [sha256.Size]byte
}
