package roster

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrRosterNotFound = errors.New("tournament repository: roster not found")
	ErrRosterCAS      = errors.New("roster compare-and-set failed")
)

type RosterPostgres struct {
	tx *db.TxManager
}

type ParticipantInput struct {
	ID         uuid.UUID
	RosterID   uuid.UUID
	PlayerID   uuid.UUID
	Seed       int32
	Attendance domain.AttendanceState
	CreatedAt  time.Time
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

type RosterRecord struct {
	ID                 uuid.UUID
	TournamentID       uuid.UUID
	Revision           int64
	LockedAt           *time.Time
	ExecutionStartedAt *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type ReservationRecord struct {
	PlayerID      uuid.UUID
	ReservationID uuid.UUID
	TournamentID  uuid.UUID
	Revision      int64
	AcquiredAt    time.Time
	UpdatedAt     time.Time
}

func NewRosterPostgres(tx *db.TxManager) *RosterPostgres {
	return &RosterPostgres{tx: tx}
}

func (r *RosterPostgres) AddParticipant(
	ctx context.Context,
	in ParticipantInput,
) (*ParticipantRecord, bool, error) {
	if err := validateParticipantInput(in); err != nil {
		return nil, false, err
	}
	if r == nil || r.tx == nil {
		return nil, false, domain.ErrValidation
	}

	var row sqlc.Participant
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		roster, err := querier.LockTournamentRosterForUpdate(txCtx, in.RosterID)
		if err != nil {
			return err
		}
		if roster.LockedAt.Valid || roster.ExecutionStartedAt.Valid {
			return ErrRosterCAS
		}
		participants, err := querier.ListTournamentParticipants(txCtx, in.RosterID)
		if err != nil {
			return err
		}
		if len(participants) >= domain.TournamentMaxParticipants {
			return ErrRosterCAS
		}
		row, err = querier.InsertTournamentParticipant(txCtx, sqlc.InsertTournamentParticipantParams{
			ID: in.ID, PlayerID: in.PlayerID, Seed: in.Seed, Attendance: string(in.Attendance),
			CreatedAt: tstz(in.CreatedAt), RosterID: in.RosterID,
		})
		return err
	})
	if err != nil {
		if errors.Is(err, ErrRosterCAS) || errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		if isParticipantConflict(err) {
			return nil, false, domain.WrapError(err, domain.ErrConflict)
		}
		return nil, false, fmt.Errorf("TournamentPostgres - AddParticipant - Querier.InsertTournamentParticipant: %w", err)
	}
	record, err := r.participantRecord(ctx, row)
	if err != nil {
		return nil, false, fmt.Errorf("TournamentPostgres - AddParticipant - map participant: %w", err)
	}
	return record, true, nil
}

func (r *RosterPostgres) UpdateAttendance(
	ctx context.Context,
	participantID uuid.UUID,
	expected domain.AttendanceState,
	next domain.AttendanceState,
	updatedAt time.Time,
) (*ParticipantRecord, bool, error) {
	if participantID == uuid.Nil || !expected.IsValid() || !expected.CanTransitionTo(next) || !validServerTime(updatedAt) {
		return nil, false, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).UpdateTournamentParticipantAttendanceCAS(
		ctx,
		sqlc.UpdateTournamentParticipantAttendanceCASParams{
			NextAttendance: string(next), UpdatedAt: tstz(updatedAt), ID: participantID,
			ExpectedAttendance: string(expected),
		},
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf(
			"TournamentPostgres - UpdateAttendance - Querier.UpdateTournamentParticipantAttendanceCAS: %w", err,
		)
	}
	record, err := r.participantRecord(ctx, row)
	if err != nil {
		return nil, false, fmt.Errorf("TournamentPostgres - UpdateAttendance - map participant: %w", err)
	}
	return record, true, nil
}

func (r *RosterPostgres) ListParticipants(
	ctx context.Context,
	rosterID uuid.UUID,
) ([]ParticipantRecord, error) {
	rows, err := r.tx.Querier(ctx).ListTournamentParticipants(ctx, rosterID)
	if err != nil {
		return nil, fmt.Errorf("TournamentPostgres - ListParticipants - Querier.ListTournamentParticipants: %w", err)
	}
	out := make([]ParticipantRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, ParticipantRecord{
			ID: row.ID, RosterID: row.RosterID, TournamentID: row.TournamentID, PlayerID: row.PlayerID,
			Seed: int(row.Seed), Attendance: domain.AttendanceState(row.Attendance),
			CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		})
	}
	return out, nil
}

func (r *RosterPostgres) GetRoster(ctx context.Context, id uuid.UUID) (*RosterRecord, error) {
	row, err := r.tx.Querier(ctx).GetTournamentRoster(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRosterNotFound
		}
		return nil, fmt.Errorf("TournamentPostgres - GetRoster - Querier.GetTournamentRoster: %w", err)
	}
	return rosterRecord(row), nil
}

func (r *RosterPostgres) LockRosterAndReserve(
	ctx context.Context,
	rosterID uuid.UUID,
	expectedRevision int64,
	lockedAt time.Time,
) (*RosterRecord, bool, error) {
	if rosterID == uuid.Nil || expectedRevision < 1 || !validServerTime(lockedAt) {
		return nil, false, domain.ErrValidation
	}
	locked, err := r.lockRosterAndReserve(ctx, rosterID, expectedRevision, nil, lockedAt)
	if err != nil {
		switch {
		case errors.Is(err, ErrRosterCAS):
			return nil, false, nil
		case errors.Is(err, pgx.ErrNoRows):
			return nil, false, ErrRosterNotFound
		case errors.Is(err, domain.ErrConflict):
			return nil, false, domain.ErrConflict
		default:
			return nil, false, fmt.Errorf("TournamentPostgres - LockRosterAndReserve: %w", err)
		}
	}
	return rosterRecord(locked), true, nil
}

func (r *RosterPostgres) lockRosterAndReserve(
	ctx context.Context,
	rosterID uuid.UUID,
	expectedRevision int64,
	expectedPlayerIDs []uuid.UUID,
	lockedAt time.Time,
) (sqlc.Roster, error) {
	var locked sqlc.Roster
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		current, err := querier.LockTournamentRosterForUpdate(txCtx, rosterID)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision || current.LockedAt.Valid || current.ExecutionStartedAt.Valid {
			return ErrRosterCAS
		}
		playerIDs, err := querier.ListCheckedInTournamentPlayerIDs(txCtx, rosterID)
		if err != nil {
			return fmt.Errorf("list checked-in players: %w", err)
		}
		if len(playerIDs) == 0 {
			return domain.ErrValidation
		}
		if expectedPlayerIDs != nil && !slices.Equal(playerIDs, expectedPlayerIDs) {
			return ErrRosterCAS
		}
		reserved, err := querier.ReserveCheckedInTournamentParticipants(
			txCtx,
			sqlc.ReserveCheckedInTournamentParticipantsParams{AcquiredAt: tstz(lockedAt), RosterID: rosterID},
		)
		if err != nil {
			return fmt.Errorf("reserve checked-in players: %w", err)
		}
		if len(reserved) != len(playerIDs) {
			return domain.ErrConflict
		}
		locked, err = querier.LockTournamentRosterCAS(txCtx, sqlc.LockTournamentRosterCASParams{
			LockedAt: tstz(lockedAt), ID: rosterID, ExpectedRevision: expectedRevision,
		})
		return err
	})
	return locked, err
}

// LockRosterAndReserveInternal preserves the transaction-scoped workflow used
// by the root roster usecase facade when it supplies an expected player set.
func (r *RosterPostgres) LockRosterAndReserveInternal(
	ctx context.Context,
	rosterID uuid.UUID,
	expectedRevision int64,
	expectedPlayerIDs []uuid.UUID,
	lockedAt time.Time,
) (sqlc.Roster, error) {
	return r.lockRosterAndReserve(ctx, rosterID, expectedRevision, expectedPlayerIDs, lockedAt)
}

func (r *RosterPostgres) UnlockRosterAndRelease(
	ctx context.Context,
	rosterID uuid.UUID,
	expectedRevision int64,
	updatedAt time.Time,
) (*RosterRecord, bool, error) {
	if rosterID == uuid.Nil || expectedRevision < 1 || !validServerTime(updatedAt) {
		return nil, false, domain.ErrValidation
	}
	var unlocked sqlc.Roster
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		current, err := querier.LockTournamentRosterForUpdate(txCtx, rosterID)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision || !current.LockedAt.Valid || current.ExecutionStartedAt.Valid {
			return ErrRosterCAS
		}
		unlocked, err = querier.UnlockTournamentRosterCAS(txCtx, sqlc.UnlockTournamentRosterCASParams{
			UpdatedAt: tstz(updatedAt), ID: rosterID, ExpectedRevision: expectedRevision,
		})
		if err != nil {
			return err
		}
		if _, err = querier.ReleaseTournamentReservations(txCtx, current.TournamentID); err != nil {
			return fmt.Errorf("release reservations: %w", err)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrRosterCAS) {
			return nil, false, nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, ErrRosterNotFound
		}
		return nil, false, fmt.Errorf("TournamentPostgres - UnlockRosterAndRelease: %w", err)
	}
	return rosterRecord(unlocked), true, nil
}

func (r *RosterPostgres) ListReservations(
	ctx context.Context,
	tournamentID uuid.UUID,
) ([]ReservationRecord, error) {
	if tournamentID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	rows, err := r.tx.Querier(ctx).ListTournamentReservations(ctx, tournamentID)
	if err != nil {
		return nil, fmt.Errorf("TournamentPostgres - ListReservations - Querier.ListTournamentReservations: %w", err)
	}
	out := make([]ReservationRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, ReservationRecord{
			PlayerID: row.PlayerID, ReservationID: row.ReservationID, TournamentID: row.TournamentID,
			Revision: row.Revision, AcquiredAt: row.AcquiredAt.Time, UpdatedAt: row.UpdatedAt.Time,
		})
	}
	return out, nil
}

func (r *RosterPostgres) MarkRosterExecutionStarted(
	ctx context.Context,
	rosterID uuid.UUID,
	expectedRevision int64,
	startedAt time.Time,
) (*RosterRecord, bool, error) {
	if rosterID == uuid.Nil || expectedRevision < 1 || !validServerTime(startedAt) {
		return nil, false, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).MarkTournamentRosterExecutionStartedCAS(
		ctx,
		sqlc.MarkTournamentRosterExecutionStartedCASParams{
			StartedAt: tstz(startedAt), ID: rosterID, ExpectedRevision: expectedRevision,
		},
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf(
			"TournamentPostgres - MarkRosterExecutionStarted - Querier.MarkTournamentRosterExecutionStartedCAS: %w", err,
		)
	}
	return rosterRecord(row), true, nil
}

func (r *RosterPostgres) participantRecord(
	ctx context.Context,
	row sqlc.Participant,
) (*ParticipantRecord, error) {
	roster, err := r.tx.Querier(ctx).GetTournamentRoster(ctx, row.RosterID)
	if err != nil {
		return nil, err
	}
	return participantRecord(row, roster.TournamentID), nil
}

func participantRecord(row sqlc.Participant, tournamentID uuid.UUID) *ParticipantRecord {
	return &ParticipantRecord{
		ID: row.ID, RosterID: row.RosterID, TournamentID: tournamentID, PlayerID: row.PlayerID,
		Seed: int(row.Seed), Attendance: domain.AttendanceState(row.Attendance),
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
}

func rosterRecord(row sqlc.Roster) *RosterRecord {
	return &RosterRecord{
		ID: row.ID, TournamentID: row.TournamentID, Revision: row.Revision,
		LockedAt: nullableTime(row.LockedAt), ExecutionStartedAt: nullableTime(row.ExecutionStartedAt),
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
}

func validateParticipantInput(in ParticipantInput) error {
	if in.ID == uuid.Nil || in.RosterID == uuid.Nil || in.PlayerID == uuid.Nil || in.Seed < 1 ||
		!in.Attendance.IsValid() || !validServerTime(in.CreatedAt) {
		return domain.ErrValidation
	}
	return nil
}

func validServerTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}

func nullableTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}

func tstz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func isParticipantConflict(err error) bool {
	return isUniqueViolation(err, "participants_pkey") ||
		isUniqueViolation(err, "participants_roster_player_key") ||
		isUniqueViolation(err, "participants_roster_seed_key")
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}
