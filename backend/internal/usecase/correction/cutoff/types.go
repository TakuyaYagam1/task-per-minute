package cutoff

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/revision"
)

type CutoffKind string

const (
	CutoffWaveStarted     CutoffKind = "wave_started"
	CutoffTaskDelivered   CutoffKind = "task_delivered"
	CutoffNoShowRecorded  CutoffKind = "no_show_recorded"
	CutoffForfeitRecorded CutoffKind = "forfeit_recorded"
	CutoffGoldenAllocated CutoffKind = "golden_direct_allocated"
)

type CutoffEvent struct {
	ID               uuid.UUID
	Kind             CutoffKind
	TournamentID     uuid.UUID
	SourceRevisionID domain.DerivedRevisionID
	OccurredAt       time.Time
}

type CutoffInput struct {
	DAG              resultprojection.RevisionDAG
	TournamentID     uuid.UUID
	TargetRevisionID domain.DerivedRevisionID
	TournamentState  domain.TournamentState
	Events           []CutoffEvent
}

type Cutoff struct {
	tournamentID     uuid.UUID
	targetRevisionID domain.DerivedRevisionID
	descendants      []domain.DerivedRevision
}

func (c Cutoff) TournamentID() uuid.UUID {
	return c.tournamentID
}

func (c Cutoff) TargetRevisionID() domain.DerivedRevisionID {
	return c.targetRevisionID
}

func (c Cutoff) Descendants() []domain.DerivedRevision {
	return append([]domain.DerivedRevision(nil), c.descendants...)
}
