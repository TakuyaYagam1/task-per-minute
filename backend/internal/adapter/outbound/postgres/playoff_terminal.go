package postgres

import (
	"context"

	"github.com/google/uuid"

	assignmentrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"
	playoffrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/playoff"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	playoffusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

// PlayoffTerminalPostgres preserves the root adapter contract while the
// terminal-stage implementation lives in the playoff child package.
type PlayoffTerminalPostgres struct {
	tx          *TxManager
	drafts      *DraftPostgres
	assignments *AssignmentPostgres
	inner       *playoffrepo.PlayoffTerminalPostgres
}

func NewPlayoffTerminalPostgres(
	tx *TxManager,
	drafts *DraftPostgres,
	assignments *AssignmentPostgres,
) *PlayoffTerminalPostgres {
	var draftRepository playoffrepo.DraftRepository
	if drafts != nil {
		draftRepository = drafts
	}
	var createAssignmentTx playoffrepo.AssignmentWriter
	if assignments != nil {
		createAssignmentTx = func(ctx context.Context, in assignmentrepo.AssignmentCreateInput) error {
			return assignments.createAssignmentTx(ctx, in)
		}
	}
	return &PlayoffTerminalPostgres{
		tx: tx, drafts: drafts, assignments: assignments,
		inner: playoffrepo.NewPlayoffTerminalPostgres(tx, draftRepository, createAssignmentTx),
	}
}

func (repository *PlayoffTerminalPostgres) child() *playoffrepo.PlayoffTerminalPostgres {
	if repository == nil {
		return nil
	}
	return repository.inner
}

var _ playoffusecase.TerminalRepository = (*PlayoffTerminalPostgres)(nil)

func (repository *PlayoffTerminalPostgres) LoadSemifinalStage(
	ctx context.Context,
	command playoffusecase.TerminalSeriesCommand,
) (*playoffusecase.SemifinalStageAuthority, error) {
	if repository == nil || repository.child() == nil {
		return nil, domain.ErrValidation
	}
	return repository.child().LoadSemifinalStage(ctx, command)
}

func (repository *PlayoffTerminalPostgres) PersistFinalDraft(
	ctx context.Context,
	plan playoffusecase.FinalDraftPlan,
) (bool, error) {
	if repository == nil || repository.child() == nil {
		return false, domain.ErrValidation
	}
	return repository.child().PersistFinalDraft(ctx, plan)
}

func (repository *PlayoffTerminalPostgres) LoadFinalDraft(
	ctx context.Context,
	command playoffusecase.TerminalDraftCommand,
) (*playoffusecase.FinalDraftAuthority, error) {
	if repository == nil || repository.child() == nil {
		return nil, domain.ErrValidation
	}
	return repository.child().LoadFinalDraft(ctx, command)
}

func (repository *PlayoffTerminalPostgres) PersistFinalInitial(
	ctx context.Context,
	plan playoffusecase.FinalInitialPlan,
) (bool, error) {
	if repository == nil || repository.child() == nil {
		return false, domain.ErrValidation
	}
	return repository.child().PersistFinalInitial(ctx, plan)
}

func (repository *PlayoffTerminalPostgres) PersistFinalContinuation(
	ctx context.Context,
	plan playoffusecase.FinalContinuationPlan,
) (bool, error) {
	if repository == nil || repository.child() == nil {
		return false, domain.ErrValidation
	}
	return repository.child().PersistFinalContinuation(ctx, plan)
}

func (repository *PlayoffTerminalPostgres) LoadFinalSettlement(
	ctx context.Context,
	command playoffusecase.TerminalSeriesCommand,
) (*playoffusecase.FinalSettlementAuthority, error) {
	if repository == nil || repository.child() == nil {
		return nil, domain.ErrValidation
	}
	return repository.child().LoadFinalSettlement(ctx, command)
}
