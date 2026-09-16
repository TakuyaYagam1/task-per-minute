package postgres

import (
	"context"

	"github.com/google/uuid"

	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	reconnectrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/reconnect"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

func (r *TournamentAdminExecutionPostgres) reconnectRepository() *reconnectrepo.TournamentReconnectPostgres {
	if r == nil {
		return nil
	}
	return reconnectrepo.NewTournamentReconnectPostgresWithDependencies(r.tx, resultauthority.FinalizeProjection)
}

var _ gameusecase.ReconnectRepository = (*TournamentAdminExecutionPostgres)(nil)

// FindCommand loads only the immutable reconnect receipt. Socket identity and
// participant connection leases intentionally remain outside this boundary.
func (r *TournamentAdminExecutionPostgres) FindCommand(
	ctx context.Context,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) (*gameusecase.ReconnectRecord, error) {
	if !validTournamentAdminExecutionRepository(ctx, r) {
		return nil, domain.ErrValidation
	}
	return r.reconnectRepository().FindCommand(ctx, tournamentID, commandID)
}

// LoadAuthority locks the current reconnect graph in the same order as the
// result repository and returns the authority snapshot used by the use case.
func (r *TournamentAdminExecutionPostgres) LoadAuthority(
	ctx context.Context,
	scope pausedomain.GraphScope,
	participantID uuid.UUID,
) (gameusecase.ReconnectAuthority, error) {
	if !validTournamentAdminExecutionRepository(ctx, r) {
		return gameusecase.ReconnectAuthority{}, domain.ErrValidation
	}
	return r.reconnectRepository().LoadAuthority(ctx, scope, participantID)
}

// CommitMutation keeps the historical root adapter API while the reconnect
// transaction and its CAS persistence live in the child repository.
func (r *TournamentAdminExecutionPostgres) CommitMutation(
	ctx context.Context,
	expectedRevision int64,
	record gameusecase.ReconnectRecord,
) (*gameusecase.ReconnectRecord, bool, error) {
	if !validTournamentAdminExecutionRepository(ctx, r) {
		return nil, false, domain.ErrValidation
	}
	return r.reconnectRepository().CommitMutation(ctx, expectedRevision, record)
}
