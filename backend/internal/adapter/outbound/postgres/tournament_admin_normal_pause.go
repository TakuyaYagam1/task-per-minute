package postgres

import (
	"context"

	"github.com/google/uuid"

	pauserepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution/pause"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

func (r *TournamentAdminExecutionPostgres) normalPauseRepository() *pauserepo.TournamentAdminNormalPausePostgres {
	if r == nil {
		return nil
	}
	return pauserepo.NewTournamentAdminNormalPausePostgresWithDependencies(r.tx, participantDraftExecution)
}

func (r *TournamentAdminExecutionPostgres) FindNormalPauseCommand(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (*gameusecase.NormalPauseRecord, error) {
	// The outer wave_control_commands receipt is written in the same admin
	// transaction and is the sole public idempotency authority.
	return nil, nil
}

func (r *TournamentAdminExecutionPostgres) FindPauseResumeCommand(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (*gameusecase.PauseResumeRecord, error) {
	return nil, nil
}

func (r *TournamentAdminExecutionPostgres) FindPauseResumePresenceCommand(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (*gameusecase.PauseResumePresenceRecord, error) {
	// The enclosing Wave command is the public idempotency authority. Presence
	// decisions are committed in that same transaction and never replayed alone.
	return nil, nil
}

func (r *TournamentAdminExecutionPostgres) ActiveNormalPauseID(
	ctx context.Context,
	scope pausedomain.GraphScope,
) (uuid.UUID, error) {
	if !validTournamentAdminExecutionRepository(ctx, r) {
		return uuid.Nil, domain.ErrValidation
	}
	return r.normalPauseRepository().ActiveNormalPauseID(ctx, scope)
}

func (r *TournamentAdminExecutionPostgres) LoadNormalPauseAuthority(
	ctx context.Context,
	scope pausedomain.GraphScope,
) (gameusecase.NormalPauseAuthority, error) {
	if !validTournamentAdminExecutionRepository(ctx, r) {
		return gameusecase.NormalPauseAuthority{}, domain.ErrValidation
	}
	return r.normalPauseRepository().LoadNormalPauseAuthority(ctx, scope)
}

func (r *TournamentAdminExecutionPostgres) LoadPauseResumeAuthority(
	ctx context.Context,
	scope pausedomain.GraphScope,
	pauseID uuid.UUID,
) (gameusecase.PauseResumeAuthority, error) {
	if !validTournamentAdminExecutionRepository(ctx, r) {
		return gameusecase.PauseResumeAuthority{}, domain.ErrValidation
	}
	return r.normalPauseRepository().LoadPauseResumeAuthority(ctx, scope, pauseID)
}

func (r *TournamentAdminExecutionPostgres) LoadPauseResumePresenceAuthority(
	ctx context.Context,
	scope pausedomain.GraphScope,
	normalPauseID, seriesPauseID, gamePauseID uuid.UUID,
) (gameusecase.PauseResumePresenceAuthority, error) {
	if !validTournamentAdminExecutionRepository(ctx, r) {
		return gameusecase.PauseResumePresenceAuthority{}, domain.ErrValidation
	}
	return r.normalPauseRepository().LoadPauseResumePresenceAuthority(
		ctx, scope, normalPauseID, seriesPauseID, gamePauseID,
	)
}

var _ gameusecase.NormalPauseRepository = (*TournamentAdminExecutionPostgres)(nil)
var _ gameusecase.PauseResumeRepository = (*TournamentAdminExecutionPostgres)(nil)
var _ gameusecase.PauseResumePresenceRepository = (*TournamentAdminExecutionPostgres)(nil)
