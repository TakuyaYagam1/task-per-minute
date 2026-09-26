package admission

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

type Clock interface {
	Now() time.Time
}

// These errors stay package-local so the HTTP adapter can preserve the
// existing generic conflict response while callers can distinguish closed,
// full, withdrawn, and reservation conflicts when needed.
var (
	ErrTournamentAdmissionClosed    = errors.New("tournament admission is closed")
	ErrTournamentAdmissionFull      = errors.New("tournament admission is full")
	ErrTournamentAdmissionConflict  = errors.New("player has a conflicting tournament reservation")
	ErrTournamentAdmissionWithdrawn = errors.New("tournament admission was withdrawn")
)

// AdmissionRecord is the repository-owned registration view. A nil
// participant is represented by ParticipantID == uuid.Nil and
// Attendance == "" for a valid not_registered status.
type AdmissionRecord struct {
	TournamentID      uuid.UUID
	PlayerID          uuid.UUID
	ParticipantID     uuid.UUID
	Seed              int
	Attendance        domain.AttendanceState
	TournamentState   domain.TournamentState
	RosterLocked      bool
	RosterRevision    int64
	PlannedRosterSize int
	RosterSize        int
}

type JoinInput struct {
	TournamentID  uuid.UUID
	PlayerID      uuid.UUID
	ParticipantID uuid.UUID
	CommandID     uuid.UUID
	JoinedAt      time.Time
}

type CancelInput struct {
	TournamentID uuid.UUID
	PlayerID     uuid.UUID
	CommandID    uuid.UUID
	CancelledAt  time.Time
}

type AdmissionRepository interface {
	Join(ctx context.Context, input JoinInput) (AdmissionRecord, bool, error)
	GetStatus(ctx context.Context, tournamentID, playerID uuid.UUID) (AdmissionRecord, error)
	Cancel(ctx context.Context, input CancelInput) (AdmissionRecord, bool, error)
}

var _ usecase.TournamentAdmissionUseCase = (*AdmissionUseCase)(nil)
