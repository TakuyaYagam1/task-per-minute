package arena

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/taskexec"
)

type GameScope struct {
	TournamentID uuid.UUID
	SeriesID     uuid.UUID
	SlotID       uuid.UUID
	GameID       uuid.UUID
}

type TaskSnapshotInput = taskexec.SnapshotInput

type FlagValidationInput = taskexec.FlagValidationInput

type Submission = taskexec.Submission

type TournamentRecord struct {
	ID              uuid.UUID
	RosterID        uuid.UUID
	Preset          domain.ArenaPreset
	State           domain.ArenaTournamentState
	PausedFromState *domain.ArenaTournamentState
	Revision        int64
	RosterSize      int
	CreatedAt       time.Time
	UpdatedAt       time.Time
	StartedAt       *time.Time
	FinishedAt      *time.Time
}

type RosterRecord struct {
	ID                 uuid.UUID
	TournamentID       uuid.UUID
	Revision           int64
	LockedAt           *time.Time
	ExecutionStartedAt *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type TournamentCreateCommand struct {
	TournamentID uuid.UUID
	RosterID     uuid.UUID
}

type TournamentListFilter struct {
	States []domain.ArenaTournamentState
}

type ParticipantRecord struct {
	ID           uuid.UUID
	RosterID     uuid.UUID
	TournamentID uuid.UUID
	PlayerID     uuid.UUID
	Seed         int
	Attendance   domain.ArenaAttendanceState
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type ParticipantInput struct {
	ID         uuid.UUID
	RosterID   uuid.UUID
	PlayerID   uuid.UUID
	Seed       int32
	Attendance domain.ArenaAttendanceState
	CreatedAt  time.Time
}

type ParticipantReplacementInput struct {
	WithdrawnParticipantID   uuid.UUID
	ReplacementParticipantID uuid.UUID
	RosterID                 uuid.UUID
	ReplacementPlayerID      uuid.UUID
	ReplacedAt               time.Time
}

type ParticipantInvitationCommand struct {
	ParticipantID uuid.UUID
	RosterID      uuid.UUID
	PlayerID      uuid.UUID
	Seed          int
}

type AttendanceChangeCommand struct {
	ParticipantID uuid.UUID
	Expected      domain.ArenaAttendanceState
	Next          domain.ArenaAttendanceState
}

type ParticipantReplacementCommand struct {
	WithdrawnParticipantID   uuid.UUID
	ReplacementParticipantID uuid.UUID
	RosterID                 uuid.UUID
	ReplacementPlayerID      uuid.UUID
}

type RosterPreflightEvidence struct {
	RosterID           uuid.UUID
	RosterRevision     int64
	CheckedInPlayerIDs []uuid.UUID
	Approved           bool
}

type RosterLockCommand struct {
	Preflight RosterPreflightEvidence
}

type RosterUnlockCommand struct {
	RosterID         uuid.UUID
	ExpectedRevision int64
	ActorID          uuid.UUID
	Reason           string
}

type TournamentLifecycleCommand struct {
	TournamentID     uuid.UUID
	ExpectedRevision int64
	NextState        domain.ArenaTournamentState
}

type TournamentLifecycleTransitionInput struct {
	TournamentID     uuid.UUID
	ExpectedRevision int64
	ExpectedState    domain.ArenaTournamentState
	NextState        domain.ArenaTournamentState
	PausedFromState  *domain.ArenaTournamentState
	TransitionedAt   time.Time
	StartedAt        *time.Time
	FinishedAt       *time.Time
}

type TournamentCancellationCommand struct {
	TournamentID     uuid.UUID
	ExpectedRevision int64
	CommandID        uuid.UUID
	ActorID          uuid.UUID
	Reason           string
	Confirmed        bool
}

type TournamentCancellationInput struct {
	TournamentID     uuid.UUID
	ExpectedRevision int64
	ExpectedState    domain.ArenaTournamentState
	CommandID        uuid.UUID
	ActorID          uuid.UUID
	Reason           string
	CancelledAt      time.Time
}

type TournamentCancellationRecord struct {
	Tournament    TournamentRecord
	CommandID     uuid.UUID
	ActorID       uuid.UUID
	Reason        string
	AuditEventID  uuid.UUID
	OutboxEventID uuid.UUID
	CancelledAt   time.Time
	ChampionID    *uuid.UUID
}

type TournamentTechnicalPauseCommand struct {
	TournamentID     uuid.UUID
	ExpectedRevision int64
	CommandID        uuid.UUID
	PauseID          uuid.UUID
	ActorID          uuid.UUID
	Reason           string
	Confirmed        bool
}

type TournamentPauseAdmission struct {
	TournamentID     uuid.UUID
	GraphRevision    int64
	ExpectedChildren int
	ObservedChildren int
	ActiveGolden     bool
	Complete         bool
}

type TournamentTechnicalPauseInput struct {
	TournamentID     uuid.UUID
	ExpectedRevision int64
	ExpectedState    domain.ArenaTournamentState
	GraphRevision    int64
	CommandID        uuid.UUID
	PauseID          uuid.UUID
	ActorID          uuid.UUID
	Reason           string
	PausedAt         time.Time
}

type TournamentTechnicalPauseRecord struct {
	Tournament TournamentRecord
	CommandID  uuid.UUID
	PauseID    uuid.UUID
	ActorID    uuid.UUID
	Reason     string
	PausedAt   time.Time
}
