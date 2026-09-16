package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	attendancerepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/attendance"
	lifecyclerepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/lifecycle"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	attendanceusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/attendance"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
	lifecycleusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/lifecycle"
	rosterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/roster"
)

func (r *TournamentCatalogPostgres) CreateTournamentDraft(
	ctx context.Context,
	command catalogusecase.TournamentCreateCommand,
	createdAt time.Time,
) (*catalogusecase.CatalogTournamentRecord, *catalogusecase.CatalogRosterRecord, error) {
	if r == nil || r.tournaments == nil || r.tournaments.tx == nil {
		return nil, nil, domain.ErrValidation
	}
	tournament, roster, err := r.tournaments.Create(ctx, TournamentCreateInput{
		ID: command.TournamentID, RosterID: command.RosterID, Name: command.Name, PublicID: command.PublicID,
		PlannedRosterSize: command.PlannedRosterSize, ContentRevision: command.ContentRevision, CreatedAt: createdAt,
	})
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
	if r == nil || r.tournaments == nil {
		return nil, domain.ErrValidation
	}
	return lifecyclerepo.NewTournamentLifecyclePostgres(r.tournaments.tx).GetTournament(ctx, id)
}

func (r *TournamentLifecyclePostgres) TransitionTournament(
	ctx context.Context,
	in lifecycleusecase.TournamentLifecycleTransitionInput,
) (*lifecycleusecase.LifecycleTournamentRecord, bool, error) {
	if r == nil || r.tournaments == nil {
		return nil, false, domain.ErrValidation
	}
	return lifecyclerepo.NewTournamentLifecyclePostgres(r.tournaments.tx).TransitionTournament(ctx, in)
}

func (r *TournamentAttendancePostgres) InviteParticipant(
	ctx context.Context,
	in attendanceusecase.ParticipantInput,
) (*attendanceusecase.ParticipantRecord, bool, error) {
	if r == nil || r.tournaments == nil {
		return nil, false, domain.ErrValidation
	}
	return attendancerepo.NewTournamentAttendancePostgres(r.tournaments.tx).InviteParticipant(ctx, in)
}

func (r *TournamentAttendancePostgres) ChangeAttendance(
	ctx context.Context,
	participantID uuid.UUID,
	expected domain.AttendanceState,
	next domain.AttendanceState,
	updatedAt time.Time,
) (*attendanceusecase.ParticipantRecord, bool, error) {
	if r == nil || r.tournaments == nil {
		return nil, false, domain.ErrValidation
	}
	return attendancerepo.NewTournamentAttendancePostgres(r.tournaments.tx).ChangeAttendance(
		ctx, participantID, expected, next, updatedAt,
	)
}

func (r *TournamentAttendancePostgres) ReplaceWithdrawnParticipant(
	ctx context.Context,
	in attendanceusecase.ParticipantReplacementInput,
) (*attendanceusecase.ParticipantRecord, bool, error) {
	if r == nil || r.tournaments == nil {
		return nil, false, domain.ErrValidation
	}
	return attendancerepo.NewTournamentAttendancePostgres(r.tournaments.tx).ReplaceWithdrawnParticipant(ctx, in)
}

func (r *TournamentAttendancePostgres) ListRosterParticipants(
	ctx context.Context,
	rosterID uuid.UUID,
) ([]attendanceusecase.ParticipantRecord, error) {
	if r == nil || r.tournaments == nil {
		return nil, domain.ErrValidation
	}
	return attendancerepo.NewTournamentAttendancePostgres(r.tournaments.tx).ListRosterParticipants(ctx, rosterID)
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
