package arena

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrTournamentGuardedTransition = errors.New("arena tournament transition requires a dedicated command")

type TournamentLifecycleUseCase struct {
	repository TournamentLifecycleRepository
	clock      Clock
}

func NewTournamentLifecycleUseCase(
	repository TournamentLifecycleRepository,
	clock Clock,
) *TournamentLifecycleUseCase {
	return &TournamentLifecycleUseCase{repository: repository, clock: clock}
}

func (u *TournamentLifecycleUseCase) Transition(
	ctx context.Context,
	command TournamentLifecycleCommand,
) (*TournamentRecord, bool, error) {
	if !u.isAvailable() || !validTournamentLifecycleCommand(command) {
		return nil, false, domain.ErrValidation
	}
	current, err := u.repository.GetTournament(ctx, command.TournamentID)
	if err != nil {
		return nil, false, tournamentLifecycleLookupError("Transition", err)
	}
	if err := validateTournamentRecordPointer(current, command.TournamentID); err != nil {
		return nil, false, err
	}
	if reconciled, ok := reconcileLifecycleTransition(current, command); ok {
		return reconciled, false, nil
	}
	next, err := prepareLifecycleTransition(current, command)
	if err != nil {
		return nil, false, err
	}
	transitionedAt := u.clock.Now()
	if !validArenaServerTime(transitionedAt) {
		return nil, false, domain.ErrValidation
	}
	startedAt := cloneTimePointer(current.StartedAt)
	finishedAt := cloneTimePointer(current.FinishedAt)
	if current.State == domain.ArenaTournamentStateRosterLocked &&
		command.NextState == domain.ArenaTournamentStateSwiss {
		startedAt = &transitionedAt
	}
	if command.NextState == domain.ArenaTournamentStateCompleted {
		finishedAt = &transitionedAt
	}

	updated, changed, err := u.repository.TransitionTournament(ctx, TournamentLifecycleTransitionInput{
		TournamentID: command.TournamentID, ExpectedRevision: command.ExpectedRevision,
		ExpectedState: current.State, NextState: next.State, PausedFromState: next.PausedFromState,
		TransitionedAt: transitionedAt, StartedAt: startedAt, FinishedAt: finishedAt,
	})
	if err != nil {
		return nil, false, tournamentLifecycleMutationError("Transition", err)
	}
	if !changed {
		return u.reconcileUnchangedTransition(ctx, command)
	}
	if err := validateLifecycleTransitionResult(updated, command, transitionedAt); err != nil {
		return nil, false, err
	}
	return cloneArenaTournamentRecord(*updated), true, nil
}

func (u *TournamentLifecycleUseCase) isAvailable() bool {
	return u != nil && u.repository != nil && u.clock != nil
}

func (u *TournamentLifecycleUseCase) reconcileUnchangedTransition(
	ctx context.Context,
	command TournamentLifecycleCommand,
) (*TournamentRecord, bool, error) {
	current, err := u.repository.GetTournament(ctx, command.TournamentID)
	if err != nil {
		return nil, false, tournamentLifecycleLookupError("reconcile transition", err)
	}
	if err := validateTournamentRecordPointer(current, command.TournamentID); err != nil {
		return nil, false, err
	}
	if reconciled, ok := reconcileLifecycleTransition(current, command); ok {
		return reconciled, false, nil
	}
	return nil, false, domain.ErrConflict
}

func validTournamentLifecycleCommand(command TournamentLifecycleCommand) bool {
	return command.TournamentID != uuid.Nil && command.ExpectedRevision >= 1 && command.NextState.IsValid()
}

func guardedTournamentTransition(current, next domain.ArenaTournamentState) bool {
	return current == domain.ArenaTournamentStateTechnicalPause ||
		next == domain.ArenaTournamentStateTechnicalPause ||
		next == domain.ArenaTournamentStateCancelled
}

func prepareLifecycleTransition(
	current *TournamentRecord,
	command TournamentLifecycleCommand,
) (domain.ArenaTournament, error) {
	if current.Revision != command.ExpectedRevision {
		return domain.ArenaTournament{}, domain.ErrConflict
	}
	if guardedTournamentTransition(current.State, command.NextState) {
		return domain.ArenaTournament{}, ErrTournamentGuardedTransition
	}
	next := domain.ArenaTournament{State: current.State, PausedFromState: current.PausedFromState}
	if _, err := next.TransitionTo(command.NextState); err != nil {
		return domain.ArenaTournament{}, err
	}
	return next, nil
}

func reconcileLifecycleTransition(
	current *TournamentRecord,
	command TournamentLifecycleCommand,
) (*TournamentRecord, bool) {
	if current.State != command.NextState {
		return nil, false
	}
	if current.Revision != command.ExpectedRevision && current.Revision != command.ExpectedRevision+1 {
		return nil, false
	}
	return cloneArenaTournamentRecord(*current), true
}

func validateLifecycleTransitionResult(
	record *TournamentRecord,
	command TournamentLifecycleCommand,
	transitionedAt time.Time,
) error {
	if err := validateTournamentRecordPointer(record, command.TournamentID); err != nil {
		return err
	}
	if record.State != command.NextState || record.Revision != command.ExpectedRevision+1 ||
		!record.UpdatedAt.Equal(transitionedAt) {
		return domain.ErrInternal
	}
	if command.NextState == domain.ArenaTournamentStateSwiss && record.StartedAt == nil {
		return domain.ErrInternal
	}
	if command.NextState == domain.ArenaTournamentStateCompleted &&
		(record.FinishedAt == nil || !record.FinishedAt.Equal(transitionedAt)) {
		return domain.ErrInternal
	}
	return nil
}

func validateTournamentRecordPointer(record *TournamentRecord, tournamentID uuid.UUID) error {
	if record == nil || record.ID != tournamentID {
		return domain.ErrInternal
	}
	return validateTournamentRecord(*record)
}

func tournamentLifecycleLookupError(operation string, err error) error {
	if errors.Is(err, ErrTournamentNotFound) {
		return ErrTournamentNotFound
	}
	return fmt.Errorf("TournamentLifecycleUseCase - %s - repository lookup: %w", operation, err)
}

func tournamentLifecycleMutationError(operation string, err error) error {
	if errors.Is(err, domain.ErrConflict) || errors.Is(err, ErrTournamentNotFound) {
		return err
	}
	return fmt.Errorf("TournamentLifecycleUseCase - %s - repository mutation: %w", operation, err)
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
