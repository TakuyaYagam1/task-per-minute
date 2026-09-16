package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

func (r *TournamentAdminExecutionPostgres) CommitNormalPause(
	ctx context.Context,
	expected gameusecase.PauseGraphRevisions,
	record gameusecase.NormalPauseRecord,
) (*gameusecase.NormalPauseRecord, bool, error) {
	if !validTournamentAdminExecutionRepository(ctx, r) {
		return nil, false, domain.ErrValidation
	}
	return r.normalPauseRepository().CommitNormalPause(ctx, expected, record)
}

func (r *TournamentAdminExecutionPostgres) CommitPauseResume(
	ctx context.Context,
	expected gameusecase.PauseResumeExpectation,
	record gameusecase.PauseResumeRecord,
) (*gameusecase.PauseResumeRecord, bool, error) {
	if !validTournamentAdminExecutionRepository(ctx, r) {
		return nil, false, domain.ErrValidation
	}
	return r.normalPauseRepository().CommitPauseResume(ctx, expected, record)
}

func (r *TournamentAdminExecutionPostgres) CommitPauseResumePresence(
	ctx context.Context,
	expected gameusecase.PauseResumePresenceExpectation,
	record gameusecase.PauseResumePresenceRecord,
) (*gameusecase.PauseResumePresenceRecord, bool, error) {
	if !validTournamentAdminExecutionRepository(ctx, r) {
		return nil, false, domain.ErrValidation
	}
	return r.normalPauseRepository().CommitPauseResumePresence(ctx, expected, record)
}

func (r *TournamentAdminExecutionPostgres) RebindNormalPauseWave(
	ctx context.Context,
	tournamentID, waveID, commandID uuid.UUID,
	authority authoritydomain.Identity,
	reboundAt time.Time,
) error {
	if !validTournamentAdminExecutionRepository(ctx, r) {
		return domain.ErrValidation
	}
	return r.normalPauseRepository().RebindNormalPauseWave(ctx, tournamentID, waveID, commandID, authority, reboundAt)
}

func tournamentAdminNormalPauseID(namespace uuid.UUID, kind string, id uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(namespace, []byte(kind+":"+id.String()))
}

func normalPauseCAS(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) || err == nil {
		return fmt.Errorf("%s: %w", operation, domain.ErrConflict)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
