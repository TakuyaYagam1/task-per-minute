package admission

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

type AdmissionUseCase struct {
	repository AdmissionRepository
	clock      Clock
}

func NewAdmissionUseCase(repository AdmissionRepository, clock Clock) *AdmissionUseCase {
	return &AdmissionUseCase{repository: repository, clock: clock}
}

func (a *AdmissionUseCase) Join(
	ctx context.Context,
	command usecase.TournamentAdmissionJoinCommand,
) (usecase.TournamentAdmissionMutation, error) {
	if !a.available() || ctx == nil || !validJoinCommand(command) {
		return usecase.TournamentAdmissionMutation{}, domain.ErrValidation
	}
	joinedAt := a.clock.Now()
	if !domain.IsValidServerTime(joinedAt) {
		return usecase.TournamentAdmissionMutation{}, domain.ErrValidation
	}

	record, changed, err := a.repository.Join(ctx, JoinInput{
		TournamentID:  command.TournamentID,
		PlayerID:      command.Actor.PlayerID,
		ParticipantID: uuid.New(),
		CommandID:     command.CommandID,
		JoinedAt:      joinedAt,
	})
	if err != nil {
		return usecase.TournamentAdmissionMutation{}, mapAdmissionError(err)
	}
	view, err := admissionView(record, command.TournamentID, command.Actor.PlayerID)
	if err != nil {
		return usecase.TournamentAdmissionMutation{}, err
	}
	return usecase.TournamentAdmissionMutation{View: view, Changed: changed}, nil
}

func (a *AdmissionUseCase) CheckIn(
	ctx context.Context,
	command usecase.TournamentAdmissionCheckInCommand,
) (usecase.TournamentAdmissionMutation, error) {
	if !a.available() || ctx == nil || !validCheckInCommand(command) {
		return usecase.TournamentAdmissionMutation{}, domain.ErrValidation
	}
	checkedInAt := a.clock.Now()
	if !domain.IsValidServerTime(checkedInAt) {
		return usecase.TournamentAdmissionMutation{}, domain.ErrValidation
	}

	record, changed, err := a.repository.CheckIn(ctx, CheckInInput{
		TournamentID: command.TournamentID,
		PlayerID:     command.Actor.PlayerID,
		CommandID:    command.CommandID,
		CheckedInAt:  checkedInAt,
	})
	if err != nil {
		return usecase.TournamentAdmissionMutation{}, mapAdmissionError(err)
	}
	view, err := admissionView(record, command.TournamentID, command.Actor.PlayerID)
	if err != nil {
		return usecase.TournamentAdmissionMutation{}, err
	}
	return usecase.TournamentAdmissionMutation{View: view, Changed: changed}, nil
}

func (a *AdmissionUseCase) GetStatus(
	ctx context.Context,
	query usecase.TournamentAdmissionStatusQuery,
) (usecase.TournamentAdmissionView, error) {
	if !a.available() || ctx == nil || !validStatusQuery(query) {
		return usecase.TournamentAdmissionView{}, domain.ErrValidation
	}
	record, err := a.repository.GetStatus(ctx, query.TournamentID, query.Actor.PlayerID)
	if err != nil {
		return usecase.TournamentAdmissionView{}, mapAdmissionError(err)
	}
	if record.TournamentState == domain.TournamentStateDraft {
		return usecase.TournamentAdmissionView{}, domain.ErrTournamentNotFound
	}
	return admissionView(record, query.TournamentID, query.Actor.PlayerID)
}

func (a *AdmissionUseCase) Cancel(
	ctx context.Context,
	command usecase.TournamentAdmissionCancelCommand,
) (usecase.TournamentAdmissionMutation, error) {
	if !a.available() || ctx == nil || !validCancelCommand(command) {
		return usecase.TournamentAdmissionMutation{}, domain.ErrValidation
	}
	cancelledAt := a.clock.Now()
	if !domain.IsValidServerTime(cancelledAt) {
		return usecase.TournamentAdmissionMutation{}, domain.ErrValidation
	}

	record, changed, err := a.repository.Cancel(ctx, CancelInput{
		TournamentID: command.TournamentID,
		PlayerID:     command.Actor.PlayerID,
		CommandID:    command.CommandID,
		CancelledAt:  cancelledAt,
	})
	if err != nil {
		return usecase.TournamentAdmissionMutation{}, mapAdmissionError(err)
	}
	view, err := admissionView(record, command.TournamentID, command.Actor.PlayerID)
	if err != nil {
		return usecase.TournamentAdmissionMutation{}, err
	}
	return usecase.TournamentAdmissionMutation{View: view, Changed: changed}, nil
}

func (a *AdmissionUseCase) available() bool {
	return a != nil && a.repository != nil && a.clock != nil
}

func validJoinCommand(command usecase.TournamentAdmissionJoinCommand) bool {
	return validIdentity(command.Actor, command.TournamentID) && command.CommandID != uuid.Nil
}

func validStatusQuery(query usecase.TournamentAdmissionStatusQuery) bool {
	return validIdentity(query.Actor, query.TournamentID)
}

func validCheckInCommand(command usecase.TournamentAdmissionCheckInCommand) bool {
	return validIdentity(command.Actor, command.TournamentID) && command.CommandID != uuid.Nil
}

func validCancelCommand(command usecase.TournamentAdmissionCancelCommand) bool {
	return validIdentity(command.Actor, command.TournamentID) && command.CommandID != uuid.Nil
}

func validIdentity(actor usecase.Identity, tournamentID uuid.UUID) bool {
	return actor.PlayerID != uuid.Nil && tournamentID != uuid.Nil
}

func mapAdmissionError(err error) error {
	switch {
	case errors.Is(err, ErrTournamentAdmissionClosed),
		errors.Is(err, ErrTournamentAdmissionFull),
		errors.Is(err, ErrTournamentAdmissionConflict),
		errors.Is(err, ErrTournamentAdmissionWithdrawn):
		return domain.WrapError(err, domain.ErrConflict)
	default:
		return err
	}
}

func admissionView(record AdmissionRecord, tournamentID, playerID uuid.UUID) (usecase.TournamentAdmissionView, error) {
	if err := validateAdmissionRecord(record, tournamentID, playerID); err != nil {
		return usecase.TournamentAdmissionView{}, err
	}
	status, err := admissionStatus(record)
	if err != nil {
		return usecase.TournamentAdmissionView{}, err
	}
	return usecase.TournamentAdmissionView{
		TournamentID:      record.TournamentID,
		PlayerID:          record.PlayerID,
		ParticipantID:     record.ParticipantID,
		Seed:              record.Seed,
		Status:            status,
		Attendance:        record.Attendance,
		TournamentState:   record.TournamentState,
		RosterLocked:      record.RosterLocked,
		RosterRevision:    record.RosterRevision,
		PlannedRosterSize: record.PlannedRosterSize,
		RosterSize:        record.RosterSize,
	}, nil
}

func validateAdmissionRecord(record AdmissionRecord, tournamentID, playerID uuid.UUID) error {
	if !validAdmissionIdentity(record, tournamentID, playerID) || !validAdmissionRoster(record) {
		return domain.ErrInternal
	}
	if record.ParticipantID == uuid.Nil {
		return validUnregisteredAdmission(record)
	}
	if record.Seed < 1 || record.Seed > domain.TournamentMaxParticipants || !record.Attendance.IsValid() {
		return domain.ErrInternal
	}
	return nil
}

func validAdmissionIdentity(record AdmissionRecord, tournamentID, playerID uuid.UUID) bool {
	return record.TournamentID == tournamentID && record.PlayerID == playerID &&
		record.TournamentID != uuid.Nil && record.PlayerID != uuid.Nil && record.TournamentState.IsValid()
}

func validAdmissionRoster(record AdmissionRecord) bool {
	return record.RosterRevision >= 1 && record.PlannedRosterSize >= domain.TournamentMinParticipants &&
		record.PlannedRosterSize <= domain.TournamentMaxParticipants && record.RosterSize >= 0 &&
		record.RosterSize <= domain.TournamentMaxParticipants
}

func validUnregisteredAdmission(record AdmissionRecord) error {
	if record.Seed != 0 || record.Attendance != "" {
		return domain.ErrInternal
	}
	return nil
}

func admissionStatus(record AdmissionRecord) (usecase.TournamentAdmissionStatus, error) {
	if record.ParticipantID == uuid.Nil {
		return usecase.TournamentAdmissionStatusNotRegistered, nil
	}
	switch record.Attendance {
	case domain.AttendanceStateInvited:
		return usecase.TournamentAdmissionStatusInvited, nil
	case domain.AttendanceStateRegistered:
		return usecase.TournamentAdmissionStatusRegistered, nil
	case domain.AttendanceStateCheckedIn:
		return usecase.TournamentAdmissionStatusCheckedIn, nil
	case domain.AttendanceStateWithdrawn:
		return usecase.TournamentAdmissionStatusWithdrawn, nil
	default:
		return "", domain.ErrInternal
	}
}
