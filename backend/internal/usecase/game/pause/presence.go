package pause

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const pausedPresenceAttempts = 2

type PausedPresenceUseCase struct {
	transactions TransactionManager
	repository   PausedPresenceRepository
	clock        PauseClock
}

func NewPausedPresenceUseCase(transactions TransactionManager, repository PausedPresenceRepository, clock PauseClock) *PausedPresenceUseCase {
	return &PausedPresenceUseCase{transactions: transactions, repository: repository, clock: clock}
}

func (u *PausedPresenceUseCase) Change(ctx context.Context, command PausedPresenceCommand) (*PausedPresenceRecord, bool, error) {
	if u == nil || u.transactions == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validatePausedPresenceCommand(command); err != nil {
		return nil, false, err
	}
	for range pausedPresenceAttempts {
		record, changed, retry, err := u.changeAttempt(ctx, command)
		if retry {
			continue
		}
		return record, changed, err
	}
	return nil, false, ErrPausedPresenceConflict
}

func (u *PausedPresenceUseCase) changeAttempt(ctx context.Context, command PausedPresenceCommand) (*PausedPresenceRecord, bool, bool, error) {
	var outcome pausedPresenceAttemptOutcome
	err := u.transactions.Do(ctx, func(txCtx context.Context) error {
		var err error
		outcome, err = u.changeLocked(txCtx, command)
		return err
	})
	if err != nil {
		return nil, false, false, fmt.Errorf("paused presence - transaction: %w", err)
	}
	return outcome.record, outcome.changed, outcome.retry, nil
}

type pausedPresenceAttemptOutcome struct {
	record  *PausedPresenceRecord
	changed bool
	retry   bool
}

func (u *PausedPresenceUseCase) changeLocked(ctx context.Context, command PausedPresenceCommand) (pausedPresenceAttemptOutcome, error) {
	recorded, err := u.findPausedPresenceCommand(ctx, command, "find command")
	if err != nil || recorded != nil {
		return reconcilePausedPresenceOutcome(recorded, command, err)
	}
	authority, err := u.repository.LoadPausedPresenceAuthority(ctx, command.Scope, command.ParticipantID)
	if err != nil {
		return pausedPresenceAttemptOutcome{}, fmt.Errorf("paused presence - load authority: %w", err)
	}
	recorded, err = u.findPausedPresenceCommand(ctx, command, "find locked command")
	if err != nil || recorded != nil {
		return reconcilePausedPresenceOutcome(recorded, command, err)
	}
	changedAt := u.clock.Now().Round(0).UTC().Truncate(time.Microsecond)
	if !pauseValidServerTime(changedAt) {
		return pausedPresenceAttemptOutcome{}, domain.ErrValidation
	}
	if err := validatePausedPresenceAuthority(authority); err != nil {
		return pausedPresenceAttemptOutcome{}, err
	}
	if err := matchPausedPresenceCommand(authority, command); err != nil {
		return pausedPresenceAttemptOutcome{}, err
	}
	built, err := buildPausedPresenceRecord(authority, command, changedAt)
	if err != nil {
		return pausedPresenceAttemptOutcome{}, err
	}
	return u.commitPausedPresence(ctx, authority, command, built)
}

func (u *PausedPresenceUseCase) findPausedPresenceCommand(ctx context.Context, command PausedPresenceCommand, operation string) (*PausedPresenceRecord, error) {
	recorded, err := u.repository.FindPausedPresenceCommand(ctx, command.Scope.TournamentID, command.CommandID)
	if err != nil {
		return nil, fmt.Errorf("paused presence - %s: %w", operation, err)
	}
	return recorded, nil
}

func reconcilePausedPresenceOutcome(recorded *PausedPresenceRecord, command PausedPresenceCommand, err error) (pausedPresenceAttemptOutcome, error) {
	if err != nil {
		return pausedPresenceAttemptOutcome{}, err
	}
	result, err := reconcilePausedPresence(*recorded, command)
	return pausedPresenceAttemptOutcome{record: result}, err
}

func (u *PausedPresenceUseCase) commitPausedPresence(
	ctx context.Context,
	authority PausedPresenceAuthority,
	command PausedPresenceCommand,
	built PausedPresenceRecord,
) (pausedPresenceAttemptOutcome, error) {
	committed, changed, err := u.repository.CommitPausedPresence(ctx, pausedPresenceExpectation(authority, command), built)
	if errors.Is(err, domain.ErrConflict) {
		return pausedPresenceAttemptOutcome{retry: true}, nil
	}
	if err != nil {
		return pausedPresenceAttemptOutcome{}, fmt.Errorf("paused presence - commit Presence: %w", err)
	}
	if committed == nil {
		return pausedPresenceAttemptOutcome{}, domain.ErrInternal
	}
	result, err := reconcilePausedPresence(*committed, command)
	if err != nil || (changed && !pausedPresenceRecordsEqual(*result, built)) {
		return pausedPresenceAttemptOutcome{}, domain.ErrInternal
	}
	return pausedPresenceAttemptOutcome{record: result, changed: changed}, nil
}
