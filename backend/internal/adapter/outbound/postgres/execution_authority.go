package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	authorityusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/authority"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

type ExecutionAuthorityPostgres struct {
	tx *TxManager
}

var (
	_ authorityusecase.Repository      = (*ExecutionAuthorityPostgres)(nil)
	_ authorityusecase.TimeSource      = (*ExecutionAuthorityPostgres)(nil)
	_ recovery.DeadlineAuthoritySource = (*ExecutionAuthorityPostgres)(nil)
)

func NewExecutionAuthorityPostgres(tx *TxManager) *ExecutionAuthorityPostgres {
	return &ExecutionAuthorityPostgres{tx: tx}
}

func (repository *ExecutionAuthorityPostgres) FindAuthorityCommand(
	ctx context.Context,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) (*authoritydomain.Lease, error) {
	if !validExecutionAuthorityRepository(ctx, repository) ||
		tournamentID == uuid.Nil || commandID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	row, err := repository.tx.Querier(ctx).FindExecutionAuthorityCommand(
		ctx,
		sqlc.FindExecutionAuthorityCommandParams{
			TournamentID: tournamentID,
			CommandID:    commandID,
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ExecutionAuthorityPostgres - FindAuthorityCommand: %w", err)
	}
	lease, err := mapExecutionAuthorityLease(row)
	if err != nil {
		return nil, fmt.Errorf("ExecutionAuthorityPostgres - FindAuthorityCommand - map: %w", err)
	}
	return lease, nil
}

func (repository *ExecutionAuthorityPostgres) LoadAuthority(
	ctx context.Context,
	tournamentID uuid.UUID,
) (*authoritydomain.Lease, error) {
	if !validExecutionAuthorityRepository(ctx, repository) || tournamentID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	row, err := repository.tx.Querier(ctx).LoadExecutionAuthority(ctx, tournamentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ExecutionAuthorityPostgres - LoadAuthority: %w", err)
	}
	lease, err := mapExecutionAuthorityLease(row)
	if err != nil {
		return nil, fmt.Errorf("ExecutionAuthorityPostgres - LoadAuthority - map: %w", err)
	}
	return lease, nil
}

// AuthorityTime uses PostgreSQL time for lease planning and controller cache
// decisions. It remains context-bound so an unavailable database fails closed.
func (repository *ExecutionAuthorityPostgres) AuthorityTime(ctx context.Context) (time.Time, error) {
	if !validExecutionAuthorityRepository(ctx, repository) {
		return time.Time{}, domain.ErrValidation
	}
	value, err := repository.tx.Querier(ctx).ReadExecutionAuthorityTime(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("ExecutionAuthorityPostgres - AuthorityTime: %w", err)
	}
	return executionAuthorityTime(value)
}

func (repository *ExecutionAuthorityPostgres) CommitAuthority(
	ctx context.Context,
	condition authorityusecase.CommitCondition,
	lease authoritydomain.Lease,
) (*authoritydomain.Lease, bool, error) {
	if !validExecutionAuthorityRepository(ctx, repository) ||
		condition.Validate() != nil || lease.Validate() != nil {
		return nil, false, domain.ErrValidation
	}

	var committed *authoritydomain.Lease
	changed := false
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		committed, changed, err = repository.commitAuthority(txCtx, condition, lease)
		return err
	})
	if err != nil {
		return nil, false, fmt.Errorf("ExecutionAuthorityPostgres - CommitAuthority: %w", err)
	}
	if committed == nil {
		return nil, false, domain.ErrInternal
	}
	return committed, changed, nil
}

func (repository *ExecutionAuthorityPostgres) commitAuthority(
	ctx context.Context,
	condition authorityusecase.CommitCondition,
	lease authoritydomain.Lease,
) (*authoritydomain.Lease, bool, error) {
	querier := repository.tx.Querier(ctx)
	lockedTournamentID, err := querier.LockExecutionAuthorityScope(ctx, lease.TournamentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, domain.ErrConflict
	}
	if err != nil {
		return nil, false, fmt.Errorf("lock tournament: %w", err)
	}
	if lockedTournamentID != lease.TournamentID {
		return nil, false, domain.ErrInternal
	}
	databaseTime, err := querier.ReadExecutionAuthorityTime(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("read authoritative time: %w", err)
	}
	authoritativeAt, err := executionAuthorityTime(databaseTime)
	if err != nil {
		return nil, false, err
	}

	recorded, err := findExecutionAuthorityCommand(ctx, querier, lease)
	if err != nil {
		return nil, false, err
	}
	if recorded != nil {
		if !executionAuthorityLeasesEqual(*recorded, lease) {
			return nil, false, authoritydomain.ErrCommandReuse
		}
		return recorded, false, nil
	}

	current, err := loadExecutionAuthority(ctx, querier, lease.TournamentID)
	if err != nil {
		return nil, false, err
	}
	if !executionAuthorityCommitMatches(condition, current, lease, authoritativeAt) {
		return nil, false, domain.ErrConflict
	}

	row, err := querier.CreateExecutionAuthorityLease(
		ctx,
		executionAuthorityCreateParams(lease, authoritativeAt),
	)
	if err != nil {
		return nil, false, mapRepositoryWriteError("ExecutionAuthorityPostgres - create lease", err)
	}
	committed, err := mapExecutionAuthorityLease(row)
	if err != nil {
		return nil, false, fmt.Errorf("map committed lease: %w", err)
	}
	if !executionAuthorityLeasesEqual(*committed, lease) {
		return nil, false, domain.ErrInternal
	}
	return committed, true, nil
}

func findExecutionAuthorityCommand(
	ctx context.Context,
	querier *sqlc.Queries,
	lease authoritydomain.Lease,
) (*authoritydomain.Lease, error) {
	row, err := querier.FindExecutionAuthorityCommand(
		ctx,
		sqlc.FindExecutionAuthorityCommandParams{
			TournamentID: lease.TournamentID,
			CommandID:    lease.CommandID,
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find command: %w", err)
	}
	return mapExecutionAuthorityLease(row)
}

func loadExecutionAuthority(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
) (*authoritydomain.Lease, error) {
	row, err := querier.LoadExecutionAuthority(ctx, tournamentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load current lease: %w", err)
	}
	return mapExecutionAuthorityLease(row)
}

func validExecutionAuthorityRepository(
	ctx context.Context,
	repository *ExecutionAuthorityPostgres,
) bool {
	return ctx != nil && repository != nil && repository.tx != nil
}
