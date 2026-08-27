package arena

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type GameScope struct {
	TournamentID uuid.UUID
	SeriesID     uuid.UUID
	SlotID       uuid.UUID
	GameID       uuid.UUID
}

type TaskSnapshotInput struct {
	SnapshotID uuid.UUID
	Version    int
	Kind       domain.ArenaTaskKind
	Task       domain.Task
}

type FlagValidationInput struct {
	ParticipantID uuid.UUID
	Snapshot      domain.ArenaTaskSnapshot
	SubmittedFlag string
}

type Submission struct {
	ID            uuid.UUID
	ParticipantID uuid.UUID
	ReceivedAt    time.Time
}
