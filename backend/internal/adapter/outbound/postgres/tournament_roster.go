package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	rosterrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/roster"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// TournamentPostgres keeps the historical roster persistence facade while
// implementation details live in tournament/roster.
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
	record, changed, err := rosterrepo.NewRosterPostgres(r.tx).AddParticipant(ctx, rosterrepo.ParticipantInput(in))
	return participantRecordFromRoster(record), changed, err
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
	if r == nil || r.tx == nil {
		return nil, false, domain.ErrValidation
	}
	record, changed, err := rosterrepo.NewRosterPostgres(r.tx).UpdateAttendance(ctx, participantID, expected, next, updatedAt)
	return participantRecordFromRoster(record), changed, err
}

func (r *TournamentPostgres) ListParticipants(
	ctx context.Context,
	rosterID uuid.UUID,
) ([]ParticipantRecord, error) {
	if r == nil || r.tx == nil {
		return nil, domain.ErrValidation
	}
	records, err := rosterrepo.NewRosterPostgres(r.tx).ListParticipants(ctx, rosterID)
	if err != nil {
		return nil, err
	}
	out := make([]ParticipantRecord, len(records))
	for index := range records {
		out[index] = *participantRecordFromRoster(&records[index])
	}
	return out, nil
}

func (r *TournamentPostgres) GetRoster(ctx context.Context, id uuid.UUID) (*RosterRecord, error) {
	if r == nil || r.tx == nil {
		return nil, domain.ErrValidation
	}
	record, err := rosterrepo.NewRosterPostgres(r.tx).GetRoster(ctx, id)
	if errors.Is(err, rosterrepo.ErrRosterNotFound) {
		return nil, ErrRosterNotFound
	}
	return rosterRecordFromRoster(record), err
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
	if r == nil || r.tx == nil {
		return nil, false, domain.ErrValidation
	}
	record, changed, err := rosterrepo.NewRosterPostgres(r.tx).LockRosterAndReserve(ctx, rosterID, expectedRevision, lockedAt)
	if errors.Is(err, rosterrepo.ErrRosterNotFound) {
		return nil, false, ErrRosterNotFound
	}
	return rosterRecordFromRoster(record), changed, err
}

func (r *TournamentPostgres) lockRosterAndReserve(
	ctx context.Context,
	rosterID uuid.UUID,
	expectedRevision int64,
	expectedPlayerIDs []uuid.UUID,
	lockedAt time.Time,
) (sqlc.Roster, error) {
	if r == nil || r.tx == nil {
		return sqlc.Roster{}, domain.ErrValidation
	}
	locked, err := rosterrepo.NewRosterPostgres(r.tx).LockRosterAndReserveInternal(
		ctx, rosterID, expectedRevision, expectedPlayerIDs, lockedAt,
	)
	if errors.Is(err, rosterrepo.ErrRosterCAS) {
		return locked, errRosterCAS
	}
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
	if r == nil || r.tx == nil {
		return nil, false, domain.ErrValidation
	}
	record, changed, err := rosterrepo.NewRosterPostgres(r.tx).UnlockRosterAndRelease(ctx, rosterID, expectedRevision, updatedAt)
	if errors.Is(err, rosterrepo.ErrRosterNotFound) {
		return nil, false, ErrRosterNotFound
	}
	return rosterRecordFromRoster(record), changed, err
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
	if r == nil || r.tx == nil {
		return nil, false, domain.ErrValidation
	}
	record, changed, err := rosterrepo.NewRosterPostgres(r.tx).MarkRosterExecutionStarted(ctx, rosterID, expectedRevision, startedAt)
	return rosterRecordFromRoster(record), changed, err
}

func (r *TournamentPostgres) ListReservations(
	ctx context.Context,
	tournamentID uuid.UUID,
) ([]ReservationRecord, error) {
	if r == nil || r.tx == nil {
		return nil, domain.ErrValidation
	}
	records, err := rosterrepo.NewRosterPostgres(r.tx).ListReservations(ctx, tournamentID)
	if err != nil {
		return nil, err
	}
	out := make([]ReservationRecord, len(records))
	for index, record := range records {
		out[index] = ReservationRecord{
			PlayerID: record.PlayerID, ReservationID: record.ReservationID, TournamentID: record.TournamentID,
			Revision: record.Revision, AcquiredAt: record.AcquiredAt, UpdatedAt: record.UpdatedAt,
		}
	}
	return out, nil
}

func participantRecordFromRoster(record *rosterrepo.ParticipantRecord) *ParticipantRecord {
	if record == nil {
		return nil
	}
	return &ParticipantRecord{
		ID: record.ID, RosterID: record.RosterID, TournamentID: record.TournamentID, PlayerID: record.PlayerID,
		Seed: record.Seed, Attendance: record.Attendance, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

func rosterRecordFromRoster(record *rosterrepo.RosterRecord) *RosterRecord {
	if record == nil {
		return nil
	}
	return &RosterRecord{
		ID: record.ID, TournamentID: record.TournamentID, Revision: record.Revision,
		LockedAt: record.LockedAt, ExecutionStartedAt: record.ExecutionStartedAt,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

var errRosterCAS = errors.New("roster compare-and-set failed")
