package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"

	attendancerepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/attendance"
	catalogrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/catalog"
	lifecyclerepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/lifecycle"
	rosterrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/roster"
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
	return catalogrepo.NewTournamentCatalogPostgres(r.tournaments.tx).CreateTournamentDraft(ctx, command, createdAt)
}

func (r *TournamentCatalogPostgres) GetTournament(
	ctx context.Context,
	id uuid.UUID,
) (*catalogusecase.CatalogTournamentRecord, error) {
	if r == nil || r.tournaments == nil || r.tournaments.tx == nil || id == uuid.Nil {
		return nil, domain.ErrValidation
	}
	return catalogrepo.NewTournamentCatalogPostgres(r.tournaments.tx).GetTournament(ctx, id)
}

func (r *TournamentCatalogPostgres) ListTournaments(ctx context.Context) ([]catalogusecase.CatalogTournamentRecord, error) {
	if r == nil || r.tournaments == nil || r.tournaments.tx == nil {
		return nil, domain.ErrValidation
	}
	return catalogrepo.NewTournamentCatalogPostgres(r.tournaments.tx).ListTournaments(ctx)
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
	return rosterrepo.NewRosterPostgres(r.tournaments.tx).GetRosterSnapshot(ctx, id)
}

func (r *TournamentRosterPostgres) LockRosterAndReserveExpected(
	ctx context.Context,
	rosterID uuid.UUID,
	expectedRevision int64,
	expectedPlayerIDs []uuid.UUID,
	lockedAt time.Time,
) (*rosterusecase.RosterRosterRecord, bool, error) {
	if r == nil || r.tournaments == nil || r.tournaments.tx == nil {
		return nil, false, domain.ErrValidation
	}
	return rosterrepo.NewRosterPostgres(r.tournaments.tx).LockRosterAndReserveExpected(
		ctx, rosterID, expectedRevision, expectedPlayerIDs, lockedAt,
	)
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
	return rosterrepo.NewRosterPostgres(r.tournaments.tx).UnlockRosterAndReleaseExpected(
		ctx, rosterID, expectedRevision, updatedAt,
	)
}
