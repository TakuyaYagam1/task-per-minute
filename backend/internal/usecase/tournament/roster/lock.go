package roster

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const maxRosterUnlockReasonLength = 512

type RosterLockUseCase struct {
	repository RosterLockRepository
	clock      RosterClock
}

func NewRosterLockUseCase(repository RosterLockRepository, clock RosterClock) *RosterLockUseCase {
	return &RosterLockUseCase{repository: repository, clock: clock}
}

func (u *RosterLockUseCase) LockRoster(
	ctx context.Context,
	command RosterLockCommand,
) (*RosterRosterRecord, bool, error) {
	if !u.isAvailable() || !validRosterLockCommand(command) {
		return nil, false, domain.ErrValidation
	}
	playerIDs, err := canonicalPreflightPlayerIDs(command.Preflight.CheckedInPlayerIDs)
	if err != nil {
		return nil, false, err
	}
	lockedAt := u.clock.Now()
	if !rosterValidServerTime(lockedAt) {
		return nil, false, domain.ErrValidation
	}
	locked, changed, err := u.repository.LockRosterAndReserveExpected(
		ctx,
		command.Preflight.RosterID,
		command.Preflight.RosterRevision,
		playerIDs,
		lockedAt,
	)
	if err != nil {
		return nil, false, rosterMutationError("LockRoster", "LockRosterAndReserve", err)
	}
	if !changed {
		current, getErr := u.repository.GetRosterSnapshot(ctx, command.Preflight.RosterID)
		if getErr != nil {
			return nil, false, rosterLookupError("LockRoster", getErr)
		}
		if current.ExecutionStartedAt != nil {
			return nil, false, domain.ErrRosterExecutionStarted
		}
		return nil, false, domain.ErrConflict
	}
	if !validLockedRosterRecord(locked, command.Preflight.RosterID, command.Preflight.RosterRevision+1) {
		return nil, false, domain.ErrInternal
	}
	return cloneRosterRecord(*locked), true, nil
}

func (u *RosterLockUseCase) UnlockRoster(
	ctx context.Context,
	command RosterUnlockCommand,
) (*RosterRosterRecord, bool, error) {
	if !u.isAvailable() || !validRosterUnlockCommand(command) {
		return nil, false, domain.ErrValidation
	}
	current, err := u.repository.GetRosterSnapshot(ctx, command.RosterID)
	if err != nil {
		return nil, false, rosterLookupError("UnlockRoster", err)
	}
	proceed, reconciled, err := classifyRosterUnlock(current, command)
	if err != nil || !proceed {
		return reconciled, false, err
	}

	updatedAt := u.clock.Now()
	if !rosterValidServerTime(updatedAt) {
		return nil, false, domain.ErrValidation
	}
	unlocked, changed, err := u.repository.UnlockRosterAndReleaseExpected(
		ctx, command.RosterID, command.ExpectedRevision, updatedAt,
	)
	if err != nil {
		return nil, false, rosterMutationError("UnlockRoster", "UnlockRosterAndRelease", err)
	}
	if !changed {
		return u.reconcileUnchangedUnlock(ctx, command)
	}
	if !validUnlockedRosterRecord(unlocked, command.RosterID, command.ExpectedRevision+1) {
		return nil, false, domain.ErrInternal
	}
	return cloneRosterRecord(*unlocked), true, nil
}

func (u *RosterLockUseCase) isAvailable() bool {
	return u != nil && u.repository != nil && u.clock != nil
}

func validRosterLockCommand(command RosterLockCommand) bool {
	return command.Preflight.Approved && command.Preflight.RosterID != uuid.Nil &&
		command.Preflight.RosterRevision >= 1
}

func validRosterUnlockCommand(command RosterUnlockCommand) bool {
	reason := strings.TrimSpace(command.Reason)
	return command.RosterID != uuid.Nil && command.ExpectedRevision >= 1 && command.ActorID != uuid.Nil &&
		reason != "" && len(reason) <= maxRosterUnlockReasonLength
}

func classifyRosterUnlock(
	record *RosterRosterRecord,
	command RosterUnlockCommand,
) (bool, *RosterRosterRecord, error) {
	if record == nil || record.ID != command.RosterID {
		return false, nil, domain.ErrInternal
	}
	if record.ExecutionStartedAt != nil {
		return false, nil, domain.ErrRosterExecutionStarted
	}
	if record.Revision == command.ExpectedRevision+1 && record.LockedAt == nil {
		return false, cloneRosterRecord(*record), nil
	}
	if record.Revision != command.ExpectedRevision || record.LockedAt == nil {
		return false, nil, domain.ErrConflict
	}
	return true, nil, nil
}

func (u *RosterLockUseCase) reconcileUnchangedUnlock(
	ctx context.Context,
	command RosterUnlockCommand,
) (*RosterRosterRecord, bool, error) {
	current, err := u.repository.GetRosterSnapshot(ctx, command.RosterID)
	if err != nil {
		return nil, false, rosterLookupError("UnlockRoster", err)
	}
	_, reconciled, err := classifyRosterUnlock(current, command)
	if err != nil || reconciled != nil {
		return reconciled, false, err
	}
	return nil, false, domain.ErrConflict
}

func rosterMutationError(useCaseOperation string, repositoryOperation string, err error) error {
	if errors.Is(err, domain.ErrConflict) {
		return domain.ErrConflict
	}
	if errors.Is(err, ErrRosterNotFound) {
		return ErrRosterNotFound
	}
	return fmt.Errorf(
		"RosterLockUseCase - %s - RosterLockRepository.%s: %w",
		useCaseOperation,
		repositoryOperation,
		err,
	)
}

func canonicalPreflightPlayerIDs(playerIDs []uuid.UUID) ([]uuid.UUID, error) {
	if len(playerIDs) < domain.TournamentMinParticipants || len(playerIDs) > domain.TournamentMaxParticipants {
		return nil, domain.ErrValidation
	}
	canonical := append([]uuid.UUID(nil), playerIDs...)
	slices.SortFunc(canonical, func(first, second uuid.UUID) int {
		return bytes.Compare(first[:], second[:])
	})
	for index, playerID := range canonical {
		if playerID == uuid.Nil || (index > 0 && playerID == canonical[index-1]) {
			return nil, domain.ErrValidation
		}
	}
	return canonical, nil
}

func validLockedRosterRecord(record *RosterRosterRecord, rosterID uuid.UUID, revision int64) bool {
	return record != nil && record.ID == rosterID && record.Revision == revision &&
		record.LockedAt != nil && record.ExecutionStartedAt == nil
}

func validUnlockedRosterRecord(record *RosterRosterRecord, rosterID uuid.UUID, revision int64) bool {
	return record != nil && record.ID == rosterID && record.Revision == revision &&
		record.LockedAt == nil && record.ExecutionStartedAt == nil
}

func rosterLookupError(operation string, err error) error {
	if errors.Is(err, ErrRosterNotFound) {
		return ErrRosterNotFound
	}
	return fmt.Errorf("RosterLockUseCase - %s - RosterLockRepository.GetRoster: %w", operation, err)
}

func cloneRosterRecord(record RosterRosterRecord) *RosterRosterRecord {
	cloned := record
	if record.LockedAt != nil {
		value := *record.LockedAt
		cloned.LockedAt = &value
	}
	if record.ExecutionStartedAt != nil {
		value := *record.ExecutionStartedAt
		cloned.ExecutionStartedAt = &value
	}
	return &cloned
}
