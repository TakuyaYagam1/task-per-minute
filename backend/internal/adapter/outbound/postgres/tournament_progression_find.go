package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

func (r *TournamentProgressionPostgres) FindStageProgression(
	ctx context.Context,
	tournamentID, commandID uuid.UUID,
) (*tournamentprogression.Receipt, error) {
	if r == nil || r.tx == nil || ctx == nil || tournamentID == uuid.Nil || commandID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).FindTournamentStageProgression(
		ctx,
		sqlc.FindTournamentStageProgressionParams{TournamentID: tournamentID, CommandID: commandID},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("TournamentProgressionPostgres - find stage progression: %w", err)
	}
	receipt, err := tournamentProgressionReceipt(row)
	if err != nil {
		return nil, err
	}
	if receipt.Result.ID != tournamentID || receipt.CommandID != commandID {
		return nil, domain.ErrConflict
	}
	return receipt, nil
}
