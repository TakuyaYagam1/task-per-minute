package attendance

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
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/roster"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	attendanceusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/attendance"
)

type TournamentAttendancePostgres struct {
	tx *db.TxManager
}

var _ attendanceusecase.AttendanceRepository = (*TournamentAttendancePostgres)(nil)

func NewTournamentAttendancePostgres(tx *db.TxManager) *TournamentAttendancePostgres {
	return &TournamentAttendancePostgres{tx: tx}
}

func (r *TournamentAttendancePostgres) InviteParticipant(
	ctx context.Context,
	in attendanceusecase.ParticipantInput,
) (*attendanceusecase.ParticipantRecord, bool, error) {
	if r == nil || r.tx == nil {
		return nil, false, domain.ErrValidation
	}
	record, changed, err := roster.NewRosterPostgres(r.tx).AddParticipant(ctx, roster.ParticipantInput(in))
	return participantUseCaseRecord(record), changed, err
}

func (r *TournamentAttendancePostgres) ChangeAttendance(
	ctx context.Context,
	participantID uuid.UUID,
	expected domain.AttendanceState,
	next domain.AttendanceState,
	updatedAt time.Time,
) (*attendanceusecase.ParticipantRecord, bool, error) {
	if r == nil || r.tx == nil {
		return nil, false, domain.ErrValidation
	}
	record, changed, err := roster.NewRosterPostgres(r.tx).UpdateAttendance(
		ctx, participantID, expected, next, updatedAt,
	)
	return participantUseCaseRecord(record), changed, err
}

func (r *TournamentAttendancePostgres) ReplaceWithdrawnParticipant(
	ctx context.Context,
	in attendanceusecase.ParticipantReplacementInput,
) (*attendanceusecase.ParticipantRecord, bool, error) {
	if r == nil || r.tx == nil || in.WithdrawnParticipantID == uuid.Nil ||
		in.ReplacementParticipantID == uuid.Nil || in.RosterID == uuid.Nil ||
		in.ReplacementPlayerID == uuid.Nil || in.WithdrawnParticipantID == in.ReplacementParticipantID ||
		!domain.IsValidServerTime(in.ReplacedAt) {
		return nil, false, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).ReplaceWithdrawnTournamentParticipant(
		ctx,
		sqlc.ReplaceWithdrawnTournamentParticipantParams{
			ReplacementParticipantID: in.ReplacementParticipantID,
			ReplacedAt:               tstz(in.ReplacedAt),
			WithdrawnParticipantID:   in.WithdrawnParticipantID,
			RosterID:                 in.RosterID,
			ReplacementPlayerID:      in.ReplacementPlayerID,
		},
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		if isParticipantConflict(err) {
			return nil, false, domain.WrapError(err, domain.ErrConflict)
		}
		return nil, false, fmt.Errorf(
			"TournamentPostgres - ReplaceWithdrawnParticipant - Querier.ReplaceWithdrawnTournamentParticipant: %w",
			err,
		)
	}
	record, err := r.participantRecord(ctx, row)
	if err != nil {
		return nil, false, fmt.Errorf("TournamentPostgres - ReplaceWithdrawnParticipant - map participant: %w", err)
	}
	return record, true, nil
}

func (r *TournamentAttendancePostgres) ListRosterParticipants(
	ctx context.Context,
	rosterID uuid.UUID,
) ([]attendanceusecase.ParticipantRecord, error) {
	if r == nil || r.tx == nil {
		return nil, domain.ErrValidation
	}
	records, err := roster.NewRosterPostgres(r.tx).ListParticipants(ctx, rosterID)
	if err != nil {
		return nil, err
	}
	out := make([]attendanceusecase.ParticipantRecord, 0, len(records))
	for index := range records {
		out = append(out, *participantUseCaseRecord(&records[index]))
	}
	return out, nil
}

func (r *TournamentAttendancePostgres) participantRecord(
	ctx context.Context,
	row sqlc.Participant,
) (*attendanceusecase.ParticipantRecord, error) {
	rosterRecord, err := r.tx.Querier(ctx).GetTournamentRoster(ctx, row.RosterID)
	if err != nil {
		return nil, err
	}
	return &attendanceusecase.ParticipantRecord{
		ID:           row.ID,
		RosterID:     row.RosterID,
		TournamentID: rosterRecord.TournamentID,
		PlayerID:     row.PlayerID,
		Seed:         int(row.Seed),
		Attendance:   domain.AttendanceState(row.Attendance),
		CreatedAt:    row.CreatedAt.Time.UTC(),
		UpdatedAt:    row.UpdatedAt.Time.UTC(),
	}, nil
}

func participantUseCaseRecord(record *roster.ParticipantRecord) *attendanceusecase.ParticipantRecord {
	if record == nil {
		return nil
	}
	return &attendanceusecase.ParticipantRecord{
		ID:           record.ID,
		RosterID:     record.RosterID,
		TournamentID: record.TournamentID,
		PlayerID:     record.PlayerID,
		Seed:         record.Seed,
		Attendance:   record.Attendance,
		CreatedAt:    record.CreatedAt.UTC(),
		UpdatedAt:    record.UpdatedAt.UTC(),
	}
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
