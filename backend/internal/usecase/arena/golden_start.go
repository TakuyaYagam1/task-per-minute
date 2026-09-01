package arena

import (
	"context"
	"crypto/sha256"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
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
	Authority               ExecutionAuthorityLease
}

type GoldenStartAuthority struct {
	Identity      ExecutionAuthorityIdentity
	LeaseRevision int64
	LeaseDigest   [sha256.Size]byte
}

type GoldenStartedAssignment struct {
	AssignmentID    uuid.UUID
	ParticipantID   uuid.UUID
	SnapshotID      uuid.UUID
	ContentDigest   [sha256.Size]byte
	StartedAt       time.Time
	Deadline        time.Time
	Authority       ExecutionAuthorityIdentity
	AuthorityDigest [sha256.Size]byte
	DeliveryEnabled bool
}

type GoldenStartRecord struct {
	CommandID                 uuid.UUID
	Scope                     GoldenStateScope
	AttemptID                 uuid.UUID
	WaveID                    uuid.UUID
	WindowID                  uuid.UUID
	ExpectedState             GoldenStateExpectation
	ExpectedExecution         GoldenWaveExecutionExpectation
	ResultExecutionRevisionID uuid.UUID
	ResultWindowRevisionID    uuid.UUID
	StartedAt                 time.Time
	Deadline                  time.Time
	Authority                 GoldenStartAuthority
	Assignments               []GoldenStartedAssignment
	PayloadDigest             [sha256.Size]byte
}

type GoldenStartUseCase struct {
	repository GoldenWaveRepository
	clock      Clock
}

func NewGoldenStartUseCase(repository GoldenWaveRepository, clock Clock) *GoldenStartUseCase {
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
	if !validArenaServerTime(startedAt) {
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
	canonicalGoldenIDs(commit.NewIdentityIDs)
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
	authority ExecutionAuthorityLease,
	scope GoldenStateScope,
	startedAt time.Time,
) bool {
	return authority.TournamentID == scope.TournamentID && authority.Proves(authority.Identity(), startedAt)
}

func goldenStartEligible(execution GoldenWaveExecution) bool {
	if execution.Start != nil ||
		execution.Attempt.State != domain.ArenaGoldenAttemptStateWaitingReady ||
		execution.Wave.State != domain.ArenaWaveStateReady || execution.Window.State != GoldenReadyWindowOpen ||
		execution.Wave.ReadyWindow == nil || execution.Wave.ReadyWindow.State != domain.ArenaReadyWindowStateOpen ||
		!equalGoldenIDs(execution.Membership.ParticipantIDs, execution.Window.ReadyParticipantIDs) ||
		!equalGoldenIDs(execution.Membership.ParticipantIDs, execution.Window.PresentParticipantIDs) {
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
	next.PreviousRevisionID = goldenUUID(next.RevisionID)
	next.RevisionID = command.NextExecutionRevisionID
	next.Revision++
	changed, err := next.Wave.Start(next.Window.ID, startedAt)
	if err != nil || !changed {
		return GoldenWaveExecution{}, GoldenWaveAuthorityCondition{}, goldenWaveError("start Wave: %v", err)
	}
	next.Attempt.State = domain.ArenaGoldenAttemptStateActive
	next.Attempt.StartedAt = cloneGoldenTime(&startedAt)
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
	if err := sealGoldenWaveExecution(&next); err != nil {
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

func validateGoldenStartRecord(execution GoldenWaveExecution) error {
	start := execution.Start
	if start == nil {
		return nil
	}
	if !goldenStartPointersPresent(execution, *start) || !goldenStartAnchorsMatch(execution, *start) ||
		!goldenStartTimeLineageMatches(execution, *start) || !goldenStartWindowEvidenceMatches(execution, *start) {
		return goldenWaveError("invalid retained Golden start")
	}
	if err := validateGoldenStartReceipt(execution, *start); err != nil {
		return err
	}
	if goldenStartedReceiptCount(execution.Receipts) != 1 {
		return goldenWaveError("Golden start receipt is not unique")
	}
	return validateGoldenStartedAssignments(execution, *start)
}

func goldenStartPointersPresent(execution GoldenWaveExecution, start GoldenStartRecord) bool {
	return len(execution.Receipts) > 0 && execution.Wave.StartedAt != nil &&
		execution.Wave.ReadyWindow != nil && execution.Wave.ReadyWindow.ConsumedAt != nil &&
		execution.Attempt.StartedAt != nil && execution.PreviousRevisionID != nil &&
		execution.Window.PreviousRevisionID != nil && start.CommandID != uuid.Nil
}

func goldenStartAnchorsMatch(execution GoldenWaveExecution, start GoldenStartRecord) bool {
	return start.Scope == execution.Scope && start.AttemptID == execution.Attempt.ID &&
		start.WaveID == execution.Wave.ID && start.WindowID == execution.Window.ID &&
		start.ExpectedState.Equal(execution.Source) && start.ResultExecutionRevisionID == execution.RevisionID &&
		start.ResultWindowRevisionID == execution.Window.RevisionID
}

func goldenStartTimeLineageMatches(execution GoldenWaveExecution, start GoldenStartRecord) bool {
	return validArenaServerTime(start.StartedAt) && validArenaServerTime(start.Deadline) &&
		!start.StartedAt.Before(execution.OpenedAt) && !start.StartedAt.After(execution.Deadline) &&
		execution.Wave.StartedAt.Equal(start.StartedAt) &&
		execution.Wave.ReadyWindow.ConsumedAt.Equal(start.StartedAt) &&
		execution.Attempt.StartedAt.Equal(start.StartedAt) &&
		execution.Revision == start.ExpectedExecution.Revision+1 &&
		*execution.PreviousRevisionID == start.ExpectedExecution.RevisionID &&
		execution.Window.Revision == start.ExpectedExecution.Window.Revision+1 &&
		*execution.Window.PreviousRevisionID == start.ExpectedExecution.Window.RevisionID
}

func goldenStartWindowEvidenceMatches(execution GoldenWaveExecution, start GoldenStartRecord) bool {
	expectedWindow := start.ExpectedExecution.Window
	return execution.Window.ReadinessRevisionID == expectedWindow.ReadinessRevisionID &&
		execution.Window.ReadinessRevision == expectedWindow.ReadinessRevision &&
		execution.Window.ReadinessDigest == expectedWindow.ReadinessDigest &&
		execution.Window.PresenceRevisionID == expectedWindow.PresenceRevisionID &&
		execution.Window.PresenceRevision == expectedWindow.PresenceRevision &&
		execution.Window.PresenceDigest == expectedWindow.PresenceDigest &&
		start.Deadline.Equal(start.StartedAt.Add(time.Duration(execution.Assignment.Snapshot.TimeLimit)*time.Second)) &&
		start.Authority.Identity.Validate() == nil &&
		start.Authority.Identity.TournamentID == execution.Scope.TournamentID && start.Authority.LeaseRevision >= 1 &&
		start.Authority.LeaseDigest != [sha256.Size]byte{} && start.PayloadDigest == goldenStartRecordDigest(start) &&
		len(start.Assignments) == len(execution.Assignment.Private)
}

func validateGoldenStartReceipt(execution GoldenWaveExecution, start GoldenStartRecord) error {
	lastReceipt := execution.Receipts[len(execution.Receipts)-1]
	if lastReceipt.Kind != GoldenWaveCommandStarted || lastReceipt.CommandID != start.CommandID ||
		lastReceipt.ParticipantID != uuid.Nil || !lastReceipt.OccurredAt.Equal(start.StartedAt) ||
		!start.ExpectedExecution.Equal(lastReceipt.ExpectedValue()) {
		return goldenWaveError("Golden start expectation changed")
	}
	return nil
}

func goldenStartedReceiptCount(receipts []GoldenWaveCommandReceipt) int {
	startReceipts := 0
	for _, receipt := range receipts {
		if receipt.Kind == GoldenWaveCommandStarted {
			startReceipts++
		}
	}
	return startReceipts
}

func validateGoldenStartedAssignments(execution GoldenWaveExecution, start GoldenStartRecord) error {
	for index, assignment := range start.Assignments {
		private := execution.Assignment.Private[index]
		if assignment.AssignmentID != private.ID || assignment.ParticipantID != private.ParticipantID ||
			assignment.SnapshotID != private.SnapshotID || assignment.ContentDigest != private.ContentDigest ||
			!assignment.StartedAt.Equal(start.StartedAt) || !assignment.Deadline.Equal(start.Deadline) ||
			assignment.Authority != start.Authority.Identity || assignment.AuthorityDigest != start.Authority.LeaseDigest ||
			!assignment.DeliveryEnabled {
			return goldenWaveError("started private assignment changed")
		}
	}
	return nil
}

func (r GoldenWaveCommandReceipt) ExpectedValue() GoldenWaveExecutionExpectation {
	if r.Expected == nil {
		return GoldenWaveExecutionExpectation{}
	}
	return *r.Expected
}

func validateGoldenStartCommand(command GoldenStartCommand) error {
	if !validGoldenStateScope(command.Scope) || command.CommandID == uuid.Nil || command.AttemptID == uuid.Nil ||
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
	if err := validateGoldenFreshExecutionIDs(state, newIDs...); err != nil {
		return err
	}
	reserved := goldenExecutionIdentitySet(execution)
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
	payload, _ := goldenEncode(command)
	return sha256.Sum256(payload)
}

func goldenExecutionAuthorityLeaseDigest(lease ExecutionAuthorityLease) [sha256.Size]byte {
	payload, _ := goldenEncode(lease)
	return sha256.Sum256(payload)
}

func GoldenExecutionAuthorityDigest(lease ExecutionAuthorityLease) [sha256.Size]byte {
	return goldenExecutionAuthorityLeaseDigest(lease)
}

func goldenStartRecordDigest(start GoldenStartRecord) [sha256.Size]byte {
	clone := start
	clone.PayloadDigest = [sha256.Size]byte{}
	payload, _ := goldenEncode(clone)
	return sha256.Sum256(payload)
}

func cloneGoldenStartRecord(input *GoldenStartRecord) *GoldenStartRecord {
	if input == nil {
		return nil
	}
	clone := *input
	clone.ExpectedState = cloneGoldenStateExpectation(input.ExpectedState)
	clone.ExpectedExecution.Source = cloneGoldenStateExpectation(input.ExpectedExecution.Source)
	clone.Assignments = append([]GoldenStartedAssignment(nil), input.Assignments...)
	return &clone
}
