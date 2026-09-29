package usecase

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// TournamentAdmissionUseCase is the transport-neutral boundary for public
// tournament registration. The actor is always resolved from the player
// session by the inbound adapter.
type TournamentAdmissionUseCase interface {
	Join(ctx context.Context, command TournamentAdmissionJoinCommand) (TournamentAdmissionMutation, error)
	CheckIn(ctx context.Context, command TournamentAdmissionCheckInCommand) (TournamentAdmissionMutation, error)
	GetStatus(ctx context.Context, query TournamentAdmissionStatusQuery) (TournamentAdmissionView, error)
	Cancel(ctx context.Context, command TournamentAdmissionCancelCommand) (TournamentAdmissionMutation, error)
}

type TournamentAdmissionJoinCommand struct {
	Actor        Identity
	TournamentID uuid.UUID
	CommandID    uuid.UUID
}

type TournamentAdmissionStatusQuery struct {
	Actor        Identity
	TournamentID uuid.UUID
}

type TournamentAdmissionCheckInCommand struct {
	Actor        Identity
	TournamentID uuid.UUID
	CommandID    uuid.UUID
}

type TournamentAdmissionCancelCommand struct {
	Actor        Identity
	TournamentID uuid.UUID
	CommandID    uuid.UUID
}

// TournamentAdmissionStatus keeps the existing attendance vocabulary while
// adding an explicit empty state for status reads before registration.
type TournamentAdmissionStatus string

const (
	TournamentAdmissionStatusNotRegistered TournamentAdmissionStatus = "not_registered"
	TournamentAdmissionStatusInvited       TournamentAdmissionStatus = "invited"
	TournamentAdmissionStatusRegistered    TournamentAdmissionStatus = "registered"
	TournamentAdmissionStatusCheckedIn     TournamentAdmissionStatus = "checked_in"
	TournamentAdmissionStatusWithdrawn     TournamentAdmissionStatus = "withdrawn"
)

func (s TournamentAdmissionStatus) IsValid() bool {
	switch s {
	case TournamentAdmissionStatusNotRegistered,
		TournamentAdmissionStatusInvited,
		TournamentAdmissionStatusRegistered,
		TournamentAdmissionStatusCheckedIn,
		TournamentAdmissionStatusWithdrawn:
		return true
	default:
		return false
	}
}

type TournamentAdmissionView struct {
	TournamentID      uuid.UUID
	PlayerID          uuid.UUID
	ParticipantID     uuid.UUID
	Seed              int
	Status            TournamentAdmissionStatus
	Attendance        domain.AttendanceState
	TournamentState   domain.TournamentState
	RosterLocked      bool
	RosterRevision    int64
	PlannedRosterSize int
	RosterSize        int
}

type TournamentAdmissionMutation struct {
	View    TournamentAdmissionView
	Changed bool
}
