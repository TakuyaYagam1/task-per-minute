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
	attendanceusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/attendance"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
	lifecycleusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/lifecycle"
	rosterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/roster"
)

func (r *TournamentCatalogPostgres) CreateTournamentDraft(
	ctx context.Context,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	createdAt time.Time,
) (*catalogusecase.CatalogTournamentRecord, *catalogusecase.CatalogRosterRecord, error) {
	if r == nil || r.tournaments == nil || r.tournaments.tx == nil {
		return nil, nil, domain.ErrValidation
	}
	tournament, roster, err := r.tournaments.Create(ctx, tournamentID, rosterID, createdAt)
	if err != nil {
		return nil, nil, err
	}
	return tournamentUseCaseRecord(tournament, roster.ID, 0), catalogRosterUseCaseRecord(roster), nil
}

func (r *TournamentCatalogPostgres) GetTournament(
	ctx context.Context,
	id uuid.UUID,
) (*catalogusecase.CatalogTournamentRecord, error) {
	if r == nil || r.tournaments == nil || r.tournaments.tx == nil || id == uuid.Nil {
		return nil, domain.ErrValidation
	}
	row, err := r.tournaments.tx.Querier(ctx).GetTournamentSummary(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, catalogusecase.ErrTournamentNotFound
		}
		return nil, fmt.Errorf("TournamentPostgres - GetTournament - Querier.GetTournamentSummary: %w", err)
	}
	return tournamentSummaryRecord(row)
}

func (r *TournamentCatalogPostgres) ListTournaments(ctx context.Context) ([]catalogusecase.CatalogTournamentRecord, error) {
	if r == nil || r.tournaments == nil || r.tournaments.tx == nil {
		return nil, domain.ErrValidation
	}
	rows, err := r.tournaments.tx.Querier(ctx).ListTournamentSummaries(ctx)
	if err != nil {
		return nil, fmt.Errorf(
			"TournamentPostgres - ListTournaments - Querier.ListTournamentSummaries: %w",
			err,
		)
	}
	out := make([]catalogusecase.CatalogTournamentRecord, 0, len(rows))
	for _, row := range rows {
		record, mapErr := tournamentListSummaryRecord(row)
		if mapErr != nil {
			return nil, mapErr
		}
		out = append(out, *record)
	}
	return out, nil
}

func (r *TournamentLifecyclePostgres) GetTournament(
	ctx context.Context,
	id uuid.UUID,
) (*lifecycleusecase.LifecycleTournamentRecord, error) {
	if r == nil || r.tournaments == nil || r.tournaments.tx == nil || id == uuid.Nil {
		return nil, domain.ErrValidation
	}
	row, err := r.tournaments.tx.Querier(ctx).GetTournamentSummary(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, lifecycleusecase.ErrTournamentNotFound
		}
		return nil, fmt.Errorf("TournamentLifecyclePostgres - GetTournament - Querier.GetTournamentSummary: %w", err)
	}
	return tournamentLifecycleSummaryRecord(row)
}

func (r *TournamentLifecyclePostgres) TransitionTournament(
	ctx context.Context,
	in lifecycleusecase.TournamentLifecycleTransitionInput,
) (*lifecycleusecase.LifecycleTournamentRecord, bool, error) {
	if r == nil || r.tournaments == nil || r.tournaments.tx == nil {
		return nil, false, domain.ErrValidation
	}
	transition := TournamentTransitionInput{
		ID: in.TournamentID, ExpectedRevision: in.ExpectedRevision, ExpectedState: in.ExpectedState,
		NextState: in.NextState, PausedFromState: in.PausedFromState, UpdatedAt: in.TransitionedAt,
		StartedAt: in.StartedAt, FinishedAt: in.FinishedAt,
	}
	if err := validateTournamentTransitionInput(transition); err != nil {
		return nil, false, err
	}
	current := domain.Tournament{State: in.ExpectedState}
	if !current.CanTransitionTo(in.NextState) {
		return nil, false, domain.ErrValidation
	}

	var record *lifecycleusecase.LifecycleTournamentRecord
	changed := false
	err := r.tournaments.tx.Do(ctx, func(txCtx context.Context) error {
		_, transitionChanged, err := r.tournaments.Transition(txCtx, transition)
		if err != nil || !transitionChanged {
			return err
		}
		changed = true
		row, err := r.tournaments.tx.Querier(txCtx).GetTournamentSummary(txCtx, in.TournamentID)
		if err != nil {
			return fmt.Errorf("load transitioned tournament: %w", err)
		}
		record, err = tournamentLifecycleSummaryRecord(row)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	return record, changed, nil
}

func (r *TournamentAttendancePostgres) InviteParticipant(
	ctx context.Context,
	in attendanceusecase.ParticipantInput,
) (*attendanceusecase.ParticipantRecord, bool, error) {
	if r == nil || r.tournaments == nil || r.tournaments.tx == nil {
		return nil, false, domain.ErrValidation
	}
	record, changed, err := r.tournaments.AddParticipant(ctx, ParticipantInput(in))
	return participantUseCaseRecord(record), changed, err
}

func (r *TournamentAttendancePostgres) ChangeAttendance(
	ctx context.Context,
	participantID uuid.UUID,
	expected domain.AttendanceState,
	next domain.AttendanceState,
	updatedAt time.Time,
) (*attendanceusecase.ParticipantRecord, bool, error) {
	if r == nil || r.tournaments == nil || r.tournaments.tx == nil {
		return nil, false, domain.ErrValidation
	}
	record, changed, err := r.tournaments.UpdateAttendance(ctx, participantID, expected, next, updatedAt)
	return participantUseCaseRecord(record), changed, err
}

func (r *TournamentAttendancePostgres) ReplaceWithdrawnParticipant(
	ctx context.Context,
	in attendanceusecase.ParticipantReplacementInput,
) (*attendanceusecase.ParticipantRecord, bool, error) {
	if r == nil || r.tournaments == nil || r.tournaments.tx == nil || in.WithdrawnParticipantID == uuid.Nil ||
		in.ReplacementParticipantID == uuid.Nil || in.RosterID == uuid.Nil ||
		in.ReplacementPlayerID == uuid.Nil || in.WithdrawnParticipantID == in.ReplacementParticipantID ||
		!validServerTime(in.ReplacedAt) {
		return nil, false, domain.ErrValidation
	}
	row, err := r.tournaments.tx.Querier(ctx).ReplaceWithdrawnTournamentParticipant(
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
	record, err := r.tournaments.participantRecord(ctx, row)
	if err != nil {
		return nil, false, fmt.Errorf("TournamentPostgres - ReplaceWithdrawnParticipant - map participant: %w", err)
	}
	return participantUseCaseRecord(record), true, nil
}

func (r *TournamentAttendancePostgres) ListRosterParticipants(
	ctx context.Context,
	rosterID uuid.UUID,
) ([]attendanceusecase.ParticipantRecord, error) {
	if r == nil || r.tournaments == nil || r.tournaments.tx == nil {
		return nil, domain.ErrValidation
	}
	records, err := r.tournaments.ListParticipants(ctx, rosterID)
	if err != nil {
		return nil, err
	}
	out := make([]attendanceusecase.ParticipantRecord, 0, len(records))
	for index := range records {
		out = append(out, *participantUseCaseRecord(&records[index]))
	}
	return out, nil
}

func (r *TournamentRosterPostgres) GetRosterSnapshot(
	ctx context.Context,
	id uuid.UUID,
) (*rosterusecase.RosterRosterRecord, error) {
	if r == nil || r.tournaments == nil || r.tournaments.tx == nil {
		return nil, domain.ErrValidation
	}
	record, err := r.tournaments.GetRoster(ctx, id)
	if errors.Is(err, ErrRosterNotFound) {
		return nil, rosterusecase.ErrRosterNotFound
	}
	return rosterUseCaseRecord(record), err
}

func (r *TournamentRosterPostgres) LockRosterAndReserveExpected(
	ctx context.Context,
	rosterID uuid.UUID,
	expectedRevision int64,
	expectedPlayerIDs []uuid.UUID,
	lockedAt time.Time,
) (*rosterusecase.RosterRosterRecord, bool, error) {
	if rosterID == uuid.Nil || expectedRevision < 1 || len(expectedPlayerIDs) == 0 || !validServerTime(lockedAt) {
		return nil, false, domain.ErrValidation
	}
	if r == nil || r.tournaments == nil || r.tournaments.tx == nil {
		return nil, false, domain.ErrValidation
	}
	locked, err := r.tournaments.lockRosterAndReserve(ctx, rosterID, expectedRevision, expectedPlayerIDs, lockedAt)
	if err != nil {
		switch {
		case errors.Is(err, errRosterCAS):
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

func (r *TournamentRosterPostgres) UnlockRosterAndReleaseExpected(
	ctx context.Context,
	rosterID uuid.UUID,
	expectedRevision int64,
	updatedAt time.Time,
) (*rosterusecase.RosterRosterRecord, bool, error) {
	if r == nil || r.tournaments == nil || r.tournaments.tx == nil {
		return nil, false, domain.ErrValidation
	}
	record, changed, err := r.tournaments.UnlockRosterAndRelease(ctx, rosterID, expectedRevision, updatedAt)
	if errors.Is(err, ErrRosterNotFound) {
		return nil, false, rosterusecase.ErrRosterNotFound
	}
	return rosterUseCaseRecord(record), changed, err
}
