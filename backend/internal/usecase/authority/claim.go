package authority

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
)

const claimCommitAttempts = 2

type UseCase struct {
	repository    Repository
	clock         Clock
	timeSource    TimeSource
	leaseDuration time.Duration
}

func New(repository Repository, clock Clock, leaseDuration time.Duration) *UseCase {
	return &UseCase{repository: repository, clock: clock, leaseDuration: leaseDuration}
}

// NewWithTimeSource builds leases from an authoritative, context-bound clock.
// It is the production constructor for durable execution authority.
func NewWithTimeSource(repository Repository, timeSource TimeSource, leaseDuration time.Duration) *UseCase {
	return &UseCase{repository: repository, timeSource: timeSource, leaseDuration: leaseDuration}
}

func (usecase *UseCase) Claim(
	ctx context.Context,
	command ClaimCommand,
) (*authoritydomain.Lease, bool, error) {
	command = cloneClaimCommand(command)
	if command.ProcessKind == authoritydomain.ProcessProjection ||
		command.ProcessKind == authoritydomain.ProcessReadOnlyTransport {
		return nil, false, ErrForbidden
	}
	if usecase == nil || usecase.repository == nil || (usecase.clock == nil && usecase.timeSource == nil) ||
		usecase.leaseDuration <= 0 {
		return nil, false, domain.ErrValidation
	}
	if err := validateClaimCommand(command); err != nil {
		return nil, false, err
	}
	now, err := usecase.claimTime(ctx)
	if err != nil {
		return nil, false, err
	}
	if !domain.IsValidServerTime(now) || !now.Add(usecase.leaseDuration).After(now) {
		return nil, false, domain.ErrValidation
	}

	for range claimCommitAttempts {
		lease, changed, retry, err := usecase.claimAttempt(ctx, command, now)
		if retry {
			continue
		}
		return lease, changed, err
	}
	return nil, false, authoritydomain.ErrConflict
}

func (usecase *UseCase) claimTime(ctx context.Context) (time.Time, error) {
	if usecase.timeSource != nil {
		now, err := usecase.timeSource.AuthorityTime(ctx)
		if err != nil {
			return time.Time{}, fmt.Errorf("authority claim - authoritative time: %w", err)
		}
		return now.Round(0).UTC(), nil
	}
	if usecase.clock == nil {
		return time.Time{}, domain.ErrValidation
	}
	return usecase.clock.Now().Round(0).UTC(), nil
}

func (usecase *UseCase) claimAttempt(
	ctx context.Context,
	command ClaimCommand,
	now time.Time,
) (*authoritydomain.Lease, bool, bool, error) {
	recorded, err := usecase.repository.FindAuthorityCommand(
		ctx,
		command.TournamentID,
		command.CommandID,
	)
	if err != nil {
		return nil, false, false, fmt.Errorf("authority claim - find command: %w", err)
	}
	if recorded != nil {
		reconciled, reconcileErr := reconcileLease(*recorded, command)
		return reconciled, false, false, reconcileErr
	}

	loaded, err := usecase.repository.LoadAuthority(ctx, command.TournamentID)
	if err != nil {
		return nil, false, false, fmt.Errorf("authority claim - load authority: %w", err)
	}
	current := authoritydomain.CloneLease(loaded)
	if current != nil {
		if err := current.Validate(); err != nil {
			return nil, false, false, err
		}
		if current.TournamentID != command.TournamentID {
			return nil, false, false, invalidClaim("lease belongs to another tournament")
		}
		if current.CommandID == command.CommandID {
			reconciled, reconcileErr := reconcileLease(*current, command)
			return reconciled, false, false, reconcileErr
		}
	}

	proposed, condition, err := usecase.buildLease(command, current, now)
	if err != nil {
		return nil, false, false, err
	}
	committed, changed, err := usecase.repository.CommitAuthority(ctx, condition, proposed)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("authority claim - commit authority: %w", err)
	}
	if !validCommittedLease(committed, proposed, command, changed) {
		return nil, false, false, domain.ErrInternal
	}
	result := committed.Clone()
	return &result, changed, false, nil
}
