package result

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gameforfeit "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/forfeit"
	noshowusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/noshow"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

type OperatorResultWorkflow struct {
	transactions OperatorResultTransactionManager
	repository   OperatorResultWorkflowRepository
	postseason   PostseasonWorkflow
}

func NewOperatorResultWorkflow(deps OperatorResultWorkflowDependencies) *OperatorResultWorkflow {
	return &OperatorResultWorkflow{
		transactions: deps.Transactions, repository: deps.Repository, postseason: deps.Postseason,
	}
}

func (w *OperatorResultWorkflow) ResolveNoShow(ctx context.Context, command NoShowCommand) error {
	if ctx == nil || !validNoShowCommand(command) {
		return domain.ErrValidation
	}
	if !w.available() {
		return domain.ErrInternal
	}
	digest, err := operatorResultDigest(OperatorResultActionNoShow, command)
	if err != nil {
		return err
	}
	return w.transactions.Do(ctx, func(txCtx context.Context) error {
		authority, err := w.prepare(
			txCtx,
			command.CommandScope,
			command.SeriesID,
			command.ExpectedAuthorityRevision,
			OperatorResultActionNoShow,
			digest,
		)
		if err != nil || authority == nil {
			return err
		}
		resolvedAt, err := w.readTime(txCtx)
		if err != nil {
			return err
		}
		repository := &operatorNoShowRepository{
			repository: w.repository,
			command:    command,
			digest:     digest,
		}
		resolution, changed, err := noshowusecase.NoShowNewUseCase(
			repository,
			operatorResultClock{at: resolvedAt},
		).Resolve(txCtx, operatorNoShowCommand(command))
		if err != nil {
			return operatorResultError(err, command.ExpectedAuthorityRevision, *authority)
		}
		if !changed || !validOperatorNoShowResolution(resolution, command, resolvedAt) {
			return domain.ErrInternal
		}
		if _, err := w.postseason.AdvanceAfterSeriesSettlement(txCtx, playoff.TerminalSeriesCommand{
			TournamentID: command.TournamentID, SeriesID: command.SeriesID,
		}); err != nil {
			return err
		}
		return nil
	})
}

func (w *OperatorResultWorkflow) RecordForfeit(ctx context.Context, command ForfeitCommand) error {
	if ctx == nil || !validForfeitCommand(command) {
		return domain.ErrValidation
	}
	if !w.available() {
		return domain.ErrInternal
	}
	digest, err := operatorResultDigest(OperatorResultActionForfeit, command)
	if err != nil {
		return err
	}
	return w.transactions.Do(ctx, func(txCtx context.Context) error {
		authority, err := w.prepare(
			txCtx,
			command.CommandScope,
			command.SeriesID,
			command.ExpectedAuthorityRevision,
			OperatorResultActionForfeit,
			digest,
		)
		if err != nil || authority == nil {
			return err
		}
		resolvedAt, err := w.readTime(txCtx)
		if err != nil {
			return err
		}
		repository := &operatorForfeitRepository{
			repository: w.repository,
			command:    command,
			digest:     digest,
		}
		resolution, changed, err := gameforfeit.ForfeitNewUseCase(
			repository,
			operatorResultClock{at: resolvedAt},
		).OperatorForfeit(txCtx, operatorForfeitCommand(command))
		if err != nil {
			return operatorResultError(err, command.ExpectedAuthorityRevision, *authority)
		}
		if !changed || !validOperatorForfeitResolution(resolution, command, resolvedAt) {
			return domain.ErrInternal
		}
		if _, err := w.postseason.AdvanceAfterSeriesSettlement(txCtx, playoff.TerminalSeriesCommand{
			TournamentID: command.TournamentID, SeriesID: command.SeriesID,
		}); err != nil {
			return err
		}
		return nil
	})
}

func (w *OperatorResultWorkflow) prepare(
	ctx context.Context,
	scope CommandScope,
	seriesID uuid.UUID,
	expectedAuthorityRevision int64,
	action OperatorResultAction,
	digest [32]byte,
) (*OperatorResultAuthority, error) {
	authority, err := w.repository.LockOperatorResultAuthority(ctx, scope.TournamentID, seriesID)
	if err != nil {
		return nil, err
	}
	if !validOperatorResultAuthority(authority, scope.TournamentID, seriesID) {
		return nil, domain.ErrInternal
	}
	recorded, err := w.repository.FindOperatorResultCommand(ctx, scope.CommandID)
	if err != nil {
		return nil, err
	}
	if recorded != nil {
		if operatorResultCommandMatches(*recorded, scope, seriesID, expectedAuthorityRevision, action, digest) {
			return nil, nil
		}
		return nil, newOperatorResultConflict(expectedAuthorityRevision, authority)
	}
	if authority.AuthorityRevision != expectedAuthorityRevision {
		return nil, newOperatorResultConflict(expectedAuthorityRevision, authority)
	}
	return &authority, nil
}

func (w *OperatorResultWorkflow) readTime(ctx context.Context) (time.Time, error) {
	value, err := w.repository.ReadOperatorResultTime(ctx)
	if err != nil {
		return time.Time{}, err
	}
	value = value.Round(0).UTC()
	if !domain.IsValidServerTime(value) {
		return time.Time{}, domain.ErrInternal
	}
	return value, nil
}

func (w *OperatorResultWorkflow) available() bool {
	return w != nil && w.transactions != nil && w.repository != nil && w.postseason != nil
}

type operatorResultClock struct {
	at time.Time
}

func (c operatorResultClock) Now() time.Time {
	return c.at
}

type operatorNoShowRepository struct {
	repository OperatorResultWorkflowRepository
	command    NoShowCommand
	digest     [32]byte
}

func (r *operatorNoShowRepository) LoadNormalNoShowAuthority(
	ctx context.Context,
	_ domain.NormalNoShowScope,
) (noshowusecase.NoShowAuthority, error) {
	return r.repository.LoadOperatorNoShowAuthority(ctx, r.command)
}

func (r *operatorNoShowRepository) CommitNormalNoShow(
	ctx context.Context,
	resolution noshowusecase.NoShowResolution,
) (*noshowusecase.NoShowResolution, bool, error) {
	return r.repository.CommitOperatorNoShow(ctx, r.command, r.digest, resolution)
}

type operatorForfeitRepository struct {
	repository OperatorResultWorkflowRepository
	command    ForfeitCommand
	digest     [32]byte
}

func (r *operatorForfeitRepository) LoadForfeitAuthority(
	ctx context.Context,
	_ gameforfeit.Scope,
) (gameforfeit.ForfeitAuthority, error) {
	return r.repository.LoadOperatorForfeitAuthority(ctx, r.command)
}

func (r *operatorForfeitRepository) CommitForfeitResolution(
	ctx context.Context,
	resolution gameforfeit.ForfeitResolution,
) (*gameforfeit.ForfeitResolution, bool, error) {
	return r.repository.CommitOperatorForfeit(ctx, r.command, r.digest, resolution)
}

func operatorResultDigest(action OperatorResultAction, command any) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(struct {
		Action  OperatorResultAction `json:"action"`
		Command any                  `json:"command"`
	}{Action: action, Command: command})
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("OperatorResultWorkflow - encode command: %w", err)
	}
	return sha256.Sum256(payload), nil
}

func operatorResultError(
	err error,
	expectedRevision int64,
	authority OperatorResultAuthority,
) error {
	if errors.Is(err, domain.ErrConflict) || errors.Is(err, noshowusecase.ErrNormalNoShowAuthorityConflict) ||
		errors.Is(err, noshowusecase.ErrNormalNoShowConflict) || errors.Is(err, gameforfeit.ErrForfeitAuthorityConflict) ||
		errors.Is(err, gameforfeit.ErrForfeitCommandReuse) {
		return newOperatorResultConflict(expectedRevision, authority)
	}
	return err
}

func newOperatorResultConflict(expected int64, authority OperatorResultAuthority) error {
	return &RevisionConflictError{
		ExpectedRevision: expected,
		CurrentRevision:  authority.AuthorityRevision,
		CurrentState:     authority.TournamentState,
	}
}

var (
	_ NoShowPort                     = (*OperatorResultWorkflow)(nil)
	_ ForfeitPort                    = (*OperatorResultWorkflow)(nil)
	_ noshowusecase.NoShowRepository = (*operatorNoShowRepository)(nil)
	_ gameforfeit.ForfeitRepository  = (*operatorForfeitRepository)(nil)
)
