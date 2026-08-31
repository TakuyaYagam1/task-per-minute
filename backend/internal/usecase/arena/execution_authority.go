package arena

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const executionAuthorityCommitAttempts = 2

var (
	ErrInvalidExecutionAuthority      = errors.New("invalid Arena execution authority")
	ErrExecutionAuthorityForbidden    = errors.New("arena execution authority is forbidden for this process")
	ErrExecutionAuthorityActive       = errors.New("arena execution authority lease is active")
	ErrExecutionAuthorityConflict     = errors.New("arena execution authority conflict")
	ErrExecutionAuthorityCommandReuse = errors.New("arena execution authority command was reused")
	ErrInvalidExecutionEpochReplay    = errors.New("invalid Arena execution epoch replay")
)

type ExecutionProcessKind string

const (
	ExecutionProcessKindAuthority         ExecutionProcessKind = "authority"
	ExecutionProcessKindProjection        ExecutionProcessKind = "projection"
	ExecutionProcessKindReadOnlyTransport ExecutionProcessKind = "read_only_transport"
)

func (k ExecutionProcessKind) IsValid() bool {
	return k == ExecutionProcessKindAuthority || k == ExecutionProcessKindProjection ||
		k == ExecutionProcessKindReadOnlyTransport
}

type ExecutionAuthorityStamp struct {
	LeaseID uuid.UUID
	Epoch   int64
}

func (s ExecutionAuthorityStamp) Validate() error {
	if s.LeaseID == uuid.Nil || s.Epoch < 1 {
		return executionAuthorityError("invalid lease stamp")
	}
	return nil
}

type ExecutionAuthorityIdentity struct {
	TournamentID uuid.UUID
	HolderID     uuid.UUID
	LeaseID      uuid.UUID
	Epoch        int64
	ProcessKind  ExecutionProcessKind
}

func (i ExecutionAuthorityIdentity) Validate() error {
	if i.TournamentID == uuid.Nil || i.HolderID == uuid.Nil || i.LeaseID == uuid.Nil ||
		i.Epoch < 1 || i.ProcessKind != ExecutionProcessKindAuthority {
		return executionAuthorityError("invalid authority identity")
	}
	return nil
}

func (i ExecutionAuthorityIdentity) Stamp() ExecutionAuthorityStamp {
	return ExecutionAuthorityStamp{LeaseID: i.LeaseID, Epoch: i.Epoch}
}

type ExecutionAuthorityLease struct {
	TournamentID uuid.UUID
	HolderID     uuid.UUID
	LeaseID      uuid.UUID
	Epoch        int64
	ProcessKind  ExecutionProcessKind
	Revision     int64
	CommandID    uuid.UUID
	Previous     *ExecutionAuthorityStamp
	AcquiredAt   time.Time
	RenewedAt    time.Time
	ExpiresAt    time.Time
}

func (l ExecutionAuthorityLease) Validate() error {
	if err := validateExecutionAuthorityLeaseIdentity(l); err != nil {
		return err
	}
	if err := validateExecutionAuthorityLeaseInterval(l); err != nil {
		return err
	}
	return validateExecutionAuthorityLeaseContinuity(l)
}

func validateExecutionAuthorityLeaseIdentity(l ExecutionAuthorityLease) error {
	if l.TournamentID == uuid.Nil || l.HolderID == uuid.Nil || l.LeaseID == uuid.Nil ||
		l.CommandID == uuid.Nil || l.Epoch < 1 || l.Revision < l.Epoch ||
		l.ProcessKind != ExecutionProcessKindAuthority {
		return executionAuthorityError("invalid lease identity")
	}
	return nil
}

func validateExecutionAuthorityLeaseInterval(l ExecutionAuthorityLease) error {
	if !validArenaServerTime(l.AcquiredAt) || !validArenaServerTime(l.RenewedAt) ||
		!validArenaServerTime(l.ExpiresAt) || l.RenewedAt.Before(l.AcquiredAt) ||
		!l.ExpiresAt.After(l.RenewedAt) {
		return executionAuthorityError("invalid lease interval")
	}
	return nil
}

func validateExecutionAuthorityLeaseContinuity(l ExecutionAuthorityLease) error {
	if l.Revision == 1 {
		if l.Epoch != 1 || l.Previous != nil {
			return executionAuthorityError("initial lease has predecessor evidence")
		}
		return nil
	}
	if l.Previous == nil || l.Previous.Validate() != nil {
		return executionAuthorityError("invalid predecessor lease stamp")
	}
	sameLeaseRenewal := l.Previous.Epoch == l.Epoch && l.Previous.LeaseID == l.LeaseID
	freshLeaseAcquisition := l.Previous.Epoch == l.Epoch-1 && l.Previous.LeaseID != l.LeaseID
	if !sameLeaseRenewal && !freshLeaseAcquisition {
		return executionAuthorityError("predecessor does not prove lease continuity")
	}
	return nil
}

func (l ExecutionAuthorityLease) Identity() ExecutionAuthorityIdentity {
	return ExecutionAuthorityIdentity{
		TournamentID: l.TournamentID,
		HolderID:     l.HolderID,
		LeaseID:      l.LeaseID,
		Epoch:        l.Epoch,
		ProcessKind:  l.ProcessKind,
	}
}

func (l ExecutionAuthorityLease) Stamp() *ExecutionAuthorityStamp {
	stamp := ExecutionAuthorityStamp{LeaseID: l.LeaseID, Epoch: l.Epoch}
	return &stamp
}

func (l ExecutionAuthorityLease) Proves(identity ExecutionAuthorityIdentity, now time.Time) bool {
	return l.Validate() == nil && identity.Validate() == nil && l.Identity() == identity &&
		validArenaServerTime(now) && !now.Before(l.RenewedAt) && now.Before(l.ExpiresAt)
}

type ExecutionAuthorityClaimCommand struct {
	TournamentID uuid.UUID
	HolderID     uuid.UUID
	LeaseID      uuid.UUID
	CommandID    uuid.UUID
	ProcessKind  ExecutionProcessKind
	Expected     *ExecutionAuthorityStamp
}

type ExecutionAuthorityExpectedState string

const (
	ExecutionAuthorityExpectedAbsent  ExecutionAuthorityExpectedState = "absent"
	ExecutionAuthorityExpectedLive    ExecutionAuthorityExpectedState = "live"
	ExecutionAuthorityExpectedExpired ExecutionAuthorityExpectedState = "expired"
)

type ExecutionAuthorityCommitCondition struct {
	ExpectedRevision int64
	ExpectedStamp    ExecutionAuthorityStamp
	ExpectedState    ExecutionAuthorityExpectedState
}

func (c ExecutionAuthorityCommitCondition) Validate() error {
	switch c.ExpectedState {
	case ExecutionAuthorityExpectedAbsent:
		if c.ExpectedRevision != 0 || c.ExpectedStamp != (ExecutionAuthorityStamp{}) {
			return executionAuthorityError("invalid absent commit condition")
		}
	case ExecutionAuthorityExpectedLive, ExecutionAuthorityExpectedExpired:
		if c.ExpectedRevision < 1 || c.ExpectedStamp.Validate() != nil {
			return executionAuthorityError("invalid existing lease commit condition")
		}
	default:
		return executionAuthorityError("unknown commit condition state")
	}
	return nil
}

// ExecutionAuthorityRepository retains every command result for exact retries
// and owns the atomic compare-and-set boundary. Commit must revalidate the
// expected revision, stamp, and absent/live/expired state using authoritative
// transaction time. It must also prove the proposed successor is still live
// before storing it and the command record together.
type ExecutionAuthorityRepository interface {
	FindExecutionAuthorityCommand(
		ctx context.Context,
		tournamentID uuid.UUID,
		commandID uuid.UUID,
	) (*ExecutionAuthorityLease, error)
	LoadExecutionAuthority(
		ctx context.Context,
		tournamentID uuid.UUID,
	) (*ExecutionAuthorityLease, error)
	CommitExecutionAuthority(
		ctx context.Context,
		condition ExecutionAuthorityCommitCondition,
		lease ExecutionAuthorityLease,
	) (*ExecutionAuthorityLease, bool, error)
}

type ExecutionAuthorityUseCase struct {
	repository    ExecutionAuthorityRepository
	clock         Clock
	leaseDuration time.Duration
}

func NewExecutionAuthorityUseCase(
	repository ExecutionAuthorityRepository,
	clock Clock,
	leaseDuration time.Duration,
) *ExecutionAuthorityUseCase {
	return &ExecutionAuthorityUseCase{
		repository: repository, clock: clock, leaseDuration: leaseDuration,
	}
}

func (u *ExecutionAuthorityUseCase) Claim(
	ctx context.Context,
	command ExecutionAuthorityClaimCommand,
) (*ExecutionAuthorityLease, bool, error) {
	command = cloneExecutionAuthorityClaimCommand(command)
	if command.ProcessKind == ExecutionProcessKindProjection ||
		command.ProcessKind == ExecutionProcessKindReadOnlyTransport {
		return nil, false, ErrExecutionAuthorityForbidden
	}
	if u == nil || u.repository == nil || u.clock == nil || u.leaseDuration <= 0 {
		return nil, false, domain.ErrValidation
	}
	if err := validateExecutionAuthorityClaimCommand(command); err != nil {
		return nil, false, err
	}
	now := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(now) || !now.Add(u.leaseDuration).After(now) {
		return nil, false, domain.ErrValidation
	}

	for range executionAuthorityCommitAttempts {
		lease, changed, retry, err := u.claimAttempt(ctx, command, now)
		if retry {
			continue
		}
		return lease, changed, err
	}
	return nil, false, ErrExecutionAuthorityConflict
}

func (u *ExecutionAuthorityUseCase) claimAttempt(
	ctx context.Context,
	command ExecutionAuthorityClaimCommand,
	now time.Time,
) (*ExecutionAuthorityLease, bool, bool, error) {
	recorded, err := u.repository.FindExecutionAuthorityCommand(
		ctx,
		command.TournamentID,
		command.CommandID,
	)
	if err != nil {
		return nil, false, false,
			fmt.Errorf("ExecutionAuthorityUseCase - find command: %w", err)
	}
	if recorded != nil {
		reconciled, reconcileErr := reconcileExecutionAuthorityLease(*recorded, command)
		return reconciled, false, false, reconcileErr
	}

	loaded, err := u.repository.LoadExecutionAuthority(ctx, command.TournamentID)
	if err != nil {
		return nil, false, false,
			fmt.Errorf("ExecutionAuthorityUseCase - load authority: %w", err)
	}
	current := cloneExecutionAuthorityLeasePointer(loaded)
	if current != nil {
		if err := current.Validate(); err != nil {
			return nil, false, false, err
		}
		if current.TournamentID != command.TournamentID {
			return nil, false, false, executionAuthorityError("lease belongs to another Tournament")
		}
		if current.CommandID == command.CommandID {
			reconciled, reconcileErr := reconcileExecutionAuthorityLease(*current, command)
			return reconciled, false, false, reconcileErr
		}
	}

	proposed, condition, err := u.buildExecutionAuthorityLease(command, current, now)
	if err != nil {
		return nil, false, false, err
	}
	if err := condition.Validate(); err != nil {
		return nil, false, false, err
	}
	committed, changed, err := u.repository.CommitExecutionAuthority(
		ctx,
		condition,
		proposed,
	)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false,
			fmt.Errorf("ExecutionAuthorityUseCase - commit authority: %w", err)
	}
	if !validCommittedExecutionAuthority(committed, proposed, command, changed) {
		return nil, false, false, domain.ErrInternal
	}
	result := cloneExecutionAuthorityLease(*committed)
	return &result, changed, false, nil
}

func (u *ExecutionAuthorityUseCase) buildExecutionAuthorityLease(
	command ExecutionAuthorityClaimCommand,
	current *ExecutionAuthorityLease,
	now time.Time,
) (ExecutionAuthorityLease, ExecutionAuthorityCommitCondition, error) {
	lease := ExecutionAuthorityLease{
		TournamentID: command.TournamentID,
		HolderID:     command.HolderID,
		LeaseID:      command.LeaseID,
		Epoch:        1,
		ProcessKind:  command.ProcessKind,
		Revision:     1,
		CommandID:    command.CommandID,
		AcquiredAt:   now,
		RenewedAt:    now,
		ExpiresAt:    now.Add(u.leaseDuration),
	}
	if current == nil {
		if command.Expected != nil {
			return ExecutionAuthorityLease{}, ExecutionAuthorityCommitCondition{},
				ErrExecutionAuthorityConflict
		}
		if err := lease.Validate(); err != nil {
			return ExecutionAuthorityLease{}, ExecutionAuthorityCommitCondition{}, err
		}
		return lease, ExecutionAuthorityCommitCondition{
			ExpectedState: ExecutionAuthorityExpectedAbsent,
		}, nil
	}
	if !executionAuthorityStampPointersEqual(command.Expected, current.Stamp()) {
		return ExecutionAuthorityLease{}, ExecutionAuthorityCommitCondition{},
			ErrExecutionAuthorityConflict
	}
	if now.Before(current.RenewedAt) {
		return ExecutionAuthorityLease{}, ExecutionAuthorityCommitCondition{},
			ErrExecutionAuthorityConflict
	}
	if current.Revision == math.MaxInt64 {
		return ExecutionAuthorityLease{}, ExecutionAuthorityCommitCondition{},
			ErrExecutionAuthorityConflict
	}
	condition := ExecutionAuthorityCommitCondition{
		ExpectedRevision: current.Revision,
		ExpectedStamp:    *current.Stamp(),
		ExpectedState:    ExecutionAuthorityExpectedLive,
	}
	lease.Revision = current.Revision + 1
	lease.Previous = current.Stamp()
	if now.Before(current.ExpiresAt) {
		if current.HolderID != command.HolderID || current.LeaseID != command.LeaseID {
			return ExecutionAuthorityLease{}, ExecutionAuthorityCommitCondition{},
				ErrExecutionAuthorityActive
		}
		lease.Epoch = current.Epoch
		lease.AcquiredAt = current.AcquiredAt
	} else {
		if current.Epoch == math.MaxInt64 || command.LeaseID == current.LeaseID {
			return ExecutionAuthorityLease{}, ExecutionAuthorityCommitCondition{},
				ErrExecutionAuthorityConflict
		}
		condition.ExpectedState = ExecutionAuthorityExpectedExpired
		lease.Epoch = current.Epoch + 1
	}
	if err := lease.Validate(); err != nil {
		return ExecutionAuthorityLease{}, ExecutionAuthorityCommitCondition{}, err
	}
	return lease, condition, nil
}

func validateExecutionAuthorityClaimCommand(command ExecutionAuthorityClaimCommand) error {
	if command.TournamentID == uuid.Nil || command.HolderID == uuid.Nil ||
		command.LeaseID == uuid.Nil || command.CommandID == uuid.Nil ||
		command.ProcessKind != ExecutionProcessKindAuthority {
		return executionAuthorityError("invalid claim command")
	}
	if command.Expected != nil && command.Expected.Validate() != nil {
		return executionAuthorityError("invalid expected lease stamp")
	}
	return nil
}

func reconcileExecutionAuthorityLease(
	lease ExecutionAuthorityLease,
	command ExecutionAuthorityClaimCommand,
) (*ExecutionAuthorityLease, error) {
	if lease.Validate() != nil || lease.TournamentID != command.TournamentID ||
		lease.HolderID != command.HolderID || lease.LeaseID != command.LeaseID ||
		lease.CommandID != command.CommandID || lease.ProcessKind != command.ProcessKind ||
		!executionAuthorityStampPointersEqual(lease.Previous, command.Expected) {
		return nil, ErrExecutionAuthorityCommandReuse
	}
	clone := cloneExecutionAuthorityLease(lease)
	return &clone, nil
}

func validCommittedExecutionAuthority(
	committed *ExecutionAuthorityLease,
	proposed ExecutionAuthorityLease,
	command ExecutionAuthorityClaimCommand,
	changed bool,
) bool {
	if committed == nil || committed.Validate() != nil {
		return false
	}
	if changed {
		return executionAuthorityLeasesEqual(*committed, proposed)
	}
	_, err := reconcileExecutionAuthorityLease(*committed, command)
	return err == nil
}

func executionAuthorityLeasesEqual(first, second ExecutionAuthorityLease) bool {
	return first.TournamentID == second.TournamentID && first.HolderID == second.HolderID &&
		first.LeaseID == second.LeaseID && first.Epoch == second.Epoch &&
		first.ProcessKind == second.ProcessKind && first.Revision == second.Revision &&
		first.CommandID == second.CommandID &&
		executionAuthorityStampPointersEqual(first.Previous, second.Previous) &&
		first.AcquiredAt.Equal(second.AcquiredAt) && first.RenewedAt.Equal(second.RenewedAt) &&
		first.ExpiresAt.Equal(second.ExpiresAt)
}

func cloneExecutionAuthorityClaimCommand(
	command ExecutionAuthorityClaimCommand,
) ExecutionAuthorityClaimCommand {
	clone := command
	clone.Expected = cloneExecutionAuthorityStampPointer(command.Expected)
	return clone
}

func cloneExecutionAuthorityLease(lease ExecutionAuthorityLease) ExecutionAuthorityLease {
	clone := lease
	clone.Previous = cloneExecutionAuthorityStampPointer(lease.Previous)
	return clone
}

func cloneExecutionAuthorityLeasePointer(
	lease *ExecutionAuthorityLease,
) *ExecutionAuthorityLease {
	if lease == nil {
		return nil
	}
	clone := cloneExecutionAuthorityLease(*lease)
	return &clone
}

func cloneExecutionAuthorityStampPointer(
	stamp *ExecutionAuthorityStamp,
) *ExecutionAuthorityStamp {
	if stamp == nil {
		return nil
	}
	clone := *stamp
	return &clone
}

func executionAuthorityStampPointersEqual(
	first *ExecutionAuthorityStamp,
	second *ExecutionAuthorityStamp,
) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

type ExecutionEpochReplayCommand struct {
	CurrentAuthority ExecutionAuthorityIdentity
	BrokenAuthority  ExecutionAuthorityStamp
	Attempt          FailedAttemptCommand
}

func (c ExecutionEpochReplayCommand) Validate() error {
	if c.CurrentAuthority.Validate() != nil || c.BrokenAuthority.Validate() != nil ||
		c.CurrentAuthority.TournamentID != c.Attempt.Scope.TournamentID ||
		c.CurrentAuthority.Stamp() == c.BrokenAuthority ||
		c.Attempt.FailureClass != NormalAttemptFailureExecutionEpochBreak ||
		validateFailedAttemptCommand(c.Attempt) != nil {
		return executionEpochReplayError("invalid replay command")
	}
	return nil
}

type ExecutionEpochReplayAuthority struct {
	Lease          ExecutionAuthorityLease
	BoundAuthority ExecutionAuthorityStamp
	Attempt        FailedAttemptAuthority
	Current        *ExecutionEpochReplayRecord
}

type ExecutionEpochReplayRecord struct {
	CurrentAuthority      ExecutionAuthorityIdentity
	BrokenAuthority       ExecutionAuthorityStamp
	ExpectedLeaseRevision int64
	Attempt               FailedAttemptRecord
}

type ExecutionEpochReplayCommitCondition struct {
	CurrentAuthority        ExecutionAuthorityIdentity
	ExpectedLeaseRevision   int64
	BrokenAuthority         ExecutionAuthorityStamp
	ExpectedAttemptRevision int64
}

func (c ExecutionEpochReplayCommitCondition) Validate() error {
	if c.CurrentAuthority.Validate() != nil || c.ExpectedLeaseRevision < 1 ||
		c.BrokenAuthority.Validate() != nil || c.ExpectedAttemptRevision < 1 ||
		c.CurrentAuthority.Stamp() == c.BrokenAuthority {
		return executionEpochReplayError("invalid replay commit condition")
	}
	return nil
}

func (r ExecutionEpochReplayRecord) Validate() error {
	if r.CurrentAuthority.Validate() != nil || r.BrokenAuthority.Validate() != nil ||
		r.ExpectedLeaseRevision < 1 || r.CurrentAuthority.TournamentID != r.Attempt.Scope.TournamentID ||
		r.CurrentAuthority.Stamp() == r.BrokenAuthority || r.Attempt.Validate() != nil ||
		r.Attempt.Failure.Class != NormalAttemptFailureExecutionEpochBreak {
		return executionEpochReplayError("invalid replay record")
	}
	return nil
}

// ExecutionEpochReplayRepository owns the atomic revalidation boundary. Its
// commit must use authoritative transaction time to prove the expected lease
// identity and revision are still live, then revalidate the broken bound stamp
// and active attempt authority before writing all failed-attempt records and
// the durable old-Wave route together.
type ExecutionEpochReplayRepository interface {
	LoadExecutionEpochReplayAuthority(
		ctx context.Context,
		scope FailedAttemptScope,
	) (ExecutionEpochReplayAuthority, error)
	CommitExecutionEpochReplay(
		ctx context.Context,
		condition ExecutionEpochReplayCommitCondition,
		record ExecutionEpochReplayRecord,
	) (*ExecutionEpochReplayRecord, bool, error)
}

type ExecutionEpochReplayUseCase struct {
	repository ExecutionEpochReplayRepository
	clock      Clock
}

func NewExecutionEpochReplayUseCase(
	repository ExecutionEpochReplayRepository,
	clock Clock,
) *ExecutionEpochReplayUseCase {
	return &ExecutionEpochReplayUseCase{repository: repository, clock: clock}
}

func (u *ExecutionEpochReplayUseCase) Replay(
	ctx context.Context,
	command ExecutionEpochReplayCommand,
) (*ExecutionEpochReplayRecord, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := command.Validate(); err != nil {
		return nil, false, err
	}
	replayedAt := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(replayedAt) {
		return nil, false, domain.ErrValidation
	}

	for range executionAuthorityCommitAttempts {
		record, changed, retry, err := u.replayAttempt(ctx, command, replayedAt)
		if retry {
			continue
		}
		return record, changed, err
	}
	return nil, false, ErrExecutionAuthorityConflict
}

func (u *ExecutionEpochReplayUseCase) ReplayExecutionEpoch(
	ctx context.Context,
	command ExecutionEpochReplayCommand,
) (bool, error) {
	_, changed, err := u.Replay(ctx, command)
	return changed, err
}

func (u *ExecutionEpochReplayUseCase) replayAttempt(
	ctx context.Context,
	command ExecutionEpochReplayCommand,
	replayedAt time.Time,
) (*ExecutionEpochReplayRecord, bool, bool, error) {
	authority, err := u.loadExecutionEpochReplayAuthority(ctx, command.Attempt.Scope)
	if err != nil {
		return nil, false, false, err
	}
	if authority.Current != nil {
		current, reconcileErr := reconcileExecutionEpochReplay(*authority.Current, command)
		return current, false, false, reconcileErr
	}
	record, condition, err := buildExecutionEpochReplayCommit(authority, command, replayedAt)
	if err != nil {
		return nil, false, false, err
	}
	return u.commitExecutionEpochReplay(ctx, command, record, condition)
}

func (u *ExecutionEpochReplayUseCase) loadExecutionEpochReplayAuthority(
	ctx context.Context,
	scope FailedAttemptScope,
) (ExecutionEpochReplayAuthority, error) {
	loaded, err := u.repository.LoadExecutionEpochReplayAuthority(ctx, scope)
	if err != nil {
		return ExecutionEpochReplayAuthority{},
			fmt.Errorf("ExecutionEpochReplayUseCase - load authority: %w", err)
	}
	authority := cloneExecutionEpochReplayAuthority(loaded)
	if err := validateExecutionEpochReplayAuthority(authority); err != nil {
		return ExecutionEpochReplayAuthority{}, err
	}
	return authority, nil
}

func buildExecutionEpochReplayCommit(
	authority ExecutionEpochReplayAuthority,
	command ExecutionEpochReplayCommand,
	replayedAt time.Time,
) (ExecutionEpochReplayRecord, ExecutionEpochReplayCommitCondition, error) {
	if !authority.Lease.Proves(command.CurrentAuthority, replayedAt) ||
		authority.Attempt.Scope != command.Attempt.Scope ||
		authority.BoundAuthority != command.BrokenAuthority ||
		authority.BoundAuthority == *authority.Lease.Stamp() {
		return ExecutionEpochReplayRecord{}, ExecutionEpochReplayCommitCondition{},
			ErrExecutionAuthorityConflict
	}
	if authority.Attempt.CurrentProjectionRevision == math.MaxInt64 ||
		authority.Attempt.CurrentOrdinal > int(^uint(0)>>1)-2 {
		return ExecutionEpochReplayRecord{}, ExecutionEpochReplayCommitCondition{},
			executionEpochReplayError("attempt revision arithmetic overflow")
	}
	attempt, err := buildFailedAttemptRecord(command.Attempt, authority.Attempt, replayedAt)
	if err != nil {
		if errors.Is(err, ErrFailedAttemptConflict) || errors.Is(err, ErrFailedAttemptUnavailable) {
			return ExecutionEpochReplayRecord{}, ExecutionEpochReplayCommitCondition{},
				fmt.Errorf("%w: %w", ErrExecutionAuthorityConflict, err)
		}
		return ExecutionEpochReplayRecord{}, ExecutionEpochReplayCommitCondition{}, err
	}
	record := ExecutionEpochReplayRecord{
		CurrentAuthority:      command.CurrentAuthority,
		BrokenAuthority:       command.BrokenAuthority,
		ExpectedLeaseRevision: authority.Lease.Revision,
		Attempt:               attempt,
	}
	if err := record.Validate(); err != nil {
		return ExecutionEpochReplayRecord{}, ExecutionEpochReplayCommitCondition{}, err
	}
	condition := ExecutionEpochReplayCommitCondition{
		CurrentAuthority: command.CurrentAuthority, ExpectedLeaseRevision: authority.Lease.Revision,
		BrokenAuthority:         command.BrokenAuthority,
		ExpectedAttemptRevision: authority.Attempt.Revision,
	}
	if err := condition.Validate(); err != nil {
		return ExecutionEpochReplayRecord{}, ExecutionEpochReplayCommitCondition{}, err
	}
	return record, condition, nil
}

func (u *ExecutionEpochReplayUseCase) commitExecutionEpochReplay(
	ctx context.Context,
	command ExecutionEpochReplayCommand,
	record ExecutionEpochReplayRecord,
	condition ExecutionEpochReplayCommitCondition,
) (*ExecutionEpochReplayRecord, bool, bool, error) {
	committed, changed, err := u.repository.CommitExecutionEpochReplay(ctx, condition, record)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false,
			fmt.Errorf("ExecutionEpochReplayUseCase - commit replay: %w", err)
	}
	if !validCommittedExecutionEpochReplay(committed, record, command, changed) {
		return nil, false, false, domain.ErrInternal
	}
	result := cloneExecutionEpochReplayRecord(*committed)
	return &result, changed, false, nil
}

func validateExecutionEpochReplayAuthority(authority ExecutionEpochReplayAuthority) error {
	if authority.Lease.Validate() != nil || authority.BoundAuthority.Validate() != nil ||
		validateFailedAttemptAuthority(authority.Attempt) != nil ||
		authority.Lease.TournamentID != authority.Attempt.Scope.TournamentID {
		return executionEpochReplayError("invalid replay authority")
	}
	if (authority.Current == nil) != (authority.Attempt.Current == nil) {
		return executionEpochReplayError("replay wrapper and attempt current differ")
	}
	if authority.Current != nil &&
		(authority.Current.Validate() != nil ||
			authority.Current.Attempt.Scope != authority.Attempt.Scope ||
			authority.Current.BrokenAuthority != authority.BoundAuthority ||
			authority.Current.ExpectedLeaseRevision > authority.Lease.Revision ||
			!failedAttemptRecordsEqual(authority.Current.Attempt, *authority.Attempt.Current)) {
		return executionEpochReplayError("invalid current replay")
	}
	return nil
}

func reconcileExecutionEpochReplay(
	record ExecutionEpochReplayRecord,
	command ExecutionEpochReplayCommand,
) (*ExecutionEpochReplayRecord, error) {
	if record.Validate() != nil {
		return nil, ErrExecutionAuthorityCommandReuse
	}
	if record.CurrentAuthority != command.CurrentAuthority ||
		record.BrokenAuthority != command.BrokenAuthority {
		return nil, ErrExecutionAuthorityConflict
	}
	if _, err := reconcileFailedAttempt(record.Attempt, command.Attempt); err != nil {
		return nil, err
	}
	clone := cloneExecutionEpochReplayRecord(record)
	return &clone, nil
}

func validCommittedExecutionEpochReplay(
	committed *ExecutionEpochReplayRecord,
	proposed ExecutionEpochReplayRecord,
	command ExecutionEpochReplayCommand,
	changed bool,
) bool {
	if committed == nil || committed.Validate() != nil {
		return false
	}
	if changed {
		return executionEpochReplayRecordsEqual(*committed, proposed)
	}
	_, err := reconcileExecutionEpochReplay(*committed, command)
	return err == nil
}

func executionEpochReplayRecordsEqual(
	first ExecutionEpochReplayRecord,
	second ExecutionEpochReplayRecord,
) bool {
	return first.CurrentAuthority == second.CurrentAuthority &&
		first.BrokenAuthority == second.BrokenAuthority &&
		first.ExpectedLeaseRevision == second.ExpectedLeaseRevision &&
		failedAttemptRecordsEqual(first.Attempt, second.Attempt)
}

func cloneExecutionEpochReplayAuthority(
	authority ExecutionEpochReplayAuthority,
) ExecutionEpochReplayAuthority {
	clone := authority
	clone.Lease = cloneExecutionAuthorityLease(authority.Lease)
	clone.Attempt.Wave = cloneArenaWaveExecution(authority.Attempt.Wave)
	clone.Attempt.Series = cloneSeriesExecution(authority.Attempt.Series)
	clone.Attempt.CurrentGameResultRevisionIDs = append(
		[]domain.ArenaOfficialResultRevisionID(nil),
		authority.Attempt.CurrentGameResultRevisionIDs...,
	)
	if authority.Attempt.Current != nil {
		current := cloneFailedAttemptRecord(*authority.Attempt.Current)
		clone.Attempt.Current = &current
	}
	if authority.Current != nil {
		current := cloneExecutionEpochReplayRecord(*authority.Current)
		clone.Current = &current
	}
	return clone
}

func cloneExecutionEpochReplayRecord(
	record ExecutionEpochReplayRecord,
) ExecutionEpochReplayRecord {
	clone := record
	clone.Attempt = cloneFailedAttemptRecord(record.Attempt)
	return clone
}

func executionAuthorityError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidExecutionAuthority, message)
}

func executionEpochReplayError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidExecutionEpochReplay, message)
}
