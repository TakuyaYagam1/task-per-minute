package golden

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type GoldenContinuationUseCase struct {
	repository ContinuationRepository
	clock      ContinuationClock
}

func NewGoldenContinuationUseCase(
	repository ContinuationRepository,
	clock ContinuationClock,
) *GoldenContinuationUseCase {
	return &GoldenContinuationUseCase{repository: repository, clock: clock}
}

func (u *GoldenContinuationUseCase) Continue(
	ctx context.Context,
	command GoldenContinuationCommand,
) (*GoldenContinuationRecord, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	command.PrivateAssignments = append([]GoldenPrivateAssignmentCommand(nil), command.PrivateAssignments...)
	command.ExpectedState = CloneExpectation(command.ExpectedState)
	if err := validateGoldenContinuationCommand(command); err != nil {
		return nil, false, err
	}
	createdAt := u.clock.Now().Round(0).UTC()
	if !domain.IsValidServerTime(createdAt) {
		return nil, false, domain.ErrValidation
	}
	for range goldenContinuationAttempts {
		record, changed, retry, err := u.continueAttempt(ctx, command, createdAt)
		if retry {
			continue
		}
		return record, changed, err
	}
	return nil, false, ErrGoldenContinuationConflict
}

func (u *GoldenContinuationUseCase) continueAttempt(
	ctx context.Context,
	command GoldenContinuationCommand,
	createdAt time.Time,
) (*GoldenContinuationRecord, bool, bool, error) {
	replay, err := u.repository.FindGoldenContinuation(ctx, command.Scope.TournamentID, command.CommandID)
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenContinuationUseCase - find replay: %w", err)
	}
	if replay != nil {
		record, replayErr := reconcileGoldenContinuation(*replay, command)
		return record, false, false, replayErr
	}
	authority, err := u.repository.LoadGoldenContinuationAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenContinuationUseCase - load authority: %w", err)
	}
	if authority.Current != nil {
		return nil, false, false, ErrGoldenContinuationAlreadyCommitted
	}
	if err := validateGoldenContinuationAuthority(authority, command, createdAt); err != nil {
		return nil, false, false, err
	}
	record, err := buildGoldenContinuationRecord(authority, command, createdAt)
	if err != nil {
		return nil, false, false, err
	}
	committed, changed, err := u.repository.CommitGoldenContinuation(ctx, record.Snapshot())
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenContinuationUseCase - commit successor: %w", err)
	}
	if committed == nil || committed.Validate() != nil ||
		!goldenContinuationResultMatches(record, *committed, !changed) {
		return nil, false, false, domain.ErrInternal
	}
	result, reconcileErr := reconcileGoldenContinuation(*committed, command)
	if reconcileErr != nil {
		return nil, false, false, domain.ErrInternal
	}
	return result, changed, false, nil
}

func goldenContinuationResultMatches(
	expected, actual GoldenContinuationRecord,
	allowServerTimeDrift bool,
) bool {
	expected = expected.Snapshot()
	actual = actual.Snapshot()
	if !allowServerTimeDrift {
		return reflect.DeepEqual(expected, actual)
	}
	expected.CreatedAt = time.Time{}
	actual.CreatedAt = time.Time{}
	expected.PayloadDigest = [sha256.Size]byte{}
	actual.PayloadDigest = [sha256.Size]byte{}
	return reflect.DeepEqual(expected, actual)
}
