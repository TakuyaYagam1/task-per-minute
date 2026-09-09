package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func (r *TournamentPostgres) AddParticipant(
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
			return errRosterCAS
		}
		participants, err := querier.ListTournamentParticipants(txCtx, in.RosterID)
		if err != nil {
			return err
		}
		if len(participants) >= domain.TournamentMaxParticipants {
			return errRosterCAS
		}
		row, err = querier.InsertTournamentParticipant(txCtx, sqlc.InsertTournamentParticipantParams{
			ID:         in.ID,
			PlayerID:   in.PlayerID,
			Seed:       in.Seed,
			Attendance: string(in.Attendance),
			CreatedAt:  tstz(in.CreatedAt),
			RosterID:   in.RosterID,
		})
		return err
	})
	if err != nil {
		if errors.Is(err, errRosterCAS) || errors.Is(err, pgx.ErrNoRows) {
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

func (r *TournamentPostgres) UpdateAttendance(
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
			NextAttendance:     string(next),
			UpdatedAt:          tstz(updatedAt),
			ID:                 participantID,
			ExpectedAttendance: string(expected),
		},
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf(
			"TournamentPostgres - UpdateAttendance - Querier.UpdateTournamentParticipantAttendanceCAS: %w",
			err,
		)
	}
	record, err := r.participantRecord(ctx, row)
	if err != nil {
		return nil, false, fmt.Errorf("TournamentPostgres - UpdateAttendance - map participant: %w", err)
	}
	return record, true, nil
}

func (r *TournamentPostgres) ListParticipants(
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
			ID:           row.ID,
			RosterID:     row.RosterID,
			TournamentID: row.TournamentID,
			PlayerID:     row.PlayerID,
			Seed:         int(row.Seed),
			Attendance:   domain.AttendanceState(row.Attendance),
			CreatedAt:    row.CreatedAt.Time,
			UpdatedAt:    row.UpdatedAt.Time,
		})
	}
	return out, nil
}

func (r *TournamentPostgres) GetRoster(ctx context.Context, id uuid.UUID) (*RosterRecord, error) {
	row, err := r.tx.Querier(ctx).GetTournamentRoster(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRosterNotFound
		}
		return nil, fmt.Errorf("TournamentPostgres - GetRoster - Querier.GetTournamentRoster: %w", err)
	}
	return rosterRecord(row), nil
}

func (r *TournamentPostgres) LockRosterAndReserve(
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
		case errors.Is(err, errRosterCAS):
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

func (r *TournamentPostgres) lockRosterAndReserve(
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
			return errRosterCAS
		}
		playerIDs, err := querier.ListCheckedInTournamentPlayerIDs(txCtx, rosterID)
		if err != nil {
			return fmt.Errorf("list checked-in players: %w", err)
		}
		if len(playerIDs) == 0 {
			return domain.ErrValidation
		}
		if expectedPlayerIDs != nil && !slices.Equal(playerIDs, expectedPlayerIDs) {
			return errRosterCAS
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
			LockedAt:         tstz(lockedAt),
			ID:               rosterID,
			ExpectedRevision: expectedRevision,
		})
		if err != nil {
			return err
		}
		return nil
	})
	return locked, err
}

func (r *TournamentPostgres) UnlockRosterAndRelease(
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
			return errRosterCAS
		}
		unlocked, err = querier.UnlockTournamentRosterCAS(txCtx, sqlc.UnlockTournamentRosterCASParams{
			UpdatedAt:        tstz(updatedAt),
			ID:               rosterID,
			ExpectedRevision: expectedRevision,
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
		if errors.Is(err, errRosterCAS) {
			return nil, false, nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, ErrRosterNotFound
		}
		return nil, false, fmt.Errorf("TournamentPostgres - UnlockRosterAndRelease: %w", err)
	}
	return rosterRecord(unlocked), true, nil
}

func (r *TournamentPostgres) MarkRosterExecutionStarted(
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
			StartedAt:        tstz(startedAt),
			ID:               rosterID,
			ExpectedRevision: expectedRevision,
		},
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf(
			"TournamentPostgres - MarkRosterExecutionStarted - Querier.MarkTournamentRosterExecutionStartedCAS: %w",
			err,
		)
	}
	return rosterRecord(row), true, nil
}

func (r *TournamentPostgres) ListReservations(
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
			PlayerID:      row.PlayerID,
			ReservationID: row.ReservationID,
			TournamentID:  row.TournamentID,
			Revision:      row.Revision,
			AcquiredAt:    row.AcquiredAt.Time,
			UpdatedAt:     row.UpdatedAt.Time,
		})
	}
	return out, nil
}

var errRosterCAS = errors.New("roster compare-and-set failed")
