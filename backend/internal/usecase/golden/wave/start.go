package golden

import (
	"context"
	"crypto/sha256"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
)

type GoldenStartCommand struct {
	Scope             GoldenStateScope
	CommandID         uuid.UUID
	AttemptID         uuid.UUID
	WaveID            uuid.UUID
	WindowID          uuid.UUID
	ExpectedState     GoldenStateExpectation
	ExpectedExecution GoldenWaveExecutionExpectation

	NextExecutionRevisionID uuid.UUID
	NextWindowRevisionID    uuid.UUID
	Authority               authoritydomain.Lease
}

type GoldenStartUseCase struct {
	repository WaveRepository
	clock      WaveClock
}

func NewGoldenStartUseCase(repository WaveRepository, clock WaveClock) *GoldenStartUseCase {
	return &GoldenStartUseCase{repository: repository, clock: clock}
}

func (u *GoldenStartUseCase) Start(
	ctx context.Context,
	command GoldenStartCommand,
) (*GoldenWaveExecution, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateGoldenStartCommand(command); err != nil {
		return nil, false, err
	}
	digest := goldenStartCommandDigest(command)
	if replay, found, err := findGoldenWaveReplay(ctx, u.repository, command.Scope, command.CommandID); err != nil {
		return nil, false, err
	} else if found {
		return reconcileGoldenWaveReplay(command.Scope, command.CommandID,
			GoldenWaveCommandStarted, digest, replay)
	}
	startedAt := u.clock.Now().Round(0).UTC()
	if !domain.IsValidServerTime(startedAt) {
		return nil, false, domain.ErrValidation
	}
	for range goldenWaveCommitAttempts {
		execution, changed, retry, err := u.startAttempt(ctx, command, digest, startedAt)
		if retry {
			continue
		}
		return execution, changed, err
	}
	return nil, false, ErrGoldenWaveCommitConflict
}

func (u *GoldenStartUseCase) startAttempt(
	ctx context.Context,
	command GoldenStartCommand,
	digest [sha256.Size]byte,
	startedAt time.Time,
) (*GoldenWaveExecution, bool, bool, error) {
	loaded, err := loadGoldenWaveCommandAttempt(
		ctx, u.repository, command.Scope, command.CommandID, GoldenWaveCommandStarted, digest,
	)
	if err != nil {
		return nil, false, false, err
	}
	if loaded.Replay != nil {
		return loaded.Replay, false, false, nil
	}
	execution := loaded.Execution
	if execution == nil {
		return nil, false, false, ErrGoldenWaveExecutionNotFound
	}
	state, err := loadValidGoldenState(ctx, u.repository, command.Scope)
	if err != nil {
		return nil, false, false, err
	}
	if err := validateGoldenStartAttempt(state, *execution, command, startedAt); err != nil {
		return nil, false, false, err
	}
	next, condition, err := buildGoldenStartSuccessor(*execution, command, digest, startedAt)
	if err != nil {
		return nil, false, false, err
	}
	expected := execution.Expectation()
	commit := GoldenWaveExecutionCommit{
		ExpectedState: command.ExpectedState, ExpectedExecution: &expected,
		Authority: &condition,
		NewIdentityIDs: []uuid.UUID{
			command.CommandID, command.NextExecutionRevisionID, command.NextWindowRevisionID,
		},
		Next: next,
	}
	SortIDs(commit.NewIdentityIDs)
	return commitGoldenWaveExecution(ctx, u.repository, commit, command.CommandID, GoldenWaveCommandStarted, digest)
}

func validateGoldenStartAttempt(
	state GoldenState,
	execution GoldenWaveExecution,
	command GoldenStartCommand,
	startedAt time.Time,
) error {
	if !goldenStartExpectationMatches(state, execution, command) {
		return ErrGoldenWaveAuthorityConflict
	}
	if err := validateGoldenExecutionSource(state, execution); err != nil {
		return err
	}
	if execution.Revision == math.MaxInt64 || execution.Window.Revision == math.MaxInt64 {
		return ErrGoldenWaveRevisionOverflow
	}
	if startedAt.Before(execution.OpenedAt) || startedAt.After(execution.Deadline) {
		return ErrGoldenReadyWindowClosed
	}
	if !goldenStartEligible(execution) {
		return ErrGoldenWaveAuthorityConflict
	}
	if !goldenStartAuthorityLive(command.Authority, execution.Scope, startedAt) {
		return ErrGoldenWaveAuthorityNotLive
	}
	return validateGoldenStartFreshIDs(state, execution, command)
}

func goldenStartExpectationMatches(
	state GoldenState,
	execution GoldenWaveExecution,
	command GoldenStartCommand,
) bool {
	return state.Expectation().Equal(command.ExpectedState) && execution.Source.Equal(command.ExpectedState) &&
		execution.Expectation().Equal(command.ExpectedExecution) && execution.Attempt.ID == command.AttemptID &&
		execution.Wave.ID == command.WaveID && execution.Window.ID == command.WindowID
}

func goldenStartAuthorityLive(
	authority authoritydomain.Lease,
	scope GoldenStateScope,
	startedAt time.Time,
) bool {
	return authority.TournamentID == scope.TournamentID && authority.Proves(authority.Identity(), startedAt)
}

func goldenStartEligible(execution GoldenWaveExecution) bool {
	if execution.Start != nil ||
		execution.Attempt.State != domain.GoldenAttemptStateWaitingReady ||
		execution.Wave.State != domain.WaveStateReady || execution.Window.State != GoldenReadyWindowOpen ||
		execution.Wave.ReadyWindow == nil || execution.Wave.ReadyWindow.State != domain.ReadyWindowStateOpen ||
		!EqualIDs(execution.Membership.ParticipantIDs, execution.Window.ReadyParticipantIDs) ||
		!EqualIDs(execution.Membership.ParticipantIDs, execution.Window.PresentParticipantIDs) {
		return false
	}
	return true
}

func buildGoldenStartSuccessor(
	execution GoldenWaveExecution,
	command GoldenStartCommand,
	digest [sha256.Size]byte,
	startedAt time.Time,
) (GoldenWaveExecution, GoldenWaveAuthorityCondition, error) {
	deadline := startedAt.Add(time.Duration(execution.Assignment.Snapshot.TimeLimit) * time.Second)
	if execution.Assignment.Snapshot.TimeLimit < 1 || !deadline.After(startedAt) {
		return GoldenWaveExecution{}, GoldenWaveAuthorityCondition{}, goldenWaveError("invalid Golden task deadline")
	}
	authorityDigest := goldenExecutionAuthorityLeaseDigest(command.Authority)
	condition := GoldenWaveAuthorityCondition{
		Identity: command.Authority.Identity(), LeaseRevision: command.Authority.Revision,
		LeaseDigest: authorityDigest,
	}
	if condition.Validate() != nil {
		return GoldenWaveExecution{}, GoldenWaveAuthorityCondition{}, ErrGoldenWaveAuthorityNotLive
	}
	next := execution.Snapshot()
	expected := execution.Expectation()
	next.PreviousRevisionID = UUIDPointer(next.RevisionID)
	next.RevisionID = command.NextExecutionRevisionID
	next.Revision++
	changed, err := next.Wave.Start(next.Window.ID, startedAt)
	if err != nil || !changed {
		return GoldenWaveExecution{}, GoldenWaveAuthorityCondition{}, goldenWaveError("start Wave: %v", err)
	}
	next.Attempt.State = domain.GoldenAttemptStateActive
	next.Attempt.StartedAt = waveCloneTimePointer(&startedAt)
	next.Group.ParticipationEstablished = true
	next.Group.Attempts[len(next.Group.Attempts)-1] = cloneGoldenAttempt(next.Attempt)
	advanceGoldenWindowState(&next.Window, command.NextWindowRevisionID)
	next.Window.State = GoldenReadyWindowConsumed
	start := GoldenStartRecord{
		CommandID: command.CommandID, Scope: command.Scope, AttemptID: command.AttemptID,
		WaveID: command.WaveID, WindowID: command.WindowID, ExpectedState: command.ExpectedState,
		ExpectedExecution: expected, ResultExecutionRevisionID: command.NextExecutionRevisionID,
		ResultWindowRevisionID: command.NextWindowRevisionID, StartedAt: startedAt, Deadline: deadline,
		Authority:   GoldenStartAuthority(condition),
		Assignments: goldenStartedAssignments(next.Assignment, startedAt, deadline, condition),
	}
	start.PayloadDigest = goldenStartRecordDigest(start)
	next.Start = &start
	next.Receipts = append(next.Receipts, GoldenWaveCommandReceipt{
		CommandID: command.CommandID, Scope: command.Scope, Kind: GoldenWaveCommandStarted,
		CommandDigest: digest, Expected: &expected, Result: next.Expectation(), OccurredAt: startedAt,
	})
	if err := SealExecution(&next); err != nil {
		return GoldenWaveExecution{}, GoldenWaveAuthorityCondition{}, err
	}
	if err := next.Validate(); err != nil {
		return GoldenWaveExecution{}, GoldenWaveAuthorityCondition{}, err
	}
	return next.Snapshot(), condition, nil
}

func goldenStartedAssignments(
	assignment GoldenAttemptAssignment,
	startedAt time.Time,
	deadline time.Time,
	authority GoldenWaveAuthorityCondition,
) []GoldenStartedAssignment {
	result := make([]GoldenStartedAssignment, len(assignment.Private))
	for index, private := range assignment.Private {
		result[index] = GoldenStartedAssignment{
			AssignmentID: private.ID, ParticipantID: private.ParticipantID,
			SnapshotID: private.SnapshotID, ContentDigest: private.ContentDigest,
			StartedAt: startedAt, Deadline: deadline, Authority: authority.Identity,
			AuthorityDigest: authority.LeaseDigest, DeliveryEnabled: true,
		}
	}
	return result
}

func validateGoldenStartCommand(command GoldenStartCommand) error {
	if !ValidStateScope(command.Scope) || command.CommandID == uuid.Nil || command.AttemptID == uuid.Nil ||
		command.WaveID == uuid.Nil || command.WindowID == uuid.Nil || command.ExpectedState.Scope != command.Scope ||
		command.ExpectedExecution.Scope != command.Scope || command.NextExecutionRevisionID == uuid.Nil ||
		command.NextWindowRevisionID == uuid.Nil || command.Authority.Validate() != nil {
		return goldenWaveError("invalid Golden start command")
	}
	return nil
}

func validateGoldenStartFreshIDs(
	state GoldenState,
	execution GoldenWaveExecution,
	command GoldenStartCommand,
) error {
	newIDs := make([]uuid.UUID, 0, 7)
	newIDs = append(newIDs,
		command.CommandID, command.NextExecutionRevisionID, command.NextWindowRevisionID,
		command.Authority.HolderID, command.Authority.LeaseID, command.Authority.CommandID,
	)
	if command.Authority.Previous != nil && command.Authority.Previous.LeaseID != command.Authority.LeaseID {
		newIDs = append(newIDs, command.Authority.Previous.LeaseID)
	}
	if err := ValidateFreshIdentityIDs(state, newIDs...); err != nil {
		return err
	}
	reserved := RetainedIdentitySet(execution)
	seen := make(map[uuid.UUID]struct{}, len(newIDs))
	for _, identity := range newIDs {
		if _, exists := reserved[identity]; exists {
			return goldenWaveError("start identity aliases execution authority")
		}
		if _, exists := seen[identity]; exists {
			return goldenWaveError("start identities alias each other")
		}
		seen[identity] = struct{}{}
	}
	return nil
}

func goldenStartCommandDigest(command GoldenStartCommand) [sha256.Size]byte {
	payload, _ := Encode(command)
	return sha256.Sum256(payload)
}

func goldenExecutionAuthorityLeaseDigest(
	lease authoritydomain.Lease,
) [sha256.Size]byte {
	payload, _ := Encode(lease)
	return sha256.Sum256(payload)
}

func GoldenExecutionAuthorityDigest(
	lease authoritydomain.Lease,
) [sha256.Size]byte {
	return goldenExecutionAuthorityLeaseDigest(lease)
}
