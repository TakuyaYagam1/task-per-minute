package roster

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	rosterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/roster"
)

func (r *RosterPostgres) GetRosterSnapshot(
	ctx context.Context,
	id uuid.UUID,
) (*rosterusecase.RosterRosterRecord, error) {
	if r == nil || r.tx == nil {
		return nil, domain.ErrValidation
	}
	record, err := r.GetRoster(ctx, id)
	if errors.Is(err, ErrRosterNotFound) {
		return nil, rosterusecase.ErrRosterNotFound
	}
	return rosterUseCaseRecord(record), err
}

func (r *RosterPostgres) LockRosterAndReserveExpected(
	ctx context.Context,
	rosterID uuid.UUID,
	expectedRevision int64,
	expectedPlayerIDs []uuid.UUID,
	lockedAt time.Time,
) (*rosterusecase.RosterRosterRecord, bool, error) {
	if rosterID == uuid.Nil || expectedRevision < 1 || len(expectedPlayerIDs) == 0 || !validServerTime(lockedAt) {
		return nil, false, domain.ErrValidation
	}
	if r == nil || r.tx == nil {
		return nil, false, domain.ErrValidation
	}
	locked, err := r.LockRosterAndReserveInternal(ctx, rosterID, expectedRevision, expectedPlayerIDs, lockedAt)
	if err != nil {
		switch {
		case errors.Is(err, ErrRosterCAS):
			return nil, false, nil
		case errors.Is(err, pgx.ErrNoRows):
			return nil, false, rosterusecase.ErrRosterNotFound
		case errors.Is(err, domain.ErrConflict):
			return nil, false, domain.ErrConflict
		default:
			return nil, false, fmt.Errorf("TournamentPostgres - LockRosterAndReserveExpected: %w", err)
		}
	}
	return rosterUseCaseRecord(rosterRecord(locked)), true, nil
}

func (r *RosterPostgres) UnlockRosterAndReleaseExpected(
	ctx context.Context,
	rosterID uuid.UUID,
	expectedRevision int64,
	updatedAt time.Time,
) (*rosterusecase.RosterRosterRecord, bool, error) {
	if r == nil || r.tx == nil {
		return nil, false, domain.ErrValidation
	}
	record, changed, err := r.UnlockRosterAndRelease(ctx, rosterID, expectedRevision, updatedAt)
	if errors.Is(err, ErrRosterNotFound) {
		return nil, false, rosterusecase.ErrRosterNotFound
	}
	return rosterUseCaseRecord(record), changed, err
}

func rosterUseCaseRecord(record *RosterRecord) *rosterusecase.RosterRosterRecord {
	if record == nil {
		return nil
	}
	return &rosterusecase.RosterRosterRecord{
		ID: record.ID, Revision: record.Revision,
		LockedAt:           utcTimePointer(record.LockedAt),
		ExecutionStartedAt: utcTimePointer(record.ExecutionStartedAt),
	}
}

func utcTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := value.UTC()
	return &normalized
}

var _ rosterusecase.RosterLockRepository = (*RosterPostgres)(nil)
