package postgres

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	gameadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/game"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrGameNotFound = gameadapter.ErrGameNotFound

type GamePostgres = gameadapter.GamePostgres
type GameSlotInput = gameadapter.GameSlotInput
type GameAttemptInput = gameadapter.GameAttemptInput
type GameAttemptRecord = gameadapter.GameAttemptRecord
type GameSlotRecord = gameadapter.GameSlotRecord

// NewGamePostgres constructs the root-package facade for game persistence.
func NewGamePostgres(tx *TxManager) *GamePostgres {
	return gameadapter.NewGamePostgres(tx)
}

// mapRepositoryWriteError remains a narrow root bridge for unmoved postgres
// capabilities that still use the historical package-private helper.
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
