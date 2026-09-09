package postgres

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

// PlayoffTerminalPostgres is the durable terminal-stage boundary. All methods
// participate in the caller transaction and lock the stage before reconciling
// deterministic identities.
type PlayoffTerminalPostgres struct {
	tx          *TxManager
	drafts      *DraftPostgres
	assignments *AssignmentPostgres
}

var _ playoff.TerminalRepository = (*PlayoffTerminalPostgres)(nil)

func NewPlayoffTerminalPostgres(
	tx *TxManager,
	drafts *DraftPostgres,
	assignments *AssignmentPostgres,
) *PlayoffTerminalPostgres {
	return &PlayoffTerminalPostgres{tx: tx, drafts: drafts, assignments: assignments}
}

func (repository *PlayoffTerminalPostgres) LoadSemifinalStage(
	ctx context.Context,
	command playoff.TerminalSeriesCommand,
) (*playoff.SemifinalStageAuthority, error) {
	if !validTerminalRepository(repository) || ctx == nil ||
		command.TournamentID == uuid.Nil || command.SeriesID == uuid.Nil {
		return nil, domain.ErrValidation
	}

	var authority *playoff.SemifinalStageAuthority
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		rows, err := repository.tx.Querier(txCtx).LockPostseasonSemifinalAuthority(
			txCtx,
			sqlc.LockPostseasonSemifinalAuthorityParams{
				TournamentID: command.TournamentID,
				SeriesID:     command.SeriesID,
			},
		)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		loaded, err := repository.semifinalStageAuthority(txCtx, rows)
		if err != nil {
			return err
		}
		authority = loaded
		return nil
	})
	if err != nil {
		return nil, terminalRepositoryError("LoadSemifinalStage", err)
	}
	return authority, nil
}
