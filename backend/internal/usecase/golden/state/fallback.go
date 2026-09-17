package state

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type GoldenFallbackUseCase struct {
	repository StateRepository
	clock      StateClock
}

func NewGoldenFallbackUseCase(repository StateRepository, clock StateClock) *GoldenFallbackUseCase {
	return &GoldenFallbackUseCase{repository: repository, clock: clock}
}

func (u *GoldenFallbackUseCase) Allocate(
	ctx context.Context,
	command GoldenFallbackCommand,
) (*GoldenState, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateGoldenFallbackCommand(command); err != nil {
		return nil, false, err
	}
	allocatedAt := u.clock.Now().Round(0).UTC()
	if !domain.IsValidServerTime(allocatedAt) {
		return nil, false, domain.ErrValidation
	}
	for range goldenStateAttempts {
		state, changed, retry, err := u.allocateAttempt(ctx, command, allocatedAt)
		if retry {
			continue
		}
		return state, changed, err
	}
	return nil, false, ErrGoldenFallbackConflict
}

func (u *GoldenFallbackUseCase) allocateAttempt(
	ctx context.Context,
	command GoldenFallbackCommand,
	allocatedAt time.Time,
) (*GoldenState, bool, bool, error) {
	authority, err := u.repository.LoadGoldenState(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenFallbackUseCase - load state: %w", err)
	}
	if err := authority.Validate(); err != nil {
		return nil, false, false, domain.ErrInternal
	}
	if authority.Allocation != nil {
		state, reconcileErr := reconcileGoldenFallback(authority, command, *authority.Allocation)
		return state, false, false, reconcileErr
	}
	if goldenFallbackCommandUsedElsewhere(authority, command.CommandID) {
		return nil, false, false, ErrGoldenCommandReuse
	}
	if !authority.Expectation().Equal(command.ExpectedState) {
		return nil, false, false, ErrGoldenFallbackAuthorityConflict
	}
	if err := validateGoldenFallbackEligibility(authority, allocatedAt); err != nil {
		return nil, false, false, err
	}
	if err := stateValidateGoldenFreshIDs(
		authority,
		command.CommandID,
		command.AllocationID,
		command.NextStateRevisionID,
	); err != nil {
		return nil, false, false, err
	}
	next, err := buildGoldenFallbackSuccessor(authority, command, allocatedAt)
	if err != nil {
		return nil, false, false, err
	}
	return u.commitGoldenFallback(ctx, command, authority.Expectation(), next)
}

func goldenFallbackCommandUsedElsewhere(state GoldenState, commandID uuid.UUID) bool {
	if _, found := goldenReadyEventByCommand(state.ReadyEvents, commandID); found {
		return true
	}
	_, found := goldenNoShowByCommand(state.NoShows, commandID)
	return found
}

func validateGoldenFallbackEligibility(state GoldenState, allocatedAt time.Time) error {
	terminal, eligible := goldenTerminalNoShow(state)
	if goldenAny(!eligible, allocatedAt.Before(terminal.ResolvedAt), allocatedAt.Before(terminal.Deadline)) {
		return ErrGoldenFallbackAuthorityConflict
	}
	activeCount := len(state.ActiveParticipantIDs())
	if state.Group.ParticipationEstablished && activeCount >= 2 {
		return ErrGoldenFallbackNotRequired
	}
	if !state.Group.ParticipationEstablished && activeCount != 0 {
		return ErrGoldenFallbackAuthorityConflict
	}
	return nil
}

func buildGoldenFallbackSuccessor(
	authority GoldenState,
	command GoldenFallbackCommand,
	allocatedAt time.Time,
) (GoldenState, error) {
	if authority.Revision == math.MaxInt64 {
		return GoldenState{}, fmt.Errorf("%w: fallback successor", ErrGoldenRevisionOverflow)
	}
	allocation, err := buildGoldenAllocation(authority, allocationCommand{
		Scope: command.Scope, CommandID: command.CommandID, AllocationID: command.AllocationID,
		ExpectedState: command.ExpectedState, NextStateRevisionID: command.NextStateRevisionID,
	}, allocatedAt)
	if err != nil {
		return GoldenState{}, err
	}
	next := authority.Snapshot()
	next.PreviousRevisionID = goldenUUID(next.RevisionID)
	next.RevisionID = command.NextStateRevisionID
	next.Revision++
	next.Allocation = &allocation
	return BuildGoldenState(next)
}
func (u *GoldenFallbackUseCase) commitGoldenFallback(
	ctx context.Context,
	command GoldenFallbackCommand,
	expected GoldenStateExpectation,
	next GoldenState,
) (*GoldenState, bool, bool, error) {
	committed, changed, err := u.repository.CommitGoldenState(ctx, GoldenStateCommit{Expected: expected, Next: next})
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenFallbackUseCase - commit state: %w", err)
	}
	if committed == nil || committed.Validate() != nil || committed.Allocation == nil {
		return nil, false, false, domain.ErrInternal
	}
	result, reconcileErr := reconcileGoldenFallback(*committed, command, *committed.Allocation)
	if reconcileErr != nil || (changed && !committed.Expectation().Equal(next.Expectation())) {
		return nil, false, false, domain.ErrInternal
	}
	return result, changed, false, nil
}

func reconcileGoldenFallback(
	state GoldenState,
	command GoldenFallbackCommand,
	allocation GoldenAllocation,
) (*GoldenState, error) {
	if allocation.ID != command.AllocationID || allocation.CommandID != command.CommandID ||
		allocation.Scope != command.Scope || !allocation.ExpectedState.Equal(command.ExpectedState) ||
		allocation.ResultStateRevisionID != command.NextStateRevisionID {
		return nil, ErrGoldenCommandReuse
	}
	clone := state.Snapshot()
	return &clone, nil
}
