package golden

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"

	"github.com/google/uuid"
)

const goldenAttemptCommitAttempts = 3

var (
	ErrInvalidGoldenAttemptCommit           = errors.New("invalid Golden attempt commit")
	ErrGoldenAttemptCommitAuthorityConflict = errors.New("golden attempt commit authority conflict")
	ErrGoldenAttemptCommitConflict          = errors.New("golden attempt commit conflict")
	ErrGoldenAttemptCommitCommandReuse      = errors.New("golden attempt commit command identifier was reused")
	ErrGoldenAttemptNotTerminal             = errors.New("golden attempt is not terminal")
)

type GoldenAttemptTerminalReason string

const (
	GoldenAttemptTerminalAllSolved GoldenAttemptTerminalReason = "all_solved"
	GoldenAttemptTerminalDeadline  GoldenAttemptTerminalReason = "deadline"
)

type GoldenAttemptCommitCommand struct {
	Scope                  GoldenSubmissionScope
	CommandID              uuid.UUID
	CommitID               uuid.UUID
	ExpectedExecution      GoldenWaveExecutionExpectation
	ExpectedSubmissions    GoldenSubmissionLedgerExpectation
	ExpectedPositions      GoldenPositionLedgerExpectation
	ExpectedSwissPoints    GoldenSwissPointLedgerSentinel
	NextPositionRevisionID uuid.UUID
	Reason                 GoldenAttemptTerminalReason
}

type GoldenAttemptCommitAuthority struct {
	Scope       GoldenSubmissionScope
	Execution   GoldenWaveExecution
	Submissions GoldenSubmissionLedger
	Positions   GoldenPositionLedger
	SwissPoints GoldenSwissPointLedgerSentinel
}

type GoldenAttemptCommitUseCase struct {
	repository AttemptRepository
	clock      AttemptClock
}

func NewGoldenAttemptCommitUseCase(
	repository AttemptRepository,
	clock AttemptClock,
) *GoldenAttemptCommitUseCase {
	return &GoldenAttemptCommitUseCase{
		repository: repository,
		clock:      clock,
	}
}

func (u *GoldenAttemptCommitUseCase) CommitAttempt(
	ctx context.Context,
	command GoldenAttemptCommitCommand,
) (*GoldenAttemptCommitRecord, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	command.ExpectedExecution = CloneExecutionExpectation(command.ExpectedExecution)
	if err := validateGoldenAttemptCommitCommand(command); err != nil {
		return nil, false, err
	}
	finishedAt := u.clock.Now().Round(0).UTC()
	if !domain.IsValidServerTime(finishedAt) {
		return nil, false, domain.ErrValidation
	}
	for range goldenAttemptCommitAttempts {
		record, changed, retry, err := u.commitAttempt(ctx, command, finishedAt)
		if retry {
			continue
		}
		return record, changed, err
	}
	return nil, false, ErrGoldenAttemptCommitConflict
}

func (u *GoldenAttemptCommitUseCase) commitAttempt(
	ctx context.Context,
	command GoldenAttemptCommitCommand,
	finishedAt time.Time,
) (*GoldenAttemptCommitRecord, bool, bool, error) {
	replay, err := u.repository.FindGoldenAttemptCommit(ctx, command.Scope.State.TournamentID, command.CommandID)
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenAttemptCommitUseCase - find replay: %w", err)
	}
	if replay != nil {
		record, replayErr := reconcileGoldenAttemptCommit(*replay, command)
		return record, false, false, replayErr
	}
	authority, err := u.repository.LoadGoldenAttemptCommitAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenAttemptCommitUseCase - load authority: %w", err)
	}
	if err := validateGoldenAttemptCommitAuthority(authority, command, finishedAt); err != nil {
		return nil, false, false, err
	}
	record, err := buildGoldenAttemptCommitRecord(authority, command, finishedAt)
	if err != nil {
		return nil, false, false, err
	}
	committed, changed, err := u.repository.CommitGoldenAttempt(ctx, record.Snapshot())
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenAttemptCommitUseCase - commit settlement: %w", err)
	}
	if committed == nil || committed.Validate() != nil ||
		!goldenAttemptCommitResultMatches(record, *committed, !changed) {
		return nil, false, false, domain.ErrInternal
	}
	result, reconcileErr := reconcileGoldenAttemptCommit(*committed, command)
	if reconcileErr != nil {
		return nil, false, false, domain.ErrInternal
	}
	return result, changed, false, nil
}

func goldenAttemptCommitResultMatches(
	expected, actual GoldenAttemptCommitRecord,
	allowServerTimeDrift bool,
) bool {
	expected = expected.Snapshot()
	actual = actual.Snapshot()
	if !allowServerTimeDrift {
		return reflect.DeepEqual(expected, actual)
	}
	expected.FinishedAt = time.Time{}
	actual.FinishedAt = time.Time{}
	expected.Attempt.FinishedAt = nil
	actual.Attempt.FinishedAt = nil
	if len(expected.Group.Attempts) > 0 {
		expected.Group.Attempts[len(expected.Group.Attempts)-1].FinishedAt = nil
	}
	if len(actual.Group.Attempts) > 0 {
		actual.Group.Attempts[len(actual.Group.Attempts)-1].FinishedAt = nil
	}
	expected.PayloadDigest = [sha256.Size]byte{}
	actual.PayloadDigest = [sha256.Size]byte{}
	return reflect.DeepEqual(expected, actual)
}
