package attendance

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var AttendanceErrRosterNotFound = errors.New("tournament roster not found")

type AttendanceUseCase struct {
	repository AttendanceRepository
	clock      AttendanceClock
}

func NewAttendanceUseCase(repository AttendanceRepository, clock AttendanceClock) *AttendanceUseCase {
	return &AttendanceUseCase{repository: repository, clock: clock}
}

func (u *AttendanceUseCase) InviteParticipant(
	ctx context.Context,
	command ParticipantInvitationCommand,
) (*ParticipantRecord, bool, error) {
	if !u.isAvailable() || !validParticipantInvitationCommand(command) {
		return nil, false, domain.ErrValidation
	}

	participants, err := u.repository.ListRosterParticipants(ctx, command.RosterID)
	if err != nil {
		return nil, false, fmt.Errorf("AttendanceUseCase - InviteParticipant - AttendanceRepository.ListParticipants: %w", err)
	}
	if existing, matched := matchingInvitation(participants, command); existing != nil {
		if matched {
			return cloneParticipantRecord(*existing), false, nil
		}
		return nil, false, domain.ErrConflict
	}
	if len(participants) >= domain.TournamentMaxParticipants {
		return nil, false, domain.ErrConflict
	}

	createdAt := u.clock.Now()
	if !attendanceValidServerTime(createdAt) {
		return nil, false, domain.ErrValidation
	}
	created, changed, err := u.repository.InviteParticipant(ctx, ParticipantInput{
		ID: command.ParticipantID, RosterID: command.RosterID, PlayerID: command.PlayerID,
		Seed: participantSeedToInt32(command.Seed), Attendance: domain.AttendanceStateInvited, CreatedAt: createdAt,
	})
	if err != nil {
		return nil, false, attendanceMutationError("InviteParticipant", "AddParticipant", err)
	}
	if !changed || created == nil || !participantMatchesInvitation(*created, command) {
		return nil, false, domain.ErrConflict
	}
	return cloneParticipantRecord(*created), true, nil
}

func (u *AttendanceUseCase) ChangeAttendance(
	ctx context.Context,
	command AttendanceChangeCommand,
) (*ParticipantRecord, bool, error) {
	if !u.isAvailable() || !validAttendanceChangeCommand(command) {
		return nil, false, domain.ErrValidation
	}
	updatedAt := u.clock.Now()
	if !attendanceValidServerTime(updatedAt) {
		return nil, false, domain.ErrValidation
	}
	updated, changed, err := u.repository.ChangeAttendance(
		ctx, command.ParticipantID, command.Expected, command.Next, updatedAt,
	)
	if err != nil {
		return nil, false, attendanceMutationError("ChangeAttendance", "UpdateAttendance", err)
	}
	if !changed || updated == nil || updated.ID != command.ParticipantID || updated.Attendance != command.Next {
		return nil, false, domain.ErrConflict
	}
	return cloneParticipantRecord(*updated), true, nil
}

func (u *AttendanceUseCase) ReplaceWithdrawnParticipant(
	ctx context.Context,
	command ParticipantReplacementCommand,
) (*ParticipantRecord, bool, error) {
	if !u.isAvailable() || !validParticipantReplacementCommand(command) {
		return nil, false, domain.ErrValidation
	}
	replacedAt := u.clock.Now()
	if !attendanceValidServerTime(replacedAt) {
		return nil, false, domain.ErrValidation
	}
	replacement, changed, err := u.repository.ReplaceWithdrawnParticipant(ctx, ParticipantReplacementInput{
		WithdrawnParticipantID: command.WithdrawnParticipantID, ReplacementParticipantID: command.ReplacementParticipantID,
		RosterID: command.RosterID, ReplacementPlayerID: command.ReplacementPlayerID, ReplacedAt: replacedAt,
	})
	if err != nil {
		return nil, false, attendanceMutationError(
			"ReplaceWithdrawnParticipant", "ReplaceWithdrawnParticipant", err,
		)
	}
	if !changed || replacement == nil || replacement.ID != command.ReplacementParticipantID ||
		replacement.RosterID != command.RosterID || replacement.PlayerID != command.ReplacementPlayerID ||
		replacement.Attendance != domain.AttendanceStateInvited {
		return nil, false, domain.ErrConflict
	}
	return cloneParticipantRecord(*replacement), true, nil
}

func (u *AttendanceUseCase) isAvailable() bool {
	return u != nil && u.repository != nil && u.clock != nil
}

func validParticipantInvitationCommand(command ParticipantInvitationCommand) bool {
	return command.ParticipantID != uuid.Nil && command.RosterID != uuid.Nil && command.PlayerID != uuid.Nil &&
		command.Seed >= 1 && command.Seed <= math.MaxInt32
}

func participantSeedToInt32(seed int) int32 {
	if seed < 1 {
		return 0
	}
	if seed > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(seed)
}

func validAttendanceChangeCommand(command AttendanceChangeCommand) bool {
	return command.ParticipantID != uuid.Nil && command.Expected.IsValid() && command.Next.IsValid() &&
		command.Expected != command.Next && command.Expected.CanTransitionTo(command.Next)
}

func validParticipantReplacementCommand(command ParticipantReplacementCommand) bool {
	return command.WithdrawnParticipantID != uuid.Nil && command.ReplacementParticipantID != uuid.Nil &&
		command.RosterID != uuid.Nil && command.ReplacementPlayerID != uuid.Nil &&
		command.WithdrawnParticipantID != command.ReplacementParticipantID
}

func attendanceMutationError(useCaseOperation string, repositoryOperation string, err error) error {
	if errors.Is(err, domain.ErrConflict) {
		return domain.ErrConflict
	}
	return fmt.Errorf(
		"AttendanceUseCase - %s - AttendanceRepository.%s: %w",
		useCaseOperation,
		repositoryOperation,
		err,
	)
}

func matchingInvitation(
	participants []ParticipantRecord,
	command ParticipantInvitationCommand,
) (*ParticipantRecord, bool) {
	for i := range participants {
		participant := &participants[i]
		if participant.ID != command.ParticipantID && participant.PlayerID != command.PlayerID &&
			participant.Seed != command.Seed {
			continue
		}
		return participant, participantMatchesInvitation(*participant, command)
	}
	return nil, false
}

func participantMatchesInvitation(participant ParticipantRecord, command ParticipantInvitationCommand) bool {
	return participant.ID == command.ParticipantID && participant.RosterID == command.RosterID &&
		participant.PlayerID == command.PlayerID && participant.Seed == command.Seed &&
		participant.Attendance == domain.AttendanceStateInvited
}

func cloneParticipantRecord(record ParticipantRecord) *ParticipantRecord {
	cloned := record
	return &cloned
}
