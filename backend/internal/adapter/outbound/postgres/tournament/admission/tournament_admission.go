package admission

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	admissionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admission"
)

type TournamentAdmissionPostgres struct {
	tx *db.TxManager
}

func NewTournamentAdmissionPostgres(tx *db.TxManager) *TournamentAdmissionPostgres {
	return &TournamentAdmissionPostgres{tx: tx}
}

func (r *TournamentAdmissionPostgres) Join(
	ctx context.Context,
	input admissionusecase.JoinInput,
) (admissionusecase.AdmissionRecord, bool, error) {
	if !validJoinInput(ctx, r, input) {
		return admissionusecase.AdmissionRecord{}, false, domain.ErrValidation
	}

	var record admissionusecase.AdmissionRecord
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		record, changed, err = r.joinInTransaction(txCtx, input)
		return err
	})
	if err != nil {
		return admissionusecase.AdmissionRecord{}, false, err
	}
	return record, changed, nil
}

func (r *TournamentAdmissionPostgres) joinInTransaction(
	ctx context.Context,
	input admissionusecase.JoinInput,
) (admissionusecase.AdmissionRecord, bool, error) {
	querier := r.tx.Querier(ctx)
	scope, err := querier.LockTournamentAdmissionScope(ctx, input.TournamentID)
	if err != nil {
		return admissionusecase.AdmissionRecord{}, false, admissionLookupError("Join - lock scope", err)
	}
	if err := validateAdmissionScope(scope); err != nil {
		return admissionusecase.AdmissionRecord{}, false, err
	}
	if scope.TournamentState == string(domain.TournamentStateDraft) {
		return admissionusecase.AdmissionRecord{}, false, domain.ErrTournamentNotFound
	}
	if scope.TournamentState != string(domain.TournamentStateRegistration) || scope.RosterLocked || scope.RosterExecutionStarted {
		return admissionusecase.AdmissionRecord{}, false, admissionusecase.ErrTournamentAdmissionClosed
	}
	if _, err := querier.LockAdmissionPlayer(ctx, input.PlayerID); err != nil {
		return admissionusecase.AdmissionRecord{}, false, admissionPlayerLookupError("Join - lock player", err)
	}
	status, err := querier.GetTournamentAdmissionStatus(ctx, sqlc.GetTournamentAdmissionStatusParams{
		PlayerID: input.PlayerID, TournamentID: input.TournamentID,
	})
	if err != nil {
		return admissionusecase.AdmissionRecord{}, false, admissionLookupError("Join - read status", err)
	}
	current, err := admissionRecordFromStatus(status, input.PlayerID)
	if err != nil {
		return admissionusecase.AdmissionRecord{}, false, err
	}
	switch current.Attendance {
	case domain.AttendanceStateRegistered, domain.AttendanceStateCheckedIn:
		return current, false, nil
	case domain.AttendanceStateWithdrawn:
		return r.joinWithdrawnInTransaction(ctx, input, scope, status.RosterSize)
	case domain.AttendanceStateInvited:
		return r.joinInvitedInTransaction(ctx, input, scope)
	default:
		if current.ParticipantID != uuid.Nil {
			return admissionusecase.AdmissionRecord{}, false, domain.ErrInternal
		}
	}
	return r.joinRegisteredInTransaction(ctx, input, scope, status.RosterSize)
}

func (r *TournamentAdmissionPostgres) joinInvitedInTransaction(
	ctx context.Context,
	input admissionusecase.JoinInput,
	scope sqlc.LockTournamentAdmissionScopeRow,
) (admissionusecase.AdmissionRecord, bool, error) {
	querier := r.tx.Querier(ctx)
	conflicting, err := querier.HasConflictingParticipantReservation(ctx, sqlc.HasConflictingParticipantReservationParams{
		PlayerID: input.PlayerID, TournamentID: input.TournamentID,
	})
	if err != nil {
		return admissionusecase.AdmissionRecord{}, false, admissionMutationError("Join - invitation reservation check", err)
	}
	if conflicting {
		return admissionusecase.AdmissionRecord{}, false, admissionusecase.ErrTournamentAdmissionConflict
	}
	if _, err := querier.RegisterInvitedParticipant(ctx, sqlc.RegisterInvitedParticipantParams{
		PlayerID: input.PlayerID, RosterID: scope.RosterID, UpdatedAt: admissionTimestamp(input.JoinedAt),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admissionusecase.AdmissionRecord{}, false, domain.ErrConflict
		}
		return admissionusecase.AdmissionRecord{}, false, admissionMutationError("Join - register invitation", err)
	}
	record, err := r.statusInTransaction(ctx, input.TournamentID, input.PlayerID)
	return record, true, err
}

func (r *TournamentAdmissionPostgres) joinWithdrawnInTransaction(
	ctx context.Context,
	input admissionusecase.JoinInput,
	scope sqlc.LockTournamentAdmissionScopeRow,
	rosterSize int64,
) (admissionusecase.AdmissionRecord, bool, error) {
	if rosterSize >= int64(scope.PlannedRosterSize) {
		return admissionusecase.AdmissionRecord{}, false, admissionusecase.ErrTournamentAdmissionFull
	}
	querier := r.tx.Querier(ctx)
	conflicting, err := querier.HasConflictingParticipantReservation(ctx, sqlc.HasConflictingParticipantReservationParams{
		PlayerID: input.PlayerID, TournamentID: input.TournamentID,
	})
	if err != nil {
		return admissionusecase.AdmissionRecord{}, false, admissionMutationError("Join - reservation check", err)
	}
	if conflicting {
		return admissionusecase.AdmissionRecord{}, false, admissionusecase.ErrTournamentAdmissionConflict
	}
	seed, err := availableAdmissionSeed(ctx, querier, scope)
	if err != nil {
		return admissionusecase.AdmissionRecord{}, false, err
	}
	if _, err := querier.RegisterWithdrawnParticipant(ctx, sqlc.RegisterWithdrawnParticipantParams{
		PlayerID: input.PlayerID, RosterID: scope.RosterID, Seed: seed,
		UpdatedAt: admissionTimestamp(input.JoinedAt),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admissionusecase.AdmissionRecord{}, false, domain.ErrConflict
		}
		return admissionusecase.AdmissionRecord{}, false, admissionMutationError("Join - re-register participant", err)
	}
	record, err := r.statusInTransaction(ctx, input.TournamentID, input.PlayerID)
	return record, true, err
}

func (r *TournamentAdmissionPostgres) joinRegisteredInTransaction(
	ctx context.Context,
	input admissionusecase.JoinInput,
	scope sqlc.LockTournamentAdmissionScopeRow,
	rosterSize int64,
) (admissionusecase.AdmissionRecord, bool, error) {
	if rosterSize >= int64(scope.PlannedRosterSize) {
		return admissionusecase.AdmissionRecord{}, false, admissionusecase.ErrTournamentAdmissionFull
	}
	querier := r.tx.Querier(ctx)
	conflicting, err := querier.HasConflictingParticipantReservation(ctx, sqlc.HasConflictingParticipantReservationParams{
		PlayerID: input.PlayerID, TournamentID: input.TournamentID,
	})
	if err != nil {
		return admissionusecase.AdmissionRecord{}, false, admissionMutationError("Join - reservation check", err)
	}
	if conflicting {
		return admissionusecase.AdmissionRecord{}, false, admissionusecase.ErrTournamentAdmissionConflict
	}
	seed, err := availableAdmissionSeed(ctx, querier, scope)
	if err != nil {
		return admissionusecase.AdmissionRecord{}, false, err
	}
	if _, err := querier.InsertRegisteredParticipant(ctx, sqlc.InsertRegisteredParticipantParams{
		ParticipantID: input.ParticipantID,
		PlayerID:      input.PlayerID,
		RosterID:      scope.RosterID,
		Seed:          seed,
		CreatedAt:     admissionTimestamp(input.JoinedAt),
	}); err != nil {
		if isAdmissionConstraintConflict(err) {
			return admissionusecase.AdmissionRecord{}, false, domain.ErrConflict
		}
		return admissionusecase.AdmissionRecord{}, false, admissionMutationError("Join - insert participant", err)
	}
	record, err := r.statusInTransaction(ctx, input.TournamentID, input.PlayerID)
	return record, true, err
}

func (r *TournamentAdmissionPostgres) CheckIn(
	ctx context.Context,
	input admissionusecase.CheckInInput,
) (admissionusecase.AdmissionRecord, bool, error) {
	if !validCheckInInput(ctx, r, input) {
		return admissionusecase.AdmissionRecord{}, false, domain.ErrValidation
	}

	var record admissionusecase.AdmissionRecord
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		record, changed, err = r.checkInInTransaction(txCtx, input)
		return err
	})
	if err != nil {
		return admissionusecase.AdmissionRecord{}, false, err
	}
	return record, changed, nil
}

func (r *TournamentAdmissionPostgres) checkInInTransaction(
	ctx context.Context,
	input admissionusecase.CheckInInput,
) (admissionusecase.AdmissionRecord, bool, error) {
	querier := r.tx.Querier(ctx)
	scope, err := querier.LockTournamentAdmissionScope(ctx, input.TournamentID)
	if err != nil {
		return admissionusecase.AdmissionRecord{}, false, admissionLookupError("CheckIn - lock scope", err)
	}
	if err := validateAdmissionScope(scope); err != nil {
		return admissionusecase.AdmissionRecord{}, false, err
	}
	if scope.TournamentState == string(domain.TournamentStateDraft) {
		return admissionusecase.AdmissionRecord{}, false, domain.ErrTournamentNotFound
	}
	if scope.TournamentState != string(domain.TournamentStateRegistration) || scope.RosterLocked || scope.RosterExecutionStarted {
		return admissionusecase.AdmissionRecord{}, false, admissionusecase.ErrTournamentAdmissionClosed
	}
	if _, err := querier.LockAdmissionPlayer(ctx, input.PlayerID); err != nil {
		return admissionusecase.AdmissionRecord{}, false, admissionPlayerLookupError("CheckIn - lock player", err)
	}
	status, err := querier.GetTournamentAdmissionStatus(ctx, sqlc.GetTournamentAdmissionStatusParams{
		PlayerID: input.PlayerID, TournamentID: input.TournamentID,
	})
	if err != nil {
		return admissionusecase.AdmissionRecord{}, false, admissionLookupError("CheckIn - read status", err)
	}
	current, err := admissionRecordFromStatus(status, input.PlayerID)
	if err != nil {
		return admissionusecase.AdmissionRecord{}, false, err
	}
	if current.ParticipantID == uuid.Nil {
		return admissionusecase.AdmissionRecord{}, false, domain.ErrConflict
	}
	if current.Attendance == domain.AttendanceStateCheckedIn {
		return current, false, nil
	}
	if current.Attendance != domain.AttendanceStateRegistered {
		return admissionusecase.AdmissionRecord{}, false, domain.ErrConflict
	}
	if _, err := querier.CheckInRegisteredParticipant(ctx, sqlc.CheckInRegisteredParticipantParams{
		PlayerID: input.PlayerID, RosterID: scope.RosterID, UpdatedAt: admissionTimestamp(input.CheckedInAt),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admissionusecase.AdmissionRecord{}, false, domain.ErrConflict
		}
		return admissionusecase.AdmissionRecord{}, false, admissionMutationError("CheckIn - update participant", err)
	}
	record, err := r.statusInTransaction(ctx, input.TournamentID, input.PlayerID)
	return record, true, err
}

func availableAdmissionSeed(
	ctx context.Context,
	querier *sqlc.Queries,
	scope sqlc.LockTournamentAdmissionScopeRow,
) (int32, error) {
	rebase, err := querier.RebaseWithdrawnParticipantSeeds(ctx, sqlc.RebaseWithdrawnParticipantSeedsParams{
		PlannedRosterSize: scope.PlannedRosterSize,
		RosterID:          scope.RosterID,
	})
	if err != nil {
		return 0, admissionMutationError("Join - rebase withdrawn seeds", err)
	}
	canRebase, ok := rebase.CanRebase.(bool)
	if !ok || !canRebase {
		return 0, domain.ErrInternal
	}
	seed, err := querier.FindFirstAvailableParticipantSeed(ctx, sqlc.FindFirstAvailableParticipantSeedParams{
		PlannedRosterSize: scope.PlannedRosterSize,
		RosterID:          scope.RosterID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, admissionusecase.ErrTournamentAdmissionFull
	}
	if err != nil {
		return 0, admissionMutationError("Join - find seed", err)
	}
	return seed, nil
}

func (r *TournamentAdmissionPostgres) GetStatus(
	ctx context.Context,
	tournamentID, playerID uuid.UUID,
) (admissionusecase.AdmissionRecord, error) {
	if ctx == nil || r == nil || r.tx == nil || tournamentID == uuid.Nil || playerID == uuid.Nil {
		return admissionusecase.AdmissionRecord{}, domain.ErrValidation
	}
	var record admissionusecase.AdmissionRecord
	err := r.tx.ReadSnapshot(ctx, func(snapshotCtx context.Context) error {
		var err error
		record, err = r.statusInTransaction(snapshotCtx, tournamentID, playerID)
		return err
	})
	if err != nil {
		return admissionusecase.AdmissionRecord{}, err
	}
	return record, nil
}

func (r *TournamentAdmissionPostgres) Cancel(
	ctx context.Context,
	input admissionusecase.CancelInput,
) (admissionusecase.AdmissionRecord, bool, error) {
	if !validCancelInput(ctx, r, input) {
		return admissionusecase.AdmissionRecord{}, false, domain.ErrValidation
	}

	var record admissionusecase.AdmissionRecord
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		record, changed, err = r.cancelInTransaction(txCtx, input)
		return err
	})
	if err != nil {
		return admissionusecase.AdmissionRecord{}, false, err
	}
	return record, changed, nil
}

func (r *TournamentAdmissionPostgres) cancelInTransaction(
	ctx context.Context,
	input admissionusecase.CancelInput,
) (admissionusecase.AdmissionRecord, bool, error) {
	querier := r.tx.Querier(ctx)
	scope, err := querier.LockTournamentAdmissionScope(ctx, input.TournamentID)
	if err != nil {
		return admissionusecase.AdmissionRecord{}, false, admissionLookupError("Cancel - lock scope", err)
	}
	if err := validateAdmissionScope(scope); err != nil {
		return admissionusecase.AdmissionRecord{}, false, err
	}
	if scope.TournamentState == string(domain.TournamentStateDraft) {
		return admissionusecase.AdmissionRecord{}, false, domain.ErrTournamentNotFound
	}
	if scope.TournamentState != string(domain.TournamentStateRegistration) || scope.RosterLocked || scope.RosterExecutionStarted {
		return admissionusecase.AdmissionRecord{}, false, admissionusecase.ErrTournamentAdmissionClosed
	}
	if _, err := querier.LockAdmissionPlayer(ctx, input.PlayerID); err != nil {
		return admissionusecase.AdmissionRecord{}, false, admissionPlayerLookupError("Cancel - lock player", err)
	}
	status, err := querier.GetTournamentAdmissionStatus(ctx, sqlc.GetTournamentAdmissionStatusParams{
		PlayerID: input.PlayerID, TournamentID: input.TournamentID,
	})
	if err != nil {
		return admissionusecase.AdmissionRecord{}, false, admissionLookupError("Cancel - read status", err)
	}
	current, err := admissionRecordFromStatus(status, input.PlayerID)
	if err != nil {
		return admissionusecase.AdmissionRecord{}, false, err
	}
	if current.ParticipantID == uuid.Nil || current.Attendance == domain.AttendanceStateWithdrawn {
		return current, false, nil
	}
	if !cancelableAdmissionAttendance(current.Attendance) {
		return admissionusecase.AdmissionRecord{}, false, domain.ErrConflict
	}
	if _, err := querier.WithdrawAdmissionParticipant(ctx, sqlc.WithdrawAdmissionParticipantParams{
		PlayerID: input.PlayerID, RosterID: scope.RosterID, UpdatedAt: admissionTimestamp(input.CancelledAt),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admissionusecase.AdmissionRecord{}, false, domain.ErrConflict
		}
		return admissionusecase.AdmissionRecord{}, false, admissionMutationError("Cancel - withdraw participant", err)
	}
	record, err := r.statusInTransaction(ctx, input.TournamentID, input.PlayerID)
	return record, true, err
}

func cancelableAdmissionAttendance(attendance domain.AttendanceState) bool {
	switch attendance {
	case domain.AttendanceStateInvited, domain.AttendanceStateRegistered, domain.AttendanceStateCheckedIn:
		return true
	case domain.AttendanceStateWithdrawn:
		return false
	default:
		return false
	}
}

func (r *TournamentAdmissionPostgres) statusInTransaction(
	ctx context.Context,
	tournamentID, playerID uuid.UUID,
) (admissionusecase.AdmissionRecord, error) {
	row, err := r.tx.Querier(ctx).GetTournamentAdmissionStatus(ctx, sqlc.GetTournamentAdmissionStatusParams{
		PlayerID: playerID, TournamentID: tournamentID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return admissionusecase.AdmissionRecord{}, domain.ErrTournamentNotFound
	}
	if err != nil {
		return admissionusecase.AdmissionRecord{}, fmt.Errorf("TournamentAdmissionPostgres - status: %w", err)
	}
	return admissionRecordFromStatus(row, playerID)
}

func admissionRecordFromStatus(
	row sqlc.GetTournamentAdmissionStatusRow,
	playerID uuid.UUID,
) (admissionusecase.AdmissionRecord, error) {
	if row.ParticipantID != uuid.Nil && row.ParticipantPlayerID != playerID {
		return admissionusecase.AdmissionRecord{}, domain.ErrInternal
	}
	attendance := domain.AttendanceState(row.ParticipantAttendance)
	if row.ParticipantID == uuid.Nil {
		attendance = ""
	}
	return admissionusecase.AdmissionRecord{
		TournamentID:      row.TournamentID,
		PlayerID:          playerID,
		ParticipantID:     row.ParticipantID,
		Seed:              int(row.ParticipantSeed),
		Attendance:        attendance,
		TournamentState:   domain.TournamentState(row.TournamentState),
		RosterLocked:      row.RosterLocked,
		RosterRevision:    row.RosterRevision,
		PlannedRosterSize: int(row.PlannedRosterSize),
		RosterSize:        int(row.RosterSize),
	}, nil
}

func validateAdmissionScope(row sqlc.LockTournamentAdmissionScopeRow) error {
	if row.TournamentID == uuid.Nil || row.RosterID == uuid.Nil || row.RosterTournamentID != row.TournamentID ||
		row.TournamentRevision < 1 || row.RosterRevision < 1 ||
		row.PlannedRosterSize < domain.TournamentMinParticipants ||
		row.PlannedRosterSize > domain.TournamentMaxParticipants ||
		!domain.TournamentState(row.TournamentState).IsValid() {
		return domain.ErrInternal
	}
	return nil
}

func validJoinInput(ctx context.Context, r *TournamentAdmissionPostgres, input admissionusecase.JoinInput) bool {
	return ctx != nil && r != nil && r.tx != nil && input.TournamentID != uuid.Nil && input.PlayerID != uuid.Nil &&
		input.ParticipantID != uuid.Nil && input.CommandID != uuid.Nil && validServerTime(input.JoinedAt)
}

func validCancelInput(ctx context.Context, r *TournamentAdmissionPostgres, input admissionusecase.CancelInput) bool {
	return ctx != nil && r != nil && r.tx != nil && input.TournamentID != uuid.Nil && input.PlayerID != uuid.Nil &&
		input.CommandID != uuid.Nil && validServerTime(input.CancelledAt)
}

func validCheckInInput(ctx context.Context, r *TournamentAdmissionPostgres, input admissionusecase.CheckInInput) bool {
	return ctx != nil && r != nil && r.tx != nil && input.TournamentID != uuid.Nil && input.PlayerID != uuid.Nil &&
		input.CommandID != uuid.Nil && validServerTime(input.CheckedInAt)
}

func validServerTime(value time.Time) bool {
	return domain.IsValidServerTime(value)
}

func admissionTimestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func admissionLookupError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrTournamentNotFound
	}
	return fmt.Errorf("TournamentAdmissionPostgres - %s: %w", operation, err)
}

func admissionPlayerLookupError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrPlayerNotFound
	}
	return fmt.Errorf("TournamentAdmissionPostgres - %s: %w", operation, err)
}

func admissionMutationError(operation string, err error) error {
	if isAdmissionConstraintConflict(err) {
		return domain.ErrConflict
	}
	return fmt.Errorf("TournamentAdmissionPostgres - %s: %w", operation, err)
}

func isAdmissionConstraintConflict(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" || pgErr.Code == "23503" || pgErr.Code == "40001"
}

var _ admissionusecase.AdmissionRepository = (*TournamentAdmissionPostgres)(nil)
