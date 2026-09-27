package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const ActiveTournamentConflictDetail = "another tournament is already active"

var (
	ErrTournamentGuardedTransition = errors.New("tournament transition requires a dedicated command")
	ErrActiveTournamentConflict    = errors.New(ActiveTournamentConflictDetail)
)

type TournamentLifecycleUseCase struct {
	repository TournamentLifecycleRepository
	clock      LifecycleClock
}

func NewTournamentLifecycleUseCase(
	repository TournamentLifecycleRepository,
	clock LifecycleClock,
) *TournamentLifecycleUseCase {
	return &TournamentLifecycleUseCase{
		repository: repository,
		clock:      clock,
	}
}

func (u *TournamentLifecycleUseCase) Transition(
	ctx context.Context,
	command TournamentLifecycleCommand,
) (record *LifecycleTournamentRecord, changed bool, err error) {
	if !u.isAvailable() || !validTournamentLifecycleCommand(command) {
		return nil, false, domain.ErrValidation
	}
	current, err := u.repository.GetTournament(ctx, command.TournamentID)
	if err != nil {
		return nil, false, tournamentLifecycleLookupError("Transition", err)
	}
	if err := lifecycleValidateTournamentRecordPointer(current, command.TournamentID); err != nil {
		return nil, false, err
	}
	if reconciled, ok := reconcileLifecycleTransition(current, command); ok {
		return reconciled, false, nil
	}
	next, err := prepareLifecycleTransition(current, command)
	if err != nil {
		return nil, false, err
	}
	transitionedAt := u.clock.Now().Truncate(time.Microsecond)
	if !lifecycleValidServerTime(transitionedAt) {
		return nil, false, domain.ErrValidation
	}
	startedAt := cloneTimePointer(current.StartedAt)
	finishedAt := cloneTimePointer(current.FinishedAt)
	if current.State == domain.TournamentStateRosterLocked &&
		command.NextState == domain.TournamentStateSwiss {
		startedAt = &transitionedAt
	}
	if command.NextState == domain.TournamentStateCompleted {
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
		reconciled, reconcileErr := u.reconcileUnchangedTransition(ctx, command)
		return reconciled, false, reconcileErr
	}
	if err := validateLifecycleTransitionResult(updated, command, transitionedAt); err != nil {
		return nil, false, err
	}
	return lifecycleCloneTournamentRecord(*updated), true, nil
}

func (u *TournamentLifecycleUseCase) isAvailable() bool {
	return u != nil && u.repository != nil && u.clock != nil
}

func (u *TournamentLifecycleUseCase) reconcileUnchangedTransition(
	ctx context.Context,
	command TournamentLifecycleCommand,
) (*LifecycleTournamentRecord, error) {
	current, err := u.repository.GetTournament(ctx, command.TournamentID)
	if err != nil {
		return nil, tournamentLifecycleLookupError("reconcile transition", err)
	}
	if err := lifecycleValidateTournamentRecordPointer(current, command.TournamentID); err != nil {
		return nil, err
	}
	if reconciled, ok := reconcileLifecycleTransition(current, command); ok {
		return reconciled, nil
	}
	return nil, domain.ErrConflict
}

func validTournamentLifecycleCommand(command TournamentLifecycleCommand) bool {
	return command.TournamentID != uuid.Nil && command.ExpectedRevision >= 1 && command.NextState.IsValid()
}

func guardedTournamentTransition(current, next domain.TournamentState) bool {
	return current == domain.TournamentStateTechnicalPause ||
		next == domain.TournamentStateTechnicalPause ||
		next == domain.TournamentStateCancelled
}

func prepareLifecycleTransition(
	current *LifecycleTournamentRecord,
	command TournamentLifecycleCommand,
) (domain.Tournament, error) {
	if current.Revision != command.ExpectedRevision {
		return domain.Tournament{}, domain.ErrConflict
	}
	if guardedTournamentTransition(current.State, command.NextState) {
		return domain.Tournament{}, ErrTournamentGuardedTransition
	}
	next := domain.Tournament{State: current.State, PausedFromState: current.PausedFromState}
	if _, err := next.TransitionTo(command.NextState); err != nil {
		return domain.Tournament{}, err
	}
	return next, nil
}

func reconcileLifecycleTransition(
	current *LifecycleTournamentRecord,
	command TournamentLifecycleCommand,
) (*LifecycleTournamentRecord, bool) {
	if current.State != command.NextState {
		return nil, false
	}
	if current.Revision != command.ExpectedRevision && current.Revision != command.ExpectedRevision+1 {
		return nil, false
	}
	return lifecycleCloneTournamentRecord(*current), true
}

func validateLifecycleTransitionResult(
	record *LifecycleTournamentRecord,
	command TournamentLifecycleCommand,
	transitionedAt time.Time,
) error {
	if err := lifecycleValidateTournamentRecordPointer(record, command.TournamentID); err != nil {
		return err
	}
	if record.State != command.NextState || record.Revision != command.ExpectedRevision+1 ||
		!record.UpdatedAt.Equal(transitionedAt) {
		return domain.ErrInternal
	}
	if command.NextState == domain.TournamentStateSwiss && record.StartedAt == nil {
		return domain.ErrInternal
	}
	if command.NextState == domain.TournamentStateCompleted &&
		(record.FinishedAt == nil || !record.FinishedAt.Equal(transitionedAt)) {
		return domain.ErrInternal
	}
	return nil
}

func lifecycleValidateTournamentRecordPointer(record *LifecycleTournamentRecord, tournamentID uuid.UUID) error {
	if record == nil || record.ID != tournamentID {
		return domain.ErrInternal
	}
	return lifecycleValidateTournamentRecord(*record)
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
