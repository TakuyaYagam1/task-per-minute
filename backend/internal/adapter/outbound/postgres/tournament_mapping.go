package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	attendanceusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/attendance"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
	lifecycleusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/lifecycle"
	rosterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/roster"
)

func validateTournamentTransitionInput(in TournamentTransitionInput) error {
	if in.ID == uuid.Nil || in.ExpectedRevision < 1 || !in.ExpectedState.IsValid() || !in.NextState.IsValid() ||
		!validServerTime(in.UpdatedAt) {
		return domain.ErrValidation
	}
	state := domain.Tournament{State: in.NextState, PausedFromState: in.PausedFromState}
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

func nullableState(value *domain.TournamentState) *string {
	if value == nil {
		return nil
	}
	out := string(*value)
	return &out
}

func tournamentRecord(row sqlc.Tournament) *TournamentRecord {
	out := &TournamentRecord{
		ID:         row.ID,
		Preset:     row.Preset,
		State:      domain.TournamentState(row.State),
		Revision:   row.Revision,
		CreatedAt:  row.CreatedAt.Time,
		UpdatedAt:  row.UpdatedAt.Time,
		StartedAt:  nullableTime(row.StartedAt),
		FinishedAt: nullableTime(row.FinishedAt),
	}
	if row.PausedFromState != nil {
		state := domain.TournamentState(*row.PausedFromState)
		out.PausedFromState = &state
	}
	return out
}

func tournamentUseCaseRecord(
	record *TournamentRecord,
	rosterID uuid.UUID,
	rosterSize int,
) *catalogusecase.CatalogTournamentRecord {
	if record == nil {
		return nil
	}
	return &catalogusecase.CatalogTournamentRecord{
		ID: record.ID, RosterID: rosterID, Preset: domain.TournamentPreset(record.Preset), State: record.State,
		PausedFromState: record.PausedFromState, Revision: record.Revision, RosterSize: rosterSize,
		CreatedAt: record.CreatedAt.UTC(), UpdatedAt: record.UpdatedAt.UTC(),
		StartedAt: utcTimePointer(record.StartedAt), FinishedAt: utcTimePointer(record.FinishedAt),
	}
}

func tournamentSummaryRecord(row sqlc.GetTournamentSummaryRow) (*catalogusecase.CatalogTournamentRecord, error) {
	rosterSize, err := validatedRosterSize(row.RosterSize)
	if err != nil {
		return nil, err
	}
	record := &catalogusecase.CatalogTournamentRecord{
		ID: row.ID, RosterID: row.RosterID, Preset: domain.TournamentPreset(row.Preset),
		State: domain.TournamentState(row.State), Revision: row.Revision, RosterSize: rosterSize,
		CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
		StartedAt: utcTimePointer(nullableTime(row.StartedAt)), FinishedAt: utcTimePointer(nullableTime(row.FinishedAt)),
	}
	if row.PausedFromState != nil {
		state := domain.TournamentState(*row.PausedFromState)
		record.PausedFromState = &state
	}
	return record, nil
}

func tournamentLifecycleSummaryRecord(
	row sqlc.GetTournamentSummaryRow,
) (*lifecycleusecase.LifecycleTournamentRecord, error) {
	record := &lifecycleusecase.LifecycleTournamentRecord{
		ID: row.ID, State: domain.TournamentState(row.State), Revision: row.Revision,
		UpdatedAt: row.UpdatedAt.Time.UTC(),
		StartedAt: utcTimePointer(nullableTime(row.StartedAt)), FinishedAt: utcTimePointer(nullableTime(row.FinishedAt)),
	}
	if row.PausedFromState != nil {
		state := domain.TournamentState(*row.PausedFromState)
		record.PausedFromState = &state
	}
	return record, nil
}

func tournamentListSummaryRecord(row sqlc.ListTournamentSummariesRow) (*catalogusecase.CatalogTournamentRecord, error) {
	rosterSize, err := validatedRosterSize(row.RosterSize)
	if err != nil {
		return nil, err
	}
	record := &catalogusecase.CatalogTournamentRecord{
		ID: row.ID, RosterID: row.RosterID, Preset: domain.TournamentPreset(row.Preset),
		State: domain.TournamentState(row.State), Revision: row.Revision, RosterSize: rosterSize,
		CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
		StartedAt: utcTimePointer(nullableTime(row.StartedAt)), FinishedAt: utcTimePointer(nullableTime(row.FinishedAt)),
	}
	if row.PausedFromState != nil {
		state := domain.TournamentState(*row.PausedFromState)
		record.PausedFromState = &state
	}
	return record, nil
}

func validatedRosterSize(value int64) (int, error) {
	if value < 0 || value > int64(domain.TournamentMaxParticipants) {
		return 0, domain.ErrInternal
	}
	return int(value), nil
}

func rosterRecord(row sqlc.Roster) *RosterRecord {
	return &RosterRecord{
		ID:                 row.ID,
		TournamentID:       row.TournamentID,
		Revision:           row.Revision,
		LockedAt:           nullableTime(row.LockedAt),
		ExecutionStartedAt: nullableTime(row.ExecutionStartedAt),
		CreatedAt:          row.CreatedAt.Time,
		UpdatedAt:          row.UpdatedAt.Time,
	}
}

func rosterUseCaseRecord(record *RosterRecord) *rosterusecase.RosterRosterRecord {
	if record == nil {
		return nil
	}
	return &rosterusecase.RosterRosterRecord{
		ID: record.ID, Revision: record.Revision,
		LockedAt: utcTimePointer(record.LockedAt), ExecutionStartedAt: utcTimePointer(record.ExecutionStartedAt),
	}
}

func catalogRosterUseCaseRecord(record *RosterRecord) *catalogusecase.CatalogRosterRecord {
	if record == nil {
		return nil
	}
	return &catalogusecase.CatalogRosterRecord{
		ID: record.ID, TournamentID: record.TournamentID,
	}
}

func participantRecord(row sqlc.Participant, tournamentID uuid.UUID) *ParticipantRecord {
	return &ParticipantRecord{
		ID:           row.ID,
		RosterID:     row.RosterID,
		TournamentID: tournamentID,
		PlayerID:     row.PlayerID,
		Seed:         int(row.Seed),
		Attendance:   domain.AttendanceState(row.Attendance),
		CreatedAt:    row.CreatedAt.Time,
		UpdatedAt:    row.UpdatedAt.Time,
	}
}

func participantUseCaseRecord(record *ParticipantRecord) *attendanceusecase.ParticipantRecord {
	if record == nil {
		return nil
	}
	return &attendanceusecase.ParticipantRecord{
		ID: record.ID, RosterID: record.RosterID, TournamentID: record.TournamentID,
		PlayerID: record.PlayerID, Seed: record.Seed, Attendance: record.Attendance,
		CreatedAt: record.CreatedAt.UTC(), UpdatedAt: record.UpdatedAt.UTC(),
	}
}

func utcTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := value.UTC()
	return &normalized
}

func (r *TournamentPostgres) participantRecord(
	ctx context.Context,
	row sqlc.Participant,
) (*ParticipantRecord, error) {
	roster, err := r.tx.Querier(ctx).GetTournamentRoster(ctx, row.RosterID)
	if err != nil {
		return nil, err
	}
	return participantRecord(row, roster.TournamentID), nil
}

func isParticipantConflict(err error) bool {
	return isUniqueViolation(err, "participants_pkey") ||
		isUniqueViolation(err, "participants_roster_player_key") ||
		isUniqueViolation(err, "participants_roster_seed_key")
}
