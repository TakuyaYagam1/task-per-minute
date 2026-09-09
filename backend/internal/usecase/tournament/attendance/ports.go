package attendance

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type AttendanceClock interface {
	Now() time.Time
}

func attendanceValidServerTime(value time.Time) bool {
	return domain.IsValidServerTime(value)
}

type ParticipantRecord struct {
	ID           uuid.UUID
	RosterID     uuid.UUID
	TournamentID uuid.UUID
	PlayerID     uuid.UUID
	Seed         int
	Attendance   domain.AttendanceState
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type ParticipantInput struct {
	ID         uuid.UUID
	RosterID   uuid.UUID
	PlayerID   uuid.UUID
	Seed       int32
	Attendance domain.AttendanceState
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
	Expected      domain.AttendanceState
	Next          domain.AttendanceState
}

type ParticipantReplacementCommand struct {
	WithdrawnParticipantID   uuid.UUID
	ReplacementParticipantID uuid.UUID
	RosterID                 uuid.UUID
	ReplacementPlayerID      uuid.UUID
}

type AttendanceRepository interface {
	InviteParticipant(ctx context.Context, in ParticipantInput) (*ParticipantRecord, bool, error)
	ChangeAttendance(
		ctx context.Context,
		participantID uuid.UUID,
		expected domain.AttendanceState,
		next domain.AttendanceState,
		updatedAt time.Time,
	) (*ParticipantRecord, bool, error)
	ReplaceWithdrawnParticipant(
		ctx context.Context,
		in ParticipantReplacementInput,
	) (*ParticipantRecord, bool, error)
	ListRosterParticipants(ctx context.Context, rosterID uuid.UUID) ([]ParticipantRecord, error)
}
