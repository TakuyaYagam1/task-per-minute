package playoff

import (
	"context"

	"github.com/google/uuid"

	assignmentrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

// PlayoffTerminalPostgres is the durable terminal-stage boundary. All methods
// participate in the caller transaction and lock the stage before reconciling
// deterministic identities.
type PlayoffTerminalPostgres struct {
	tx                 *db.TxManager
	drafts             DraftRepository
	createAssignmentTx AssignmentWriter
}

// DraftRepository is the narrow draft persistence surface required by final
// stage materialization. It keeps the child package independent of the root
// postgres facade.
type DraftRepository interface {
	Create(context.Context, draft.DraftCreateInput) (*draft.DraftAggregate, error)
	Get(context.Context, uuid.UUID) (*draft.DraftAggregate, error)
}

// AssignmentWriter persists an assignment inside the caller's transaction.
// The root facade supplies the existing assignment transaction bridge.
type AssignmentWriter func(context.Context, assignmentrepo.AssignmentCreateInput) error

var _ playoff.TerminalRepository = (*PlayoffTerminalPostgres)(nil)

func NewPlayoffTerminalPostgres(
	tx *db.TxManager,
	drafts DraftRepository,
	createAssignmentTx AssignmentWriter,
) *PlayoffTerminalPostgres {
	return &PlayoffTerminalPostgres{
		tx: tx, drafts: drafts, createAssignmentTx: createAssignmentTx,
	}
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
