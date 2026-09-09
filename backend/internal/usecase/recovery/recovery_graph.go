package recovery

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const RecoveryCursorSchemaVersion = 1

type RecoveryFailReason string

const (
	RecoveryFailReasonMissingResult      RecoveryFailReason = "missing_result"
	RecoveryFailReasonMissingScore       RecoveryFailReason = "missing_score"
	RecoveryFailReasonMissingPause       RecoveryFailReason = "missing_pause"
	RecoveryFailReasonMissingRevision    RecoveryFailReason = "missing_revision"
	RecoveryFailReasonMissingReservation RecoveryFailReason = "missing_reservation"
	RecoveryFailReasonMissingLease       RecoveryFailReason = "missing_lease"
	RecoveryFailReasonMissingDeadline    RecoveryFailReason = "missing_deadline"
	RecoveryFailReasonInvalidGraph       RecoveryFailReason = "invalid_graph"
	RecoveryFailReasonInvalidDAG         RecoveryFailReason = "invalid_dag"
	RecoveryFailReasonInvalidCursor      RecoveryFailReason = "invalid_cursor"
)

type RecoveryWorkKind string

const (
	RecoveryWorkGame        RecoveryWorkKind = "game"
	RecoveryWorkReadyWindow RecoveryWorkKind = "ready_window"
)

type RecoveryPauseKind string

const (
	RecoveryPauseTournament RecoveryPauseKind = "tournament"
	RecoveryPauseWave       RecoveryPauseKind = "wave"
	RecoveryPauseSeries     RecoveryPauseKind = "series"
	RecoveryPauseGame       RecoveryPauseKind = "game"
)

type RecoveryCursor struct {
	SchemaVersion      int
	TournamentID       uuid.UUID
	LastSequence       int64
	ProjectionRevision int64
	DerivedRevisionID  domain.DerivedRevisionID
}

type RecoverySeries struct {
	WaveID uuid.UUID
	Series domain.Series
}

type RecoveryAssignment struct {
	WaveID     uuid.UUID
	SeriesID   uuid.UUID
	SlotID     uuid.UUID
	GameID     uuid.UUID
	Assignment domain.Assignment
}

type RecoveryResultEvidence struct {
	Artifact          domain.ArtifactRef
	Reason            domain.GameResultReason
	WinnerID          *uuid.UUID
	RevisionID        domain.OfficialResultRevisionID
	DerivedRevisionID domain.DerivedRevisionID
}

type RecoveryScoreEvidence struct {
	SeriesID          uuid.UUID
	Score             domain.SeriesScore
	RevisionID        domain.SeriesScoreRevisionID
	DerivedRevisionID domain.DerivedRevisionID
}

type RecoveryPauseEvidence struct {
	Kind     RecoveryPauseKind
	EntityID uuid.UUID
	PausedAt time.Time
}

type RecoveryLease struct {
	TournamentID uuid.UUID
	LeaseID      uuid.UUID
	HolderID     uuid.UUID
	Revision     int64
	AcquiredAt   time.Time
	RenewedAt    time.Time
	ExpiresAt    time.Time
}

type RecoveryWork struct {
	Kind          RecoveryWorkKind
	TournamentID  uuid.UUID
	WaveID        uuid.UUID
	SeriesID      uuid.UUID
	SlotID        uuid.UUID
	GameID        uuid.UUID
	ReadyWindowID uuid.UUID
}

type RecoveryDeadlineEvidence struct {
	Work     RecoveryWork
	Deadline time.Time
}

type RecoveryGraph struct {
	TournamentID uuid.UUID
	Tournament   domain.Tournament
	Roster       domain.Roster
	Waves        []domain.Wave
	Series       []RecoverySeries
	Assignments  []RecoveryAssignment
	Results      []RecoveryResultEvidence
	Scores       []RecoveryScoreEvidence
	Pauses       []RecoveryPauseEvidence
	Revisions    domain.RevisionGraph
	Reservations []domain.ParticipantReservation
	Lease        *RecoveryLease
	Deadlines    []RecoveryDeadlineEvidence
	Cursor       RecoveryCursor
	LastSequence int64
}
