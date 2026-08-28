package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const arenaTournamentActiveConstraint = "arena_tournaments_single_active_idx"

var (
	ErrArenaTournamentNotFound = errors.New("arena tournament repository: tournament not found")
	ErrArenaRosterNotFound     = errors.New("arena tournament repository: roster not found")
)

type ArenaTournamentPostgres struct {
	tx *TxManager
}

type ArenaTournamentRecord struct {
	ID              uuid.UUID
	Preset          string
	State           domain.ArenaTournamentState
	PausedFromState *domain.ArenaTournamentState
	Revision        int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
	StartedAt       *time.Time
	FinishedAt      *time.Time
}

type ArenaRosterRecord struct {
	ID                 uuid.UUID
	TournamentID       uuid.UUID
	Revision           int64
	LockedAt           *time.Time
	ExecutionStartedAt *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type ArenaParticipantRecord struct {
	ID           uuid.UUID
	RosterID     uuid.UUID
	TournamentID uuid.UUID
	PlayerID     uuid.UUID
	Seed         int
	Attendance   domain.ArenaAttendanceState
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type ArenaReservationRecord struct {
	PlayerID      uuid.UUID
	ReservationID uuid.UUID
	TournamentID  uuid.UUID
	Revision      int64
	AcquiredAt    time.Time
	UpdatedAt     time.Time
}

type ArenaTournamentTransitionInput struct {
	ID               uuid.UUID
	ExpectedRevision int64
	ExpectedState    domain.ArenaTournamentState
	NextState        domain.ArenaTournamentState
	PausedFromState  *domain.ArenaTournamentState
	UpdatedAt        time.Time
	StartedAt        *time.Time
	FinishedAt       *time.Time
}

type ArenaParticipantInput struct {
	ID         uuid.UUID
	RosterID   uuid.UUID
	PlayerID   uuid.UUID
	Seed       int32
	Attendance domain.ArenaAttendanceState
	CreatedAt  time.Time
}

func NewArenaTournamentPostgres(tx *TxManager) *ArenaTournamentPostgres {
	return &ArenaTournamentPostgres{tx: tx}
}

func (r *ArenaTournamentPostgres) Create(
	ctx context.Context,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	createdAt time.Time,
) (*ArenaTournamentRecord, *ArenaRosterRecord, error) {
	if r == nil || r.tx == nil || tournamentID == uuid.Nil || rosterID == uuid.Nil || !validServerTime(createdAt) {
		return nil, nil, domain.ErrValidation
	}

	var tournament sqlc.ArenaTournament
	var roster sqlc.ArenaRoster
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		tournament, err = r.tx.Querier(txCtx).CreateArenaTournament(txCtx, sqlc.CreateArenaTournamentParams{
			ID:        tournamentID,
			CreatedAt: tstz(createdAt),
		})
		if err != nil {
			return fmt.Errorf("ArenaTournamentPostgres - Create - Querier.CreateArenaTournament: %w", err)
		}
		roster, err = r.tx.Querier(txCtx).CreateArenaRoster(txCtx, sqlc.CreateArenaRosterParams{
			ID:           rosterID,
			TournamentID: tournamentID,
			CreatedAt:    tstz(createdAt),
		})
		if err != nil {
			return fmt.Errorf("ArenaTournamentPostgres - Create - Querier.CreateArenaRoster: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return arenaTournamentRecord(tournament), arenaRosterRecord(roster), nil
}

func (r *ArenaTournamentPostgres) Get(ctx context.Context, id uuid.UUID) (*ArenaTournamentRecord, error) {
	row, err := r.tx.Querier(ctx).GetArenaTournament(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrArenaTournamentNotFound
		}
		return nil, fmt.Errorf("ArenaTournamentPostgres - Get - Querier.GetArenaTournament: %w", err)
	}
	return arenaTournamentRecord(row), nil
}

func (r *ArenaTournamentPostgres) Active(ctx context.Context) (*ArenaTournamentRecord, error) {
	row, err := r.tx.Querier(ctx).GetActiveArenaTournament(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("ArenaTournamentPostgres - Active - Querier.GetActiveArenaTournament: %w", err)
	}
	return arenaTournamentRecord(row), nil
}

func (r *ArenaTournamentPostgres) List(ctx context.Context) ([]ArenaTournamentRecord, error) {
	rows, err := r.tx.Querier(ctx).ListArenaTournaments(ctx)
	if err != nil {
		return nil, fmt.Errorf("ArenaTournamentPostgres - List - Querier.ListArenaTournaments: %w", err)
	}
	out := make([]ArenaTournamentRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, *arenaTournamentRecord(row))
	}
	return out, nil
}

func (r *ArenaTournamentPostgres) Transition(
	ctx context.Context,
	in ArenaTournamentTransitionInput,
) (*ArenaTournamentRecord, bool, error) {
	if err := validateTournamentTransitionInput(in); err != nil {
		return nil, false, err
	}

	var updated sqlc.ArenaTournament
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		updated, err = r.tx.Querier(txCtx).UpdateArenaTournamentCAS(txCtx, sqlc.UpdateArenaTournamentCASParams{
			NextState:        string(in.NextState),
			PausedFromState:  nullableArenaState(in.PausedFromState),
			UpdatedAt:        tstz(in.UpdatedAt),
			StartedAt:        nullableTSTZ(in.StartedAt),
			FinishedAt:       nullableTSTZ(in.FinishedAt),
			ID:               in.ID,
			ExpectedRevision: in.ExpectedRevision,
			ExpectedState:    string(in.ExpectedState),
		})
		if err != nil {
			return err
		}
		if !in.NextState.IsTerminal() {
			return nil
		}
		if _, err = r.tx.Querier(txCtx).ReleaseArenaReservations(txCtx, in.ID); err != nil {
			return fmt.Errorf("ArenaTournamentPostgres - Transition - Querier.ReleaseArenaReservations: %w", err)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		if isUniqueViolation(err, arenaTournamentActiveConstraint) {
			return nil, false, domain.WrapError(err, domain.ErrConflict)
		}
		return nil, false, fmt.Errorf("ArenaTournamentPostgres - Transition - Querier.UpdateArenaTournamentCAS: %w", err)
	}
	return arenaTournamentRecord(updated), true, nil
}

func (r *ArenaTournamentPostgres) AddParticipant(
	ctx context.Context,
	in ArenaParticipantInput,
) (*ArenaParticipantRecord, bool, error) {
	if err := validateArenaParticipantInput(in); err != nil {
		return nil, false, err
	}
	row, err := r.tx.Querier(ctx).InsertArenaParticipant(ctx, sqlc.InsertArenaParticipantParams{
		ID:         in.ID,
		PlayerID:   in.PlayerID,
		Seed:       in.Seed,
		Attendance: string(in.Attendance),
		CreatedAt:  tstz(in.CreatedAt),
		RosterID:   in.RosterID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		if isArenaParticipantConflict(err) {
			return nil, false, domain.WrapError(err, domain.ErrConflict)
		}
		return nil, false, fmt.Errorf("ArenaTournamentPostgres - AddParticipant - Querier.InsertArenaParticipant: %w", err)
	}
	record, err := r.participantRecord(ctx, row)
	if err != nil {
		return nil, false, fmt.Errorf("ArenaTournamentPostgres - AddParticipant - map participant: %w", err)
	}
	return record, true, nil
}

func (r *ArenaTournamentPostgres) UpdateAttendance(
	ctx context.Context,
	participantID uuid.UUID,
	expected domain.ArenaAttendanceState,
	next domain.ArenaAttendanceState,
	updatedAt time.Time,
) (*ArenaParticipantRecord, bool, error) {
	if participantID == uuid.Nil || !expected.IsValid() || !expected.CanTransitionTo(next) || !validServerTime(updatedAt) {
		return nil, false, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).UpdateArenaParticipantAttendanceCAS(
		ctx,
		sqlc.UpdateArenaParticipantAttendanceCASParams{
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
			"ArenaTournamentPostgres - UpdateAttendance - Querier.UpdateArenaParticipantAttendanceCAS: %w",
			err,
		)
	}
	record, err := r.participantRecord(ctx, row)
	if err != nil {
		return nil, false, fmt.Errorf("ArenaTournamentPostgres - UpdateAttendance - map participant: %w", err)
	}
	return record, true, nil
}

func (r *ArenaTournamentPostgres) ListParticipants(
	ctx context.Context,
	rosterID uuid.UUID,
) ([]ArenaParticipantRecord, error) {
	rows, err := r.tx.Querier(ctx).ListArenaParticipants(ctx, rosterID)
	if err != nil {
		return nil, fmt.Errorf("ArenaTournamentPostgres - ListParticipants - Querier.ListArenaParticipants: %w", err)
	}
	out := make([]ArenaParticipantRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, ArenaParticipantRecord{
			ID:           row.ID,
			RosterID:     row.RosterID,
			TournamentID: row.TournamentID,
			PlayerID:     row.PlayerID,
			Seed:         int(row.Seed),
			Attendance:   domain.ArenaAttendanceState(row.Attendance),
			CreatedAt:    row.CreatedAt.Time,
			UpdatedAt:    row.UpdatedAt.Time,
		})
	}
	return out, nil
}

func (r *ArenaTournamentPostgres) GetRoster(ctx context.Context, id uuid.UUID) (*ArenaRosterRecord, error) {
	row, err := r.tx.Querier(ctx).GetArenaRoster(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrArenaRosterNotFound
		}
		return nil, fmt.Errorf("ArenaTournamentPostgres - GetRoster - Querier.GetArenaRoster: %w", err)
	}
	return arenaRosterRecord(row), nil
}

func (r *ArenaTournamentPostgres) LockRosterAndReserve(
	ctx context.Context,
	rosterID uuid.UUID,
	expectedRevision int64,
	lockedAt time.Time,
) (*ArenaRosterRecord, bool, error) {
	if rosterID == uuid.Nil || expectedRevision < 1 || !validServerTime(lockedAt) {
		return nil, false, domain.ErrValidation
	}

	locked, err := r.lockRosterAndReserve(ctx, rosterID, expectedRevision, lockedAt)
	if err != nil {
		switch {
		case errors.Is(err, errArenaRosterCAS):
			return nil, false, nil
		case errors.Is(err, pgx.ErrNoRows):
			return nil, false, ErrArenaRosterNotFound
		case errors.Is(err, domain.ErrConflict):
			return nil, false, domain.ErrConflict
		default:
			return nil, false, fmt.Errorf("ArenaTournamentPostgres - LockRosterAndReserve: %w", err)
		}
	}
	return arenaRosterRecord(locked), true, nil
}

func (r *ArenaTournamentPostgres) lockRosterAndReserve(
	ctx context.Context,
	rosterID uuid.UUID,
	expectedRevision int64,
	lockedAt time.Time,
) (sqlc.ArenaRoster, error) {
	var locked sqlc.ArenaRoster
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		current, err := querier.LockArenaRosterForUpdate(txCtx, rosterID)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision || current.LockedAt.Valid || current.ExecutionStartedAt.Valid {
			return errArenaRosterCAS
		}
		playerIDs, err := querier.ListCheckedInArenaPlayerIDs(txCtx, rosterID)
		if err != nil {
			return fmt.Errorf("list checked-in players: %w", err)
		}
		if len(playerIDs) == 0 {
			return domain.ErrValidation
		}
		reserved, err := querier.ReserveCheckedInArenaParticipants(
			txCtx,
			sqlc.ReserveCheckedInArenaParticipantsParams{AcquiredAt: tstz(lockedAt), RosterID: rosterID},
		)
		if err != nil {
			return fmt.Errorf("reserve checked-in players: %w", err)
		}
		if len(reserved) != len(playerIDs) {
			return domain.ErrConflict
		}
		locked, err = querier.LockArenaRosterCAS(txCtx, sqlc.LockArenaRosterCASParams{
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

func (r *ArenaTournamentPostgres) UnlockRosterAndRelease(
	ctx context.Context,
	rosterID uuid.UUID,
	expectedRevision int64,
	updatedAt time.Time,
) (*ArenaRosterRecord, bool, error) {
	if rosterID == uuid.Nil || expectedRevision < 1 || !validServerTime(updatedAt) {
		return nil, false, domain.ErrValidation
	}
	var unlocked sqlc.ArenaRoster
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		current, err := querier.LockArenaRosterForUpdate(txCtx, rosterID)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision || !current.LockedAt.Valid || current.ExecutionStartedAt.Valid {
			return errArenaRosterCAS
		}
		unlocked, err = querier.UnlockArenaRosterCAS(txCtx, sqlc.UnlockArenaRosterCASParams{
			UpdatedAt:        tstz(updatedAt),
			ID:               rosterID,
			ExpectedRevision: expectedRevision,
		})
		if err != nil {
			return err
		}
		if _, err = querier.ReleaseArenaReservations(txCtx, current.TournamentID); err != nil {
			return fmt.Errorf("release reservations: %w", err)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errArenaRosterCAS) {
			return nil, false, nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, ErrArenaRosterNotFound
		}
		return nil, false, fmt.Errorf("ArenaTournamentPostgres - UnlockRosterAndRelease: %w", err)
	}
	return arenaRosterRecord(unlocked), true, nil
}

func (r *ArenaTournamentPostgres) MarkRosterExecutionStarted(
	ctx context.Context,
	rosterID uuid.UUID,
	expectedRevision int64,
	startedAt time.Time,
) (*ArenaRosterRecord, bool, error) {
	if rosterID == uuid.Nil || expectedRevision < 1 || !validServerTime(startedAt) {
		return nil, false, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).MarkArenaRosterExecutionStartedCAS(
		ctx,
		sqlc.MarkArenaRosterExecutionStartedCASParams{
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
			"ArenaTournamentPostgres - MarkRosterExecutionStarted - Querier.MarkArenaRosterExecutionStartedCAS: %w",
			err,
		)
	}
	return arenaRosterRecord(row), true, nil
}

func (r *ArenaTournamentPostgres) ListReservations(
	ctx context.Context,
	tournamentID uuid.UUID,
) ([]ArenaReservationRecord, error) {
	if tournamentID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	rows, err := r.tx.Querier(ctx).ListArenaReservations(
		ctx,
		uuid.NullUUID{UUID: tournamentID, Valid: true},
	)
	if err != nil {
		return nil, fmt.Errorf("ArenaTournamentPostgres - ListReservations - Querier.ListArenaReservations: %w", err)
	}
	out := make([]ArenaReservationRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, ArenaReservationRecord{
			PlayerID:      row.PlayerID,
			ReservationID: row.ReservationID,
			TournamentID:  row.ArenaTournamentID.UUID,
			Revision:      row.Revision,
			AcquiredAt:    row.AcquiredAt.Time,
			UpdatedAt:     row.UpdatedAt.Time,
		})
	}
	return out, nil
}

var errArenaRosterCAS = errors.New("arena roster compare-and-set failed")

func validateTournamentTransitionInput(in ArenaTournamentTransitionInput) error {
	if in.ID == uuid.Nil || in.ExpectedRevision < 1 || !in.ExpectedState.IsValid() || !in.NextState.IsValid() ||
		!validServerTime(in.UpdatedAt) {
		return domain.ErrValidation
	}
	state := domain.ArenaTournament{State: in.NextState, PausedFromState: in.PausedFromState}
	if err := state.Validate(); err != nil {
		return domain.ErrValidation
	}
	if in.StartedAt != nil && !validServerTime(*in.StartedAt) {
		return domain.ErrValidation
	}
	if in.FinishedAt != nil && !validServerTime(*in.FinishedAt) {
		return domain.ErrValidation
	}
	return nil
}

func validateArenaParticipantInput(in ArenaParticipantInput) error {
	if in.ID == uuid.Nil || in.RosterID == uuid.Nil || in.PlayerID == uuid.Nil || in.Seed < 1 ||
		!in.Attendance.IsValid() || !validServerTime(in.CreatedAt) {
		return domain.ErrValidation
	}
	return nil
}

func validServerTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}

func nullableArenaState(value *domain.ArenaTournamentState) *string {
	if value == nil {
		return nil
	}
	out := string(*value)
	return &out
}

func arenaTournamentRecord(row sqlc.ArenaTournament) *ArenaTournamentRecord {
	out := &ArenaTournamentRecord{
		ID:         row.ID,
		Preset:     row.Preset,
		State:      domain.ArenaTournamentState(row.State),
		Revision:   row.Revision,
		CreatedAt:  row.CreatedAt.Time,
		UpdatedAt:  row.UpdatedAt.Time,
		StartedAt:  nullableTime(row.StartedAt),
		FinishedAt: nullableTime(row.FinishedAt),
	}
	if row.PausedFromState != nil {
		state := domain.ArenaTournamentState(*row.PausedFromState)
		out.PausedFromState = &state
	}
	return out
}

func arenaRosterRecord(row sqlc.ArenaRoster) *ArenaRosterRecord {
	return &ArenaRosterRecord{
		ID:                 row.ID,
		TournamentID:       row.TournamentID,
		Revision:           row.Revision,
		LockedAt:           nullableTime(row.LockedAt),
		ExecutionStartedAt: nullableTime(row.ExecutionStartedAt),
		CreatedAt:          row.CreatedAt.Time,
		UpdatedAt:          row.UpdatedAt.Time,
	}
}

func arenaParticipantRecord(row sqlc.ArenaParticipant, tournamentID uuid.UUID) *ArenaParticipantRecord {
	return &ArenaParticipantRecord{
		ID:           row.ID,
		RosterID:     row.RosterID,
		TournamentID: tournamentID,
		PlayerID:     row.PlayerID,
		Seed:         int(row.Seed),
		Attendance:   domain.ArenaAttendanceState(row.Attendance),
		CreatedAt:    row.CreatedAt.Time,
		UpdatedAt:    row.UpdatedAt.Time,
	}
}

func (r *ArenaTournamentPostgres) participantRecord(
	ctx context.Context,
	row sqlc.ArenaParticipant,
) (*ArenaParticipantRecord, error) {
	roster, err := r.tx.Querier(ctx).GetArenaRoster(ctx, row.RosterID)
	if err != nil {
		return nil, err
	}
	return arenaParticipantRecord(row, roster.TournamentID), nil
}

func isArenaParticipantConflict(err error) bool {
	return isUniqueViolation(err, "arena_participants_pkey") ||
		isUniqueViolation(err, "arena_participants_roster_player_key") ||
		isUniqueViolation(err, "arena_participants_roster_seed_key")
}
