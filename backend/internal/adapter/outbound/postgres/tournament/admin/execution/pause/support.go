package pause

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	draftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

// TournamentAdminNormalPausePostgres persists the normal operator pause graph
// inside the caller's transaction. It is deliberately independent from the
// root postgres package so the execution adapter can be composed directly.
type TournamentAdminNormalPausePostgres struct {
	tx             *db.TxManager
	draftExecution func(*draftrepo.DraftAggregate, int64) (*draftusecase.Execution, error)
}

func NewTournamentAdminNormalPausePostgres(tx *db.TxManager) *TournamentAdminNormalPausePostgres {
	return &TournamentAdminNormalPausePostgres{tx: tx}
}

func NewTournamentAdminNormalPausePostgresWithDependencies(
	tx *db.TxManager,
	draftExecution func(*draftrepo.DraftAggregate, int64) (*draftusecase.Execution, error),
) *TournamentAdminNormalPausePostgres {
	return &TournamentAdminNormalPausePostgres{tx: tx, draftExecution: draftExecution}
}

func validNormalPauseRepository(ctx context.Context, repository *TournamentAdminNormalPausePostgres) bool {
	return ctx != nil && repository != nil && repository.tx != nil && repository.tx.HasPool()
}

func (r *TournamentAdminNormalPausePostgres) loadDraftExecution(
	aggregate *draftrepo.DraftAggregate,
	targetRevision int64,
) (*draftusecase.Execution, error) {
	if r == nil || r.draftExecution == nil {
		return nil, domain.ErrInternal
	}
	return r.draftExecution(aggregate, targetRevision)
}

func tstz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func nullableTSTZ(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *value, Valid: true}
}

func nullableUUID(value *uuid.UUID) uuid.NullUUID {
	if value == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *value, Valid: true}
}

func uniqueTournamentAdminExecutionIDs(values []uuid.UUID) bool {
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value == uuid.Nil {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func executionWriteError(operation string, err error) error {
	return mapRepositoryWriteError("TournamentAdminExecutionPostgres - "+operation, err)
}

func mapRepositoryWriteError(operation string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "23503", "23514", "40001":
			return domain.WrapError(err, domain.ErrConflict)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
