package game

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
)

var (
	ErrInvalidEpochReplay      = errors.New("invalid execution epoch replay")
	ErrEpochReplayConflict     = errors.New("execution epoch replay conflict")
	ErrEpochReplayCommandReuse = errors.New("execution epoch replay command was reused")
)

const epochReplayCommitAttempts = 2

type EpochReplayCommand struct {
	CurrentAuthority authoritydomain.Identity
	BrokenAuthority  authoritydomain.Stamp
	RosterID         uuid.UUID
	Attempt          AttemptCommand
}

func (c EpochReplayCommand) Validate() error {
	if c.CurrentAuthority.Validate() != nil || c.BrokenAuthority.Validate() != nil ||
		c.CurrentAuthority.TournamentID != c.Attempt.Scope.TournamentID ||
		c.RosterID == uuid.Nil ||
		c.CurrentAuthority.Stamp() == c.BrokenAuthority ||
		c.Attempt.FailureClass != gamedomain.FailureExecutionEpochBreak ||
		ValidateCommand(c.Attempt) != nil {
		return epochReplayError("invalid replay command")
	}
	return nil
}

type EpochReplayAuthority struct {
	Lease          authoritydomain.Lease
	BoundAuthority authoritydomain.Stamp
	RosterID       uuid.UUID
	Attempt        AttemptAuthority
	Current        *EpochReplayRecord
}

type EpochReplayRecord struct {
	CurrentAuthority      authoritydomain.Identity
	BrokenAuthority       authoritydomain.Stamp
	RosterID              uuid.UUID
	ExpectedLeaseRevision int64
	CommandDigest         [sha256.Size]byte
	Attempt               AttemptRecord
}

type EpochReplayCommitCondition struct {
	CurrentAuthority        authoritydomain.Identity
	RosterID                uuid.UUID
	ExpectedLeaseRevision   int64
	BrokenAuthority         authoritydomain.Stamp
	ExpectedAttemptRevision int64
}

func (c EpochReplayCommitCondition) Validate() error {
	if c.CurrentAuthority.Validate() != nil || c.RosterID == uuid.Nil || c.ExpectedLeaseRevision < 1 ||
		c.BrokenAuthority.Validate() != nil || c.ExpectedAttemptRevision < 1 ||
		c.CurrentAuthority.Stamp() == c.BrokenAuthority {
		return epochReplayError("invalid replay commit condition")
	}
	return nil
}

func (r EpochReplayRecord) Validate() error {
	if r.CurrentAuthority.Validate() != nil || r.BrokenAuthority.Validate() != nil || r.RosterID == uuid.Nil ||
		r.ExpectedLeaseRevision < 1 || r.CurrentAuthority.TournamentID != r.Attempt.Scope.TournamentID ||
		r.CurrentAuthority.Stamp() == r.BrokenAuthority || r.Attempt.Validate() != nil ||
		r.Attempt.Failure.Class != gamedomain.FailureExecutionEpochBreak ||
		r.CommandDigest == [sha256.Size]byte{} {
		return epochReplayError("invalid replay record")
	}
	return nil
}

type EpochReplayUseCase struct {
	repository EpochReplayRepository
	timeSource AuthorityTimeSource
}

func NewEpochReplayUseCase(
	repository EpochReplayRepository,
	timeSource AuthorityTimeSource,
) *EpochReplayUseCase {
	return &EpochReplayUseCase{repository: repository, timeSource: timeSource}
}

func (u *EpochReplayUseCase) Replay(
	ctx context.Context,
	command EpochReplayCommand,
) (*EpochReplayRecord, bool, error) {
	if u == nil || u.repository == nil || u.timeSource == nil {
		return nil, false, domain.ErrValidation
	}
	if err := command.Validate(); err != nil {
		return nil, false, err
	}
	// A stored immutable result is retry identity, not a fresh mutation. Its
	// retry path should not require a current authority-time sample.
	stored, err := u.repository.FindEpochReplay(ctx, command.Attempt.Scope)
	if err != nil {
		return nil, false, fmt.Errorf("EpochReplayUseCase - find replay: %w", err)
	}
	if stored != nil {
		reconciled, reconcileErr := reconcileEpochReplay(*stored, command)
		return reconciled, false, reconcileErr
	}
	replayedAt, err := u.authorityTime(ctx)
	if err != nil {
		return nil, false, err
	}

	for range epochReplayCommitAttempts {
		record, changed, retry, err := u.replayAttempt(ctx, command, replayedAt)
		if retry {
			continue
		}
		return record, changed, err
	}
	return nil, false, ErrEpochReplayConflict
}

func (u *EpochReplayUseCase) authorityTime(ctx context.Context) (time.Time, error) {
	if ctx == nil || u == nil || u.timeSource == nil {
		return time.Time{}, domain.ErrValidation
	}
	now, err := u.timeSource.AuthorityTime(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("EpochReplayUseCase - authoritative time: %w", err)
	}
	now = now.Round(0).UTC()
	if !domain.IsValidServerTime(now) {
		return time.Time{}, domain.ErrValidation
	}
	return now, nil
}

func (u *EpochReplayUseCase) ReplayEpoch(
	ctx context.Context,
	command EpochReplayCommand,
) (bool, error) {
	_, changed, err := u.Replay(ctx, command)
	return changed, err
}

func (u *EpochReplayUseCase) replayAttempt(
	ctx context.Context,
	command EpochReplayCommand,
	replayedAt time.Time,
) (*EpochReplayRecord, bool, bool, error) {
	current, err := u.repository.FindEpochReplay(ctx, command.Attempt.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("EpochReplayUseCase - find replay: %w", err)
	}
	if current != nil {
		reconciled, reconcileErr := reconcileEpochReplay(*current, command)
		return reconciled, false, false, reconcileErr
	}
	authority, err := u.loadEpochReplayAuthority(ctx, command.Attempt.Scope, command.RosterID)
	if err != nil {
		return nil, false, false, err
	}
	if authority.Current != nil {
		current, reconcileErr := reconcileEpochReplay(*authority.Current, command)
		return current, false, false, reconcileErr
	}
	record, condition, err := buildEpochReplayCommit(authority, command, replayedAt)
	if err != nil {
		return nil, false, false, err
	}
	return u.commitEpochReplay(ctx, command, record, condition)
}

func (u *EpochReplayUseCase) loadEpochReplayAuthority(
	ctx context.Context,
	scope domain.FailedAttemptScope,
	rosterID uuid.UUID,
) (EpochReplayAuthority, error) {
	loaded, err := u.repository.LoadEpochReplayAuthority(ctx, scope, rosterID)
	if err != nil {
		return EpochReplayAuthority{},
			fmt.Errorf("EpochReplayUseCase - load authority: %w", err)
	}
	authority := cloneEpochReplayAuthority(loaded)
	if err := validateEpochReplayAuthority(authority); err != nil {
		return EpochReplayAuthority{}, err
	}
	return authority, nil
}

func buildEpochReplayCommit(
	authority EpochReplayAuthority,
	command EpochReplayCommand,
	replayedAt time.Time,
) (EpochReplayRecord, EpochReplayCommitCondition, error) {
	if !authority.Lease.Proves(command.CurrentAuthority, replayedAt) ||
		authority.Attempt.Scope != command.Attempt.Scope ||
		authority.RosterID != command.RosterID ||
		authority.BoundAuthority != command.BrokenAuthority ||
		authority.BoundAuthority == *authority.Lease.Stamp() {
		return EpochReplayRecord{}, EpochReplayCommitCondition{},
			ErrEpochReplayConflict
	}
	if authority.Attempt.CurrentProjectionRevision == math.MaxInt64 ||
		authority.Attempt.CurrentOrdinal > int(^uint(0)>>1)-2 {
		return EpochReplayRecord{}, EpochReplayCommitCondition{},
			epochReplayError("attempt revision arithmetic overflow")
	}
	attempt, err := BuildRecord(command.Attempt, authority.Attempt, replayedAt)
	if err != nil {
		if errors.Is(err, ErrFailedAttemptConflict) ||
			errors.Is(err, ErrFailedAttemptUnavailable) {
			return EpochReplayRecord{}, EpochReplayCommitCondition{},
				fmt.Errorf("%w: %w", ErrEpochReplayConflict, err)
		}
		return EpochReplayRecord{}, EpochReplayCommitCondition{}, err
	}
	record := EpochReplayRecord{
		CurrentAuthority:      command.CurrentAuthority,
		BrokenAuthority:       command.BrokenAuthority,
		RosterID:              command.RosterID,
		ExpectedLeaseRevision: authority.Lease.Revision,
		CommandDigest:         epochReplayCommandDigest(command),
		Attempt:               attempt,
	}
	if err := record.Validate(); err != nil {
		return EpochReplayRecord{}, EpochReplayCommitCondition{}, err
	}
	condition := EpochReplayCommitCondition{
		CurrentAuthority:        command.CurrentAuthority,
		RosterID:                command.RosterID,
		ExpectedLeaseRevision:   authority.Lease.Revision,
		BrokenAuthority:         command.BrokenAuthority,
		ExpectedAttemptRevision: authority.Attempt.Revision,
	}
	if err := condition.Validate(); err != nil {
		return EpochReplayRecord{}, EpochReplayCommitCondition{}, err
	}
	return record, condition, nil
}

func (u *EpochReplayUseCase) commitEpochReplay(
	ctx context.Context,
	command EpochReplayCommand,
	record EpochReplayRecord,
	condition EpochReplayCommitCondition,
) (*EpochReplayRecord, bool, bool, error) {
	committed, changed, err := u.repository.CommitEpochReplay(ctx, condition, record)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false,
			fmt.Errorf("EpochReplayUseCase - commit replay: %w", err)
	}
	if !validCommittedEpochReplay(committed, record, command, changed) {
		return nil, false, false, domain.ErrInternal
	}
	result := cloneEpochReplayRecord(*committed)
	return &result, changed, false, nil
}

func validateEpochReplayAuthority(authority EpochReplayAuthority) error {
	if authority.Lease.Validate() != nil || authority.BoundAuthority.Validate() != nil || authority.RosterID == uuid.Nil ||
		ValidateAuthority(authority.Attempt) != nil ||
		authority.Lease.TournamentID != authority.Attempt.Scope.TournamentID {
		return epochReplayError("invalid replay authority")
	}
	if (authority.Current == nil) != (authority.Attempt.Current == nil) {
		return epochReplayError("replay wrapper and attempt current differ")
	}
	if authority.Current != nil &&
		(authority.Current.Validate() != nil ||
			authority.Current.Attempt.Scope != authority.Attempt.Scope ||
			authority.Current.RosterID != authority.RosterID ||
			authority.Current.BrokenAuthority != authority.BoundAuthority ||
			authority.Current.ExpectedLeaseRevision > authority.Lease.Revision ||
			!RecordsEqual(authority.Current.Attempt, *authority.Attempt.Current)) {
		return epochReplayError("invalid current replay")
	}
	return nil
}

func reconcileEpochReplay(
	record EpochReplayRecord,
	command EpochReplayCommand,
) (*EpochReplayRecord, error) {
	if record.Validate() != nil {
		return nil, ErrEpochReplayCommandReuse
	}
	// The current authority is commit evidence, not retry identity. A later
	// renewal or an expired-owner takeover must not turn a completed technical
	// replay into a failed retry. The immutable digest covers only the command
	// material supplied to the failed-attempt workflow.
	if record.BrokenAuthority != command.BrokenAuthority || record.RosterID != command.RosterID {
		return nil, ErrEpochReplayConflict
	}
	if record.CommandDigest != epochReplayCommandDigest(command) {
		return nil, ErrEpochReplayCommandReuse
	}
	if _, err := Reconcile(record.Attempt, command.Attempt); err != nil {
		return nil, err
	}
	clone := cloneEpochReplayRecord(record)
	return &clone, nil
}

func validCommittedEpochReplay(
	committed *EpochReplayRecord,
	proposed EpochReplayRecord,
	command EpochReplayCommand,
	changed bool,
) bool {
	if committed == nil || committed.Validate() != nil {
		return false
	}
	if changed {
		return epochReplayRecordsEqual(*committed, proposed)
	}
	_, err := reconcileEpochReplay(*committed, command)
	return err == nil
}

func epochReplayRecordsEqual(
	first EpochReplayRecord,
	second EpochReplayRecord,
) bool {
	return first.CurrentAuthority == second.CurrentAuthority &&
		first.BrokenAuthority == second.BrokenAuthority &&
		first.RosterID == second.RosterID &&
		first.ExpectedLeaseRevision == second.ExpectedLeaseRevision &&
		first.CommandDigest == second.CommandDigest &&
		RecordsEqual(first.Attempt, second.Attempt)
}

func epochReplayCommandDigest(command EpochReplayCommand) [sha256.Size]byte {
	// Authority is server-owned fencing evidence. It deliberately does not
	// participate in request identity so a completed replay remains retryable
	// after the lease has been renewed or taken over by another replica.
	//nolint:musttag // This versioned application-owned document is validated on both encode and decode.
	document, err := json.Marshal(struct {
		BrokenAuthority authoritydomain.Stamp
		RosterID        uuid.UUID
		Attempt         AttemptCommand
	}{
		BrokenAuthority: command.BrokenAuthority,
		RosterID:        command.RosterID,
		Attempt:         command.Attempt,
	})
	if err != nil {
		return [sha256.Size]byte{}
	}
	return sha256.Sum256(document)
}

func epochReplayError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidEpochReplay, message)
}
