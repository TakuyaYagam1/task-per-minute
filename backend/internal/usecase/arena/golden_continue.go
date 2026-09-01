package arena

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const goldenContinuationAttempts = 3

var (
	ErrInvalidGoldenContinuation           = errors.New("invalid Golden continuation")
	ErrGoldenContinuationAuthorityConflict = errors.New("golden continuation authority conflict")
	ErrGoldenContinuationConflict          = errors.New("golden continuation conflict")
	ErrGoldenContinuationCommandReuse      = errors.New("golden continuation command identifier was reused")
	ErrGoldenContinuationAlreadyCommitted  = errors.New("golden continuation is already committed")
	ErrGoldenContinuationFallbackRequired  = errors.New("golden continuation requires terminal fallback")
	ErrGoldenContinuationReservesExhausted = errors.New("golden continuation reserves are exhausted")
)

type GoldenReserveAttemptAssignment struct {
	ID            uuid.UUID
	RevisionID    uuid.UUID
	Scope         GoldenStateScope
	AttemptID     uuid.UUID
	EdgeID        uuid.UUID
	EdgePosition  int
	ReservationID uuid.UUID
	SnapshotID    uuid.UUID
	TaskID        uuid.UUID
	ContentDigest [sha256.Size]byte
	Private       []GoldenPrivateAssignment
	PayloadDigest [sha256.Size]byte
}

func (a GoldenReserveAttemptAssignment) Snapshot() GoldenReserveAttemptAssignment {
	clone := a
	clone.Private = append([]GoldenPrivateAssignment(nil), a.Private...)
	return clone
}

func (a GoldenReserveAttemptAssignment) Validate() error {
	if !validGoldenReserveAssignmentIdentity(a) {
		return goldenContinuationError("invalid reserve assignment identity")
	}
	if !uniqueNonZeroUUIDs(goldenReserveAssignmentIDs(a)) {
		return goldenContinuationError("reserve assignment identity is aliased")
	}
	if err := validateGoldenReservePrivateAssignments(a); err != nil {
		return err
	}
	payload, err := goldenReserveAssignmentPayload(a)
	if err != nil || a.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != a.PayloadDigest {
		return goldenContinuationError("reserve assignment digest changed")
	}
	return nil
}

func validGoldenReserveAssignmentIdentity(a GoldenReserveAttemptAssignment) bool {
	return a.ID != uuid.Nil && a.RevisionID != uuid.Nil && validGoldenStateScope(a.Scope) &&
		a.AttemptID != uuid.Nil && a.EdgeID != uuid.Nil && a.EdgePosition > 0 &&
		a.ReservationID != uuid.Nil && a.SnapshotID != uuid.Nil && a.TaskID != uuid.Nil &&
		a.ContentDigest != [sha256.Size]byte{} && len(a.Private) >= 2
}

func goldenReserveAssignmentIDs(a GoldenReserveAttemptAssignment) []uuid.UUID {
	return []uuid.UUID{a.ID, a.RevisionID, a.AttemptID, a.EdgeID, a.ReservationID, a.SnapshotID, a.TaskID}
}

func validateGoldenReservePrivateAssignments(a GoldenReserveAttemptAssignment) error {
	participants := make(map[uuid.UUID]struct{}, len(a.Private))
	assignments := make(map[uuid.UUID]struct{}, len(a.Private))
	for _, private := range a.Private {
		if private.ID == uuid.Nil || private.ParticipantID == uuid.Nil || private.SnapshotID != a.SnapshotID ||
			private.ContentDigest != a.ContentDigest {
			return goldenContinuationError("invalid private reserve assignment")
		}
		if _, duplicate := participants[private.ParticipantID]; duplicate {
			return goldenContinuationError("reserve assignment repeats a participant")
		}
		if _, duplicate := assignments[private.ID]; duplicate {
			return goldenContinuationError("private assignment identity is reused")
		}
		participants[private.ParticipantID] = struct{}{}
		assignments[private.ID] = struct{}{}
	}
	return nil
}

type GoldenContinuationCommand struct {
	Scope                    GoldenStateScope
	CommandID                uuid.UUID
	ContinuationID           uuid.UUID
	ExpectedTerminalID       uuid.UUID
	ExpectedTerminalDigest   [sha256.Size]byte
	ExpectedParentID         uuid.UUID
	ExpectedParentDigest     [sha256.Size]byte
	ExpectedState            GoldenStateExpectation
	ExpectedPlan             GoldenPlanStateBinding
	ExpectedPositions        GoldenPositionLedgerExpectation
	ExpectedSwissPoints      GoldenSwissPointLedgerSentinel
	NextAttemptID            uuid.UUID
	NextAssignmentID         uuid.UUID
	NextAssignmentRevisionID uuid.UUID
	PrivateAssignments       []GoldenPrivateAssignmentCommand
}

type GoldenContinuationAuthority struct {
	Scope       GoldenStateScope
	State       GoldenState
	Plan        GoldenExactPlan
	Terminal    GoldenAttemptCommitRecord
	Positions   GoldenPositionLedger
	SwissPoints GoldenSwissPointLedgerSentinel
	Parent      *GoldenContinuationRecord
	Current     *GoldenContinuationRecord
}

type GoldenContinuationRecord struct {
	ID                       uuid.UUID
	CommandID                uuid.UUID
	CommandDigest            [sha256.Size]byte
	Scope                    GoldenStateScope
	SourceTerminalID         uuid.UUID
	SourceTerminalDigest     [sha256.Size]byte
	SourceParentID           uuid.UUID
	SourceParentDigest       [sha256.Size]byte
	ExpectedState            GoldenStateExpectation
	ExpectedPlan             GoldenPlanStateBinding
	ExpectedPositions        GoldenPositionLedgerExpectation
	SwissPoints              GoldenSwissPointLedgerSentinel
	NewIdentityIDs           []uuid.UUID
	ResolvedParticipantIDs   []uuid.UUID
	UnresolvedParticipantIDs []uuid.UUID
	RemainingPositions       []int
	Group                    domain.ArenaGoldenGroupState
	Attempt                  domain.ArenaGoldenAttempt
	Assignment               GoldenReserveAttemptAssignment
	CreatedAt                time.Time
	Positions                GoldenPositionLedger
	PayloadDigest            [sha256.Size]byte
}

func (r GoldenContinuationRecord) Snapshot() GoldenContinuationRecord {
	clone := r
	clone.ExpectedState = cloneGoldenStateExpectation(r.ExpectedState)
	clone.NewIdentityIDs = append([]uuid.UUID(nil), r.NewIdentityIDs...)
	clone.ResolvedParticipantIDs = append([]uuid.UUID(nil), r.ResolvedParticipantIDs...)
	clone.UnresolvedParticipantIDs = append([]uuid.UUID(nil), r.UnresolvedParticipantIDs...)
	clone.RemainingPositions = append([]int(nil), r.RemainingPositions...)
	clone.Group = cloneGoldenStateGroup(r.Group)
	clone.Attempt = cloneGoldenAttempt(r.Attempt)
	clone.Assignment = r.Assignment.Snapshot()
	clone.Positions = r.Positions.Snapshot()
	return clone
}

func (r GoldenContinuationRecord) Validate() error {
	if !validGoldenContinuationRecordIdentity(r) || !validGoldenContinuationRecordBindings(r) {
		return goldenContinuationError("invalid continuation identity or binding")
	}
	if !validGoldenContinuationSuccessor(r) {
		return goldenContinuationError("successor group does not retain the next attempt")
	}
	if !validGoldenContinuationMembership(r) {
		return goldenContinuationError("successor membership is not exact")
	}
	if err := validateGoldenContinuationPartition(r); err != nil {
		return err
	}
	payload, err := goldenContinuationPayload(r)
	if err != nil || r.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != r.PayloadDigest {
		return goldenContinuationError("continuation payload digest changed")
	}
	return nil
}

func validGoldenContinuationRecordIdentity(r GoldenContinuationRecord) bool {
	return r.ID != uuid.Nil && r.CommandID != uuid.Nil && r.ID != r.CommandID &&
		validGoldenStateScope(r.Scope) && r.CommandDigest != [sha256.Size]byte{} &&
		r.SourceTerminalID != uuid.Nil && r.SourceTerminalDigest != [sha256.Size]byte{} &&
		validGoldenContinuationParentReference(r.SourceParentID, r.SourceParentDigest) &&
		r.SourceTerminalID != r.SourceParentID && !goldenIDsContain(r.NewIdentityIDs, r.SourceTerminalID) &&
		(r.SourceParentID == uuid.Nil || !goldenIDsContain(r.NewIdentityIDs, r.SourceParentID)) &&
		validArenaServerTime(r.CreatedAt) && goldenIDsAreCanonical(r.NewIdentityIDs) &&
		equalGoldenIDs(r.NewIdentityIDs, goldenContinuationRecordIdentityIDs(r))
}

func validGoldenContinuationParentReference(id uuid.UUID, digest [sha256.Size]byte) bool {
	return (id == uuid.Nil) == (digest == [sha256.Size]byte{})
}

func validGoldenContinuationRecordBindings(r GoldenContinuationRecord) bool {
	return r.ExpectedState.Scope == r.Scope && r.ExpectedPlan.GroupID == r.Scope.GroupID &&
		r.ExpectedPlan.GroupRevisionID == r.Scope.GroupRevisionID && r.ExpectedPositions.ScopeIs(r.Scope) &&
		r.SwissPoints.Validate() == nil && r.Attempt.Validate() == nil &&
		r.Attempt.State == domain.ArenaGoldenAttemptStatePlanned && r.Attempt.GroupID == r.Scope.GroupID &&
		r.Attempt.GroupRevisionID == r.Scope.GroupRevisionID && r.Assignment.Validate() == nil &&
		r.Assignment.Scope == r.Scope && r.Assignment.AttemptID == r.Attempt.ID &&
		r.Positions.Validate() == nil && r.Positions.Expectation().Equal(r.ExpectedPositions) &&
		goldenContinuationPositionsMatchGroup(r.Positions, r.Group)
}

func goldenContinuationPositionsMatchGroup(
	positions GoldenPositionLedger,
	group domain.ArenaGoldenGroupState,
) bool {
	if !goldenPositionLedgerMatchesGroup(positions, group) {
		return false
	}
	members := make(map[uuid.UUID]struct{}, len(group.Members))
	for _, member := range group.Members {
		members[member.ParticipantID] = struct{}{}
	}
	return goldenPositionParticipantsBelongToGroup(positions, members)
}

func validGoldenContinuationSuccessor(r GoldenContinuationRecord) bool {
	if _, err := domain.NewArenaGoldenGroup(r.Group); err != nil || len(r.Group.Attempts) < 2 {
		return false
	}
	return reflect.DeepEqual(r.Group.Attempts[len(r.Group.Attempts)-1], r.Attempt)
}

func validGoldenContinuationMembership(r GoldenContinuationRecord) bool {
	return len(r.UnresolvedParticipantIDs) >= 2 &&
		equalGoldenIDs(r.UnresolvedParticipantIDs, r.Attempt.ParticipantIDs) &&
		equalGoldenIDs(r.UnresolvedParticipantIDs, goldenPrivateAssignmentIDs(r.Assignment.Private)) &&
		goldenIDsAreCanonical(r.ResolvedParticipantIDs) && goldenIDsAreCanonical(r.UnresolvedParticipantIDs)
}

// GoldenContinuationRepository consumes one exact terminal receipt under CAS
// against the state, plan, position and read-only Swiss point heads, plus the
// exact parent continuation receipt when the terminal attempt came from a
// reserve. Commit stores the complete successor group, attempt, reserve
// assignment and receipt atomically, then marks the terminal receipt consumed
// exactly once. Replays
// remain globally findable and must be checked before mutable current loads.
// Commit globally reserves every ID in NewIdentityIDs across current and
// archived Golden records in the same transaction as the successor.
type GoldenContinuationRepository interface {
	FindGoldenContinuation(
		ctx context.Context,
		tournamentID uuid.UUID,
		commandID uuid.UUID,
	) (*GoldenContinuationRecord, error)
	LoadGoldenContinuationAuthority(
		ctx context.Context,
		scope GoldenStateScope,
	) (GoldenContinuationAuthority, error)
	CommitGoldenContinuation(
		ctx context.Context,
		record GoldenContinuationRecord,
	) (*GoldenContinuationRecord, bool, error)
}

type GoldenContinuationUseCase struct {
	repository GoldenContinuationRepository
	clock      Clock
}

func NewGoldenContinuationUseCase(
	repository GoldenContinuationRepository,
	clock Clock,
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
	command.ExpectedState = cloneGoldenStateExpectation(command.ExpectedState)
	if err := validateGoldenContinuationCommand(command); err != nil {
		return nil, false, err
	}
	createdAt := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(createdAt) {
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

func validateGoldenContinuationCommand(command GoldenContinuationCommand) error {
	if !validGoldenContinuationCommandHeader(command) {
		return goldenContinuationError("invalid continuation command identity")
	}
	if !validGoldenContinuationCommandParent(command) {
		return goldenContinuationError("invalid parent continuation reference")
	}
	newIdentities := goldenContinuationCommandIdentityIDs(command)
	if !uniqueNonZeroUUIDs(newIdentities) {
		return goldenContinuationError("continuation command identity is aliased")
	}
	if goldenContinuationCommandAliasesSource(command, newIdentities) {
		return goldenContinuationError("continuation identity aliases a source receipt")
	}
	return nil
}

func validGoldenContinuationCommandHeader(command GoldenContinuationCommand) bool {
	return validGoldenStateScope(command.Scope) && command.CommandID != uuid.Nil &&
		command.ContinuationID != uuid.Nil && command.ExpectedTerminalID != uuid.Nil &&
		command.ExpectedTerminalDigest != [sha256.Size]byte{} && command.NextAttemptID != uuid.Nil &&
		command.NextAssignmentID != uuid.Nil && command.NextAssignmentRevisionID != uuid.Nil &&
		command.ExpectedSwissPoints.Validate() == nil
}

func validGoldenContinuationCommandParent(command GoldenContinuationCommand) bool {
	return validGoldenContinuationParentReference(command.ExpectedParentID, command.ExpectedParentDigest) &&
		command.ExpectedParentID != command.ExpectedTerminalID
}

func goldenContinuationCommandAliasesSource(
	command GoldenContinuationCommand,
	newIdentities []uuid.UUID,
) bool {
	return goldenIDsContain(newIdentities, command.ExpectedTerminalID) ||
		(command.ExpectedParentID != uuid.Nil && goldenIDsContain(newIdentities, command.ExpectedParentID))
}

func goldenContinuationCommandIdentityIDs(command GoldenContinuationCommand) []uuid.UUID {
	identities := make([]uuid.UUID, 0, 5+len(command.PrivateAssignments))
	identities = append(identities,
		command.CommandID,
		command.ContinuationID,
		command.NextAttemptID,
		command.NextAssignmentID,
		command.NextAssignmentRevisionID,
	)
	for _, private := range command.PrivateAssignments {
		identities = append(identities, private.AssignmentID)
	}
	canonicalGoldenIDs(identities)
	return identities
}

func goldenContinuationRecordIdentityIDs(record GoldenContinuationRecord) []uuid.UUID {
	identities := make([]uuid.UUID, 0, 5+len(record.Assignment.Private))
	identities = append(identities,
		record.CommandID,
		record.ID,
		record.Attempt.ID,
		record.Assignment.ID,
		record.Assignment.RevisionID,
	)
	for _, private := range record.Assignment.Private {
		identities = append(identities, private.ID)
	}
	canonicalGoldenIDs(identities)
	return identities
}

func validateGoldenContinuationNewIdentities(
	authority GoldenContinuationAuthority,
	command GoldenContinuationCommand,
) error {
	identities := goldenContinuationCommandIdentityIDs(command)
	if err := validateGoldenFreshExecutionIDs(authority.State, identities...); err != nil {
		return goldenContinuationError("successor identity aliases retained state or plan authority")
	}
	reserved := make(map[uuid.UUID]struct{})
	reserveGoldenPositionLedgerIdentities(reserved, authority.Positions)
	reserveGoldenPositionLedgerIdentities(reserved, authority.Terminal.PriorPositions)
	reserveGoldenAttemptCommitIdentities(reserved, authority.Terminal)
	if authority.Parent != nil {
		reserveGoldenContinuationIdentities(reserved, *authority.Parent)
	}
	for _, identity := range identities {
		if _, found := reserved[identity]; found {
			return goldenContinuationError("successor identity aliases retained terminal authority")
		}
	}
	return nil
}

func reserveGoldenContinuationIdentities(
	reserved map[uuid.UUID]struct{},
	record GoldenContinuationRecord,
) {
	values := []uuid.UUID{
		record.ID, record.CommandID, record.SourceTerminalID, record.SourceParentID,
		record.Assignment.ID, record.Assignment.RevisionID, record.Assignment.AttemptID,
		record.Assignment.EdgeID, record.Assignment.ReservationID, record.Assignment.SnapshotID,
		record.Assignment.TaskID, record.Attempt.ID,
	}
	values = append(values, record.NewIdentityIDs...)
	values = append(values, record.ResolvedParticipantIDs...)
	values = append(values, record.UnresolvedParticipantIDs...)
	for _, private := range record.Assignment.Private {
		values = append(values, private.ID, private.ParticipantID, private.SnapshotID)
	}
	for _, attempt := range record.Group.Attempts {
		values = append(values, attempt.ID)
		values = append(values, attempt.ParticipantIDs...)
		if attempt.PreviousAttemptID != nil {
			values = append(values, *attempt.PreviousAttemptID)
		}
	}
	reserveGoldenPositionLedgerIdentities(reserved, record.Positions)
	for _, value := range values {
		if value != uuid.Nil {
			reserved[value] = struct{}{}
		}
	}
}

func reserveGoldenAttemptCommitIdentities(
	reserved map[uuid.UUID]struct{},
	record GoldenAttemptCommitRecord,
) {
	values := []uuid.UUID{
		record.ID, record.CommandID,
		record.Scope.State.TournamentID, record.Scope.State.GroupID, record.Scope.State.GroupRevisionID.UUID(),
		record.Scope.AttemptID, record.Scope.WaveID, record.Scope.AssignmentID,
		record.Scope.SnapshotID, record.Scope.TaskID,
		record.ExpectedSubmissions.RevisionID, record.ExpectedPositions.RevisionID,
		record.SwissPoints.RevisionID, record.Attempt.ID, record.Wave.ID,
		record.ActiveExecution.RevisionID, record.ActiveExecution.AttemptID, record.ActiveExecution.WaveID,
		record.ActiveExecution.WaveRevisionID.UUID(), record.ActiveExecution.Window.WindowID,
		record.ActiveExecution.Window.RevisionID, record.ActiveExecution.Window.ReadinessRevisionID,
		record.ActiveExecution.Window.PresenceRevisionID, record.ActiveExecution.MembershipID,
		record.ActiveExecution.MembershipRevisionID, record.ActiveExecution.AssignmentID,
		record.ActiveExecution.AssignmentRevisionID, record.ActiveExecution.Source.RevisionID,
		record.ActiveExecution.Source.Membership.RevisionID, record.ActiveExecution.Source.Plan.PlanID,
		record.ActiveExecution.Source.Plan.RevisionID,
		record.Assignment.ID, record.Assignment.RevisionID, record.Assignment.AttemptID,
		record.Assignment.WaveID, record.Assignment.MembershipID, record.Assignment.Plan.PlanID,
		record.Assignment.Plan.RevisionID, record.Assignment.EdgeID, record.Assignment.ReservationID,
		record.Assignment.SnapshotID, record.Assignment.TaskID,
	}
	if record.ActiveExecution.Source.Membership.PreviousRevisionID != nil {
		values = append(values, *record.ActiveExecution.Source.Membership.PreviousRevisionID)
	}
	for _, member := range record.Group.Members {
		values = append(values, member.ParticipantID)
	}
	for _, private := range record.Assignment.Private {
		values = append(values, private.ID, private.ParticipantID, private.SnapshotID)
	}
	for _, attempt := range record.Group.Attempts {
		values = append(values, attempt.ID)
		values = append(values, attempt.ParticipantIDs...)
		if attempt.PreviousAttemptID != nil {
			values = append(values, *attempt.PreviousAttemptID)
		}
	}
	for _, value := range values {
		if value != uuid.Nil {
			reserved[value] = struct{}{}
		}
	}
}

func goldenContinuationTerminalGroupMatchesState(
	state domain.ArenaGoldenGroupState,
	terminal GoldenAttemptCommitRecord,
) bool {
	group := terminal.Group
	stableGroup := group.ID == state.ID && group.TournamentID == state.TournamentID &&
		group.RevisionID == state.RevisionID &&
		group.SourceProjectionRevisionID == state.SourceProjectionRevisionID &&
		group.PositionFrom == state.PositionFrom && group.PositionTo == state.PositionTo &&
		reflect.DeepEqual(group.Members, state.Members)
	if !stableGroup || !group.ParticipationEstablished || !terminal.ActiveExecution.Started ||
		len(group.Attempts) != len(state.Attempts)+1 {
		return false
	}
	for index := range state.Attempts {
		if !reflect.DeepEqual(group.Attempts[index], state.Attempts[index]) {
			return false
		}
	}
	return terminal.ActiveExecution.Source.Scope == terminal.Scope.State &&
		reflect.DeepEqual(group.Attempts[len(group.Attempts)-1], terminal.Attempt)
}

func goldenContinuationTerminalMatchesParent(
	parent GoldenContinuationRecord,
	terminal GoldenAttemptCommitRecord,
	command GoldenContinuationCommand,
) bool {
	if !goldenContinuationParentMatchesCommand(parent, command) ||
		!goldenContinuationTerminalPositionsMatchParent(parent, terminal) {
		return false
	}
	if !goldenContinuationTerminalScopeMatchesParent(parent, terminal) ||
		!goldenContinuationTerminalExecutionMatchesParent(parent, terminal) ||
		!goldenContinuationTerminalAssignmentMatchesParent(terminal.Assignment, parent) {
		return false
	}
	if !goldenContinuationTerminalGroupTransition(parent.Group, terminal.Group, parent.Attempt, terminal.Attempt) {
		return false
	}
	return terminal.Positions.Expectation().Equal(command.ExpectedPositions)
}

func goldenContinuationParentMatchesCommand(
	parent GoldenContinuationRecord,
	command GoldenContinuationCommand,
) bool {
	return parent.ID == command.ExpectedParentID && parent.PayloadDigest == command.ExpectedParentDigest &&
		parent.Scope == command.Scope && parent.ExpectedState.Equal(command.ExpectedState) &&
		parent.ExpectedPlan == command.ExpectedPlan && parent.SwissPoints == command.ExpectedSwissPoints
}

func goldenContinuationTerminalPositionsMatchParent(
	parent GoldenContinuationRecord,
	terminal GoldenAttemptCommitRecord,
) bool {
	return parent.Positions.Validate() == nil && terminal.SwissPoints == parent.SwissPoints &&
		terminal.ExpectedPositions.Equal(parent.Positions.Expectation()) &&
		reflect.DeepEqual(terminal.PriorPositions.Snapshot(), parent.Positions.Snapshot())
}

func goldenContinuationTerminalScopeMatchesParent(
	parent GoldenContinuationRecord,
	terminal GoldenAttemptCommitRecord,
) bool {
	return terminal.Scope.State == parent.Scope && terminal.Scope.AttemptID == parent.Attempt.ID &&
		terminal.Scope.AssignmentID == parent.Assignment.ID &&
		terminal.Scope.SnapshotID == parent.Assignment.SnapshotID &&
		terminal.Scope.TaskID == parent.Assignment.TaskID
}

func goldenContinuationTerminalExecutionMatchesParent(
	parent GoldenContinuationRecord,
	terminal GoldenAttemptCommitRecord,
) bool {
	return terminal.ActiveExecution.Scope == parent.Scope &&
		terminal.ActiveExecution.Source.Equal(parent.ExpectedState) &&
		terminal.ActiveExecution.AttemptID == parent.Attempt.ID &&
		terminal.ActiveExecution.AssignmentID == parent.Assignment.ID &&
		terminal.ActiveExecution.AssignmentRevisionID == parent.Assignment.RevisionID
}

func goldenContinuationTerminalAssignmentMatchesParent(
	assignment GoldenAttemptAssignmentEvidence,
	parent GoldenContinuationRecord,
) bool {
	return assignment.ID == parent.Assignment.ID && assignment.RevisionID == parent.Assignment.RevisionID &&
		assignment.Scope == parent.Scope && assignment.AttemptID == parent.Attempt.ID &&
		assignment.Plan == parent.ExpectedPlan && assignment.EdgeID == parent.Assignment.EdgeID &&
		assignment.ReservationID == parent.Assignment.ReservationID &&
		assignment.SnapshotID == parent.Assignment.SnapshotID && assignment.TaskID == parent.Assignment.TaskID &&
		assignment.ContentDigest == parent.Assignment.ContentDigest &&
		reflect.DeepEqual(assignment.Private, parent.Assignment.Private)
}

func goldenContinuationTerminalGroupTransition(
	plannedGroup domain.ArenaGoldenGroupState,
	terminalGroup domain.ArenaGoldenGroupState,
	plannedAttempt domain.ArenaGoldenAttempt,
	terminalAttempt domain.ArenaGoldenAttempt,
) bool {
	if !goldenContinuationTerminalAttemptsMatch(
		plannedGroup,
		terminalGroup,
		plannedAttempt,
		terminalAttempt,
	) {
		return false
	}
	if !goldenContinuationGroupsAreStable(plannedGroup, terminalGroup) {
		return false
	}
	if !goldenContinuationAttemptPrefixesEqual(plannedGroup.Attempts, terminalGroup.Attempts) {
		return false
	}
	wantTerminalAttempt := cloneGoldenAttempt(plannedAttempt)
	wantTerminalAttempt.State = domain.ArenaGoldenAttemptStateCompleted
	wantTerminalAttempt.StartedAt = cloneTimePointer(terminalAttempt.StartedAt)
	wantTerminalAttempt.FinishedAt = cloneTimePointer(terminalAttempt.FinishedAt)
	return reflect.DeepEqual(wantTerminalAttempt, terminalAttempt)
}

func goldenContinuationTerminalAttemptsMatch(
	plannedGroup domain.ArenaGoldenGroupState,
	terminalGroup domain.ArenaGoldenGroupState,
	plannedAttempt domain.ArenaGoldenAttempt,
	terminalAttempt domain.ArenaGoldenAttempt,
) bool {
	return plannedAttempt.State == domain.ArenaGoldenAttemptStatePlanned && plannedAttempt.StartedAt == nil &&
		plannedAttempt.FinishedAt == nil && terminalAttempt.State == domain.ArenaGoldenAttemptStateCompleted &&
		terminalAttempt.StartedAt != nil && terminalAttempt.FinishedAt != nil &&
		len(plannedGroup.Attempts) > 0 && len(terminalGroup.Attempts) == len(plannedGroup.Attempts) &&
		reflect.DeepEqual(plannedGroup.Attempts[len(plannedGroup.Attempts)-1], plannedAttempt) &&
		reflect.DeepEqual(terminalGroup.Attempts[len(terminalGroup.Attempts)-1], terminalAttempt)
}

func goldenContinuationGroupsAreStable(
	plannedGroup domain.ArenaGoldenGroupState,
	terminalGroup domain.ArenaGoldenGroupState,
) bool {
	return plannedGroup.ID == terminalGroup.ID && plannedGroup.TournamentID == terminalGroup.TournamentID &&
		plannedGroup.RevisionID == terminalGroup.RevisionID &&
		plannedGroup.SourceProjectionRevisionID == terminalGroup.SourceProjectionRevisionID &&
		plannedGroup.ParticipationEstablished == terminalGroup.ParticipationEstablished &&
		plannedGroup.PositionFrom == terminalGroup.PositionFrom && plannedGroup.PositionTo == terminalGroup.PositionTo &&
		reflect.DeepEqual(plannedGroup.Members, terminalGroup.Members)
}

func goldenContinuationAttemptPrefixesEqual(
	plannedAttempts []domain.ArenaGoldenAttempt,
	terminalAttempts []domain.ArenaGoldenAttempt,
) bool {
	for index := 0; index < len(plannedAttempts)-1; index++ {
		if !reflect.DeepEqual(plannedAttempts[index], terminalAttempts[index]) {
			return false
		}
	}
	return true
}

func validateGoldenContinuationAuthority(
	authority GoldenContinuationAuthority,
	command GoldenContinuationCommand,
	createdAt time.Time,
) error {
	if !validGoldenContinuationAuthorityDocuments(authority, command.Scope) {
		return domain.ErrInternal
	}
	if !goldenContinuationHeadsMatch(authority, command) {
		return ErrGoldenContinuationAuthorityConflict
	}
	if !goldenContinuationTerminalMatches(authority, command, createdAt) {
		return ErrGoldenContinuationAuthorityConflict
	}
	if !goldenContinuationPlanMatches(authority.Plan, authority.State.ExactPlan, command.ExpectedPlan) {
		return ErrGoldenContinuationAuthorityConflict
	}
	if err := validateGoldenContinuationNewIdentities(authority, command); err != nil {
		return err
	}
	if !goldenContinuationLineageMatches(authority, command) {
		return ErrGoldenContinuationAuthorityConflict
	}
	resolved, unresolved := goldenContinuationParticipants(authority.Terminal)
	if len(unresolved) < 2 {
		return ErrGoldenContinuationFallbackRequired
	}
	remaining, validRemaining := goldenContinuationRemainingPositions(authority.Terminal)
	if !validRemaining {
		return goldenContinuationError("invalid remaining position interval")
	}
	if len(remaining) < len(unresolved) {
		return goldenContinuationError("remaining interval cannot contain unresolved members")
	}
	edge, found := goldenContinuationEdge(authority.Plan, authority.Terminal)
	if !found {
		return ErrGoldenContinuationReservesExhausted
	}
	if err := validateGoldenContinuationPrivate(command.PrivateAssignments, unresolved, edge); err != nil {
		return err
	}
	if !goldenContinuationSolversRemainActive(authority.Terminal.Group.Members, resolved) {
		return goldenContinuationError("a committed solver was converted to an exclusion")
	}
	return nil
}

func validGoldenContinuationAuthorityDocuments(authority GoldenContinuationAuthority, scope GoldenStateScope) bool {
	validParent := authority.Parent == nil || authority.Parent.Validate() == nil
	return authority.Scope == scope && validParent && authority.State.Validate() == nil && authority.Plan.Validate() == nil &&
		authority.Terminal.Validate() == nil && authority.Positions.Validate() == nil &&
		authority.SwissPoints.Validate() == nil
}

func goldenContinuationHeadsMatch(authority GoldenContinuationAuthority, command GoldenContinuationCommand) bool {
	return authority.Terminal.ID == command.ExpectedTerminalID &&
		authority.Terminal.PayloadDigest == command.ExpectedTerminalDigest &&
		authority.State.Expectation().Equal(command.ExpectedState) && authority.State.Plan == command.ExpectedPlan &&
		authority.Positions.Expectation().Equal(command.ExpectedPositions) &&
		authority.SwissPoints == command.ExpectedSwissPoints &&
		goldenContinuationParentHeadMatches(authority.Parent, command)
}

func goldenContinuationParentHeadMatches(
	parent *GoldenContinuationRecord,
	command GoldenContinuationCommand,
) bool {
	if command.ExpectedParentID == uuid.Nil {
		return parent == nil
	}
	return parent != nil && parent.ID == command.ExpectedParentID &&
		parent.PayloadDigest == command.ExpectedParentDigest
}

func goldenContinuationLineageMatches(
	authority GoldenContinuationAuthority,
	command GoldenContinuationCommand,
) bool {
	if authority.Parent == nil {
		return goldenContinuationTerminalGroupMatchesState(authority.State.Group, authority.Terminal) &&
			goldenTerminalAssignmentMatchesPlan(authority.Plan, authority.Terminal.Attempt.AttemptNo, authority.Terminal.Assignment)
	}
	return goldenContinuationParentMatchesState(authority.State.Group, *authority.Parent) &&
		goldenContinuationReserveAssignmentMatchesPlan(authority.Plan, authority.Parent.Assignment) &&
		goldenTerminalAssignmentMatchesPlan(
			authority.Plan,
			authority.Terminal.Attempt.AttemptNo,
			authority.Terminal.Assignment,
		) && goldenContinuationTerminalMatchesParent(*authority.Parent, authority.Terminal, command)
}

func goldenContinuationParentMatchesState(
	state domain.ArenaGoldenGroupState,
	parent GoldenContinuationRecord,
) bool {
	group := parent.Group
	if !goldenContinuationParentGroupMatchesState(group, state) || !group.ParticipationEstablished ||
		len(group.Attempts) < len(state.Attempts)+2 {
		return false
	}
	if !goldenContinuationAttemptPrefixMatchesState(group.Attempts, state.Attempts) {
		return false
	}
	suffix := group.Attempts[len(state.Attempts):]
	completed := suffix[:len(suffix)-1]
	planned := suffix[len(suffix)-1]
	if !reflect.DeepEqual(planned, parent.Attempt) || len(parent.Positions.Attempts) < len(completed) {
		return false
	}

	expectedAttemptNo, previousAttemptID := goldenContinuationNextAttempt(state.Attempts)
	positionOffset := len(parent.Positions.Attempts) - len(completed)
	return goldenContinuationParentSuffixMatches(
		suffix,
		parent.Positions.Attempts[positionOffset:],
		expectedAttemptNo,
		previousAttemptID,
	)
}

func goldenContinuationParentGroupMatchesState(
	group domain.ArenaGoldenGroupState,
	state domain.ArenaGoldenGroupState,
) bool {
	return group.ID == state.ID && group.TournamentID == state.TournamentID &&
		group.RevisionID == state.RevisionID &&
		group.SourceProjectionRevisionID == state.SourceProjectionRevisionID &&
		group.PositionFrom == state.PositionFrom && group.PositionTo == state.PositionTo &&
		reflect.DeepEqual(group.Members, state.Members)
}

func goldenContinuationAttemptPrefixMatchesState(
	groupAttempts []domain.ArenaGoldenAttempt,
	stateAttempts []domain.ArenaGoldenAttempt,
) bool {
	for index := range stateAttempts {
		if !reflect.DeepEqual(groupAttempts[index], stateAttempts[index]) {
			return false
		}
	}
	return true
}

func goldenContinuationNextAttempt(attempts []domain.ArenaGoldenAttempt) (int, uuid.UUID) {
	if len(attempts) == 0 {
		return 1, uuid.Nil
	}
	previous := attempts[len(attempts)-1]
	return previous.AttemptNo + 1, previous.ID
}

func goldenContinuationParentSuffixMatches(
	suffix []domain.ArenaGoldenAttempt,
	positions []GoldenAttemptOrderingEvidence,
	expectedAttemptNo int,
	previousAttemptID uuid.UUID,
) bool {
	for index, attempt := range suffix {
		if !goldenContinuationAttemptFollows(attempt, expectedAttemptNo, previousAttemptID) {
			return false
		}
		lastAttempt := index == len(suffix)-1
		if lastAttempt && !goldenContinuationPlannedAttemptIsValid(attempt) {
			return false
		}
		if !lastAttempt && !goldenContinuationCompletedAttemptMatchesPosition(attempt, positions[index]) {
			return false
		}
		previousAttemptID = attempt.ID
		expectedAttemptNo++
	}
	return true
}

func goldenContinuationAttemptFollows(
	attempt domain.ArenaGoldenAttempt,
	expectedAttemptNo int,
	previousAttemptID uuid.UUID,
) bool {
	return attempt.AttemptNo == expectedAttemptNo &&
		(expectedAttemptNo != 1 || attempt.PreviousAttemptID == nil) &&
		(expectedAttemptNo == 1 ||
			(attempt.PreviousAttemptID != nil && *attempt.PreviousAttemptID == previousAttemptID))
}

func goldenContinuationPlannedAttemptIsValid(attempt domain.ArenaGoldenAttempt) bool {
	return attempt.State == domain.ArenaGoldenAttemptStatePlanned &&
		attempt.StartedAt == nil && attempt.FinishedAt == nil
}

func goldenContinuationCompletedAttemptMatchesPosition(
	attempt domain.ArenaGoldenAttempt,
	position GoldenAttemptOrderingEvidence,
) bool {
	return attempt.State == domain.ArenaGoldenAttemptStateCompleted && attempt.StartedAt != nil &&
		attempt.FinishedAt != nil && !attempt.FinishedAt.Before(*attempt.StartedAt) &&
		position.AttemptID == attempt.ID && position.AttemptNo == attempt.AttemptNo
}

func goldenTerminalAssignmentMatchesPlan(
	plan GoldenExactPlan,
	attemptNo int,
	assignment GoldenAttemptAssignmentEvidence,
) bool {
	edge, found := goldenPlanEdge(plan, assignment.Scope.GroupID, assignment.Scope.GroupRevisionID, attemptNo-1)
	return found && assignment.Plan.PlanID == plan.PlanID && assignment.Plan.RevisionID == plan.PlanRevisionID &&
		assignment.EdgeID == edge.ID && assignment.ReservationID == edge.ReservationID &&
		assignment.SnapshotID == edge.Snapshot.SnapshotID && assignment.TaskID == edge.Snapshot.TaskID &&
		assignment.ContentDigest == edge.ContentDigest
}

func goldenContinuationReserveAssignmentMatchesPlan(
	plan GoldenExactPlan,
	assignment GoldenReserveAttemptAssignment,
) bool {
	edge, found := goldenPlanEdge(
		plan,
		assignment.Scope.GroupID,
		assignment.Scope.GroupRevisionID,
		assignment.EdgePosition-1,
	)
	return found && assignment.EdgeID == edge.ID && assignment.EdgePosition == edge.Position &&
		assignment.ReservationID == edge.ReservationID && assignment.SnapshotID == edge.Snapshot.SnapshotID &&
		assignment.TaskID == edge.Snapshot.TaskID && assignment.ContentDigest == edge.ContentDigest
}

func goldenPlanEdge(
	plan GoldenExactPlan,
	groupID uuid.UUID,
	groupRevisionID domain.ArenaDerivedRevisionID,
	edgeIndex int,
) (GoldenExactPlanEdge, bool) {
	if edgeIndex < 0 {
		return GoldenExactPlanEdge{}, false
	}
	for _, group := range plan.Groups {
		if group.GroupID != groupID || group.GroupRevisionID != groupRevisionID || edgeIndex >= len(group.Edges) {
			continue
		}
		return group.Edges[edgeIndex], true
	}
	return GoldenExactPlanEdge{}, false
}

func goldenContinuationTerminalMatches(
	authority GoldenContinuationAuthority,
	command GoldenContinuationCommand,
	createdAt time.Time,
) bool {
	return authority.Terminal.ActiveExecution.Source.Equal(command.ExpectedState) &&
		authority.Terminal.Scope.State == command.Scope &&
		authority.Terminal.Positions.Expectation().Equal(command.ExpectedPositions) &&
		!createdAt.Before(authority.Terminal.FinishedAt)
}

func goldenContinuationPlanMatches(
	plan GoldenExactPlan,
	statePlan GoldenExactPlan,
	binding GoldenPlanStateBinding,
) bool {
	return plan.PlanID == binding.PlanID && plan.PlanRevisionID == binding.RevisionID &&
		reflect.DeepEqual(plan.Snapshot(), statePlan.Snapshot())
}

func goldenContinuationSolversRemainActive(members []domain.ArenaGoldenMember, resolved []uuid.UUID) bool {
	for _, solved := range resolved {
		member, found := goldenStateMember(members, solved)
		if !found || member.Excluded {
			return false
		}
	}
	return true
}

func buildGoldenContinuationRecord(
	authority GoldenContinuationAuthority,
	command GoldenContinuationCommand,
	createdAt time.Time,
) (GoldenContinuationRecord, error) {
	resolved, unresolved := goldenContinuationParticipants(authority.Terminal)
	remaining, validRemaining := goldenContinuationRemainingPositions(authority.Terminal)
	if !validRemaining {
		return GoldenContinuationRecord{}, goldenContinuationError("invalid remaining position interval")
	}
	edge, found := goldenContinuationEdge(authority.Plan, authority.Terminal)
	if !found {
		return GoldenContinuationRecord{}, ErrGoldenContinuationReservesExhausted
	}
	attempt := domain.ArenaGoldenAttempt{
		ID: command.NextAttemptID, GroupID: command.Scope.GroupID, GroupRevisionID: command.Scope.GroupRevisionID,
		AttemptNo:         authority.Terminal.Attempt.AttemptNo + 1,
		PreviousAttemptID: goldenUUID(authority.Terminal.Attempt.ID),
		State:             domain.ArenaGoldenAttemptStatePlanned, ParticipantIDs: append([]uuid.UUID(nil), unresolved...),
	}
	group := cloneGoldenStateGroup(authority.Terminal.Group)
	group.Attempts = append(group.Attempts, cloneGoldenAttempt(attempt))
	if _, err := domain.NewArenaGoldenGroup(group); err != nil {
		return GoldenContinuationRecord{}, goldenContinuationError("successor group is invalid")
	}
	private := make([]GoldenPrivateAssignment, len(command.PrivateAssignments))
	for index, requested := range command.PrivateAssignments {
		private[index] = GoldenPrivateAssignment{
			ID: requested.AssignmentID, ParticipantID: requested.ParticipantID,
			SnapshotID: edge.Snapshot.SnapshotID, ContentDigest: edge.ContentDigest,
		}
	}
	canonicalGoldenPrivateAssignments(private)
	assignment := GoldenReserveAttemptAssignment{
		ID: command.NextAssignmentID, RevisionID: command.NextAssignmentRevisionID,
		Scope: command.Scope, AttemptID: command.NextAttemptID, EdgeID: edge.ID,
		EdgePosition: edge.Position, ReservationID: edge.ReservationID,
		SnapshotID: edge.Snapshot.SnapshotID, TaskID: edge.Snapshot.TaskID,
		ContentDigest: edge.ContentDigest, Private: private,
	}
	payload, err := goldenReserveAssignmentPayload(assignment)
	if err != nil {
		return GoldenContinuationRecord{}, goldenContinuationError("encode reserve assignment")
	}
	assignment.PayloadDigest = sha256.Sum256(payload)
	record := GoldenContinuationRecord{
		ID: command.ContinuationID, CommandID: command.CommandID,
		CommandDigest: goldenContinuationCommandDigest(command), Scope: command.Scope,
		SourceTerminalID: authority.Terminal.ID, SourceTerminalDigest: authority.Terminal.PayloadDigest,
		SourceParentID: command.ExpectedParentID, SourceParentDigest: command.ExpectedParentDigest,
		ExpectedState: cloneGoldenStateExpectation(command.ExpectedState), ExpectedPlan: command.ExpectedPlan,
		ExpectedPositions: command.ExpectedPositions, SwissPoints: command.ExpectedSwissPoints,
		NewIdentityIDs:         goldenContinuationCommandIdentityIDs(command),
		ResolvedParticipantIDs: resolved, UnresolvedParticipantIDs: unresolved,
		RemainingPositions: remaining, Group: group, Attempt: attempt,
		Assignment: assignment, CreatedAt: createdAt, Positions: authority.Positions.Snapshot(),
	}
	payload, err = goldenContinuationPayload(record)
	if err != nil {
		return GoldenContinuationRecord{}, goldenContinuationError("encode continuation")
	}
	record.PayloadDigest = sha256.Sum256(payload)
	if err := record.Validate(); err != nil {
		return GoldenContinuationRecord{}, err
	}
	return record.Snapshot(), nil
}

func validateGoldenContinuationPartition(record GoldenContinuationRecord) error {
	wantResolved := make([]uuid.UUID, len(record.Positions.Positions))
	for index, position := range record.Positions.Positions {
		wantResolved[index] = position.ParticipantID
	}
	canonicalGoldenIDs(wantResolved)
	if !equalGoldenIDs(wantResolved, record.ResolvedParticipantIDs) {
		return goldenContinuationError("committed solver union changed")
	}
	resolved := goldenIDState(record.ResolvedParticipantIDs)
	wantUnresolved := make([]uuid.UUID, 0, len(record.Group.Members))
	for _, member := range record.Group.Members {
		if !member.Excluded && !resolved[member.ParticipantID] {
			wantUnresolved = append(wantUnresolved, member.ParticipantID)
		}
	}
	canonicalGoldenIDs(wantUnresolved)
	if !equalGoldenIDs(wantUnresolved, record.UnresolvedParticipantIDs) {
		return goldenContinuationError("exclusions were not applied before successor membership")
	}
	wantRemaining, validRemaining := goldenRemainingPositionRange(
		record.Positions.PositionFrom,
		record.Positions.PositionTo,
		len(record.Positions.Positions),
	)
	if !validRemaining {
		return goldenContinuationError("remaining position interval is invalid")
	}
	if len(wantRemaining) != len(record.RemainingPositions) {
		return goldenContinuationError("remaining position interval changed")
	}
	for index := range wantRemaining {
		if wantRemaining[index] != record.RemainingPositions[index] {
			return goldenContinuationError("remaining position interval changed")
		}
	}
	return nil
}

func goldenContinuationParticipants(
	terminal GoldenAttemptCommitRecord,
) ([]uuid.UUID, []uuid.UUID) {
	resolved := make([]uuid.UUID, len(terminal.Positions.Positions))
	resolvedSet := make(map[uuid.UUID]struct{}, len(resolved))
	for index, position := range terminal.Positions.Positions {
		resolved[index] = position.ParticipantID
		resolvedSet[position.ParticipantID] = struct{}{}
	}
	canonicalGoldenIDs(resolved)
	unresolved := make([]uuid.UUID, 0, len(terminal.Group.Members))
	for _, member := range terminal.Group.Members {
		if _, solved := resolvedSet[member.ParticipantID]; !member.Excluded && !solved {
			unresolved = append(unresolved, member.ParticipantID)
		}
	}
	canonicalGoldenIDs(unresolved)
	return resolved, unresolved
}

func goldenContinuationRemainingPositions(terminal GoldenAttemptCommitRecord) ([]int, bool) {
	return goldenRemainingPositionRange(
		terminal.Group.PositionFrom,
		terminal.Group.PositionTo,
		len(terminal.Positions.Positions),
	)
}

func goldenRemainingPositionRange(positionFrom, positionTo, committed int) ([]int, bool) {
	if positionFrom < 1 || positionTo < positionFrom || positionTo >= int(^uint(0)>>1) {
		return nil, false
	}
	capacity := positionTo - positionFrom + 1
	if committed < 0 || committed > capacity {
		return nil, false
	}
	remaining := make([]int, capacity-committed)
	remainingFrom := positionFrom + committed
	for index := range remaining {
		remaining[index] = remainingFrom + index
	}
	return remaining, true
}

func goldenContinuationEdge(
	plan GoldenExactPlan,
	terminal GoldenAttemptCommitRecord,
) (GoldenExactPlanEdge, bool) {
	for _, group := range plan.Groups {
		if group.GroupID != terminal.Scope.State.GroupID ||
			group.GroupRevisionID != terminal.Scope.State.GroupRevisionID {
			continue
		}
		index := terminal.Attempt.AttemptNo
		if index < 0 || index >= len(group.Edges) {
			return GoldenExactPlanEdge{}, false
		}
		return group.Edges[index], true
	}
	return GoldenExactPlanEdge{}, false
}

func validateGoldenContinuationPrivate(
	requested []GoldenPrivateAssignmentCommand,
	unresolved []uuid.UUID,
	edge GoldenExactPlanEdge,
) error {
	if len(requested) != len(unresolved) {
		return goldenContinuationError("private assignments do not cover unresolved members")
	}
	participants := make([]uuid.UUID, len(requested))
	assignments := make([]uuid.UUID, len(requested))
	for index, value := range requested {
		participants[index] = value.ParticipantID
		assignments[index] = value.AssignmentID
	}
	canonicalGoldenIDs(participants)
	canonicalGoldenIDs(assignments)
	if !equalGoldenIDs(participants, unresolved) || !goldenIDsAreCanonical(assignments) ||
		edge.ID == uuid.Nil || edge.ReservationID == uuid.Nil || edge.Snapshot.SnapshotID == uuid.Nil ||
		edge.Snapshot.TaskID == uuid.Nil || edge.ContentDigest == [sha256.Size]byte{} {
		return goldenContinuationError("invalid exact reserve assignment")
	}
	return nil
}

func canonicalGoldenPrivateAssignments(values []GoldenPrivateAssignment) {
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j].ParticipantID.String() < values[i].ParticipantID.String() {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}

func reconcileGoldenContinuation(
	record GoldenContinuationRecord,
	command GoldenContinuationCommand,
) (*GoldenContinuationRecord, error) {
	if record.Validate() != nil || !goldenContinuationRecordMatchesCommand(record, command) {
		return nil, ErrGoldenContinuationCommandReuse
	}
	clone := record.Snapshot()
	return &clone, nil
}

func goldenContinuationRecordMatchesCommand(
	record GoldenContinuationRecord,
	command GoldenContinuationCommand,
) bool {
	return goldenContinuationRecordMatchesSource(record, command) &&
		goldenContinuationRecordMatchesSuccessor(record, command) &&
		goldenContinuationPrivateMatchesCommand(record.Assignment.Private, command.PrivateAssignments)
}

func goldenContinuationRecordMatchesSource(
	record GoldenContinuationRecord,
	command GoldenContinuationCommand,
) bool {
	return record.CommandID == command.CommandID && record.ID == command.ContinuationID &&
		record.Scope == command.Scope && record.CommandDigest == goldenContinuationCommandDigest(command) &&
		record.SourceTerminalID == command.ExpectedTerminalID &&
		record.SourceTerminalDigest == command.ExpectedTerminalDigest &&
		record.SourceParentID == command.ExpectedParentID &&
		record.SourceParentDigest == command.ExpectedParentDigest &&
		record.ExpectedState.Equal(command.ExpectedState) && record.ExpectedPlan == command.ExpectedPlan &&
		record.ExpectedPositions.Equal(command.ExpectedPositions) && record.SwissPoints == command.ExpectedSwissPoints
}

func goldenContinuationRecordMatchesSuccessor(
	record GoldenContinuationRecord,
	command GoldenContinuationCommand,
) bool {
	return record.Attempt.ID == command.NextAttemptID && record.Assignment.ID == command.NextAssignmentID &&
		record.Assignment.RevisionID == command.NextAssignmentRevisionID &&
		equalGoldenIDs(record.NewIdentityIDs, goldenContinuationCommandIdentityIDs(command))
}

func goldenContinuationPrivateMatchesCommand(
	stored []GoldenPrivateAssignment,
	requested []GoldenPrivateAssignmentCommand,
) bool {
	if len(stored) != len(requested) {
		return false
	}
	want := make(map[uuid.UUID]uuid.UUID, len(requested))
	for _, assignment := range requested {
		want[assignment.ParticipantID] = assignment.AssignmentID
	}
	for _, assignment := range stored {
		if want[assignment.ParticipantID] != assignment.ID {
			return false
		}
	}
	return true
}

func goldenReserveAssignmentPayload(assignment GoldenReserveAttemptAssignment) ([]byte, error) {
	return goldenEncode(struct {
		ID            uuid.UUID
		RevisionID    uuid.UUID
		Scope         GoldenStateScope
		AttemptID     uuid.UUID
		EdgeID        uuid.UUID
		EdgePosition  int
		ReservationID uuid.UUID
		SnapshotID    uuid.UUID
		TaskID        uuid.UUID
		ContentDigest [sha256.Size]byte
		Private       []GoldenPrivateAssignment
	}{
		ID: assignment.ID, RevisionID: assignment.RevisionID, Scope: assignment.Scope,
		AttemptID: assignment.AttemptID, EdgeID: assignment.EdgeID, EdgePosition: assignment.EdgePosition,
		ReservationID: assignment.ReservationID, SnapshotID: assignment.SnapshotID,
		TaskID: assignment.TaskID, ContentDigest: assignment.ContentDigest, Private: assignment.Private,
	})
}

func goldenContinuationPayload(record GoldenContinuationRecord) ([]byte, error) {
	return goldenEncode(struct {
		ID                       uuid.UUID
		CommandID                uuid.UUID
		CommandDigest            [sha256.Size]byte
		Scope                    GoldenStateScope
		SourceTerminalID         uuid.UUID
		SourceTerminalDigest     [sha256.Size]byte
		SourceParentID           uuid.UUID
		SourceParentDigest       [sha256.Size]byte
		ExpectedState            GoldenStateExpectation
		ExpectedPlan             GoldenPlanStateBinding
		ExpectedPositions        GoldenPositionLedgerExpectation
		SwissPoints              GoldenSwissPointLedgerSentinel
		NewIdentityIDs           []uuid.UUID
		ResolvedParticipantIDs   []uuid.UUID
		UnresolvedParticipantIDs []uuid.UUID
		RemainingPositions       []int
		Group                    domain.ArenaGoldenGroupState
		Attempt                  domain.ArenaGoldenAttempt
		Assignment               GoldenReserveAttemptAssignment
		CreatedAt                time.Time
		Positions                GoldenPositionLedger
	}{
		ID: record.ID, CommandID: record.CommandID, CommandDigest: record.CommandDigest,
		Scope: record.Scope, SourceTerminalID: record.SourceTerminalID,
		SourceTerminalDigest: record.SourceTerminalDigest, SourceParentID: record.SourceParentID,
		SourceParentDigest: record.SourceParentDigest, ExpectedState: record.ExpectedState,
		ExpectedPlan: record.ExpectedPlan, ExpectedPositions: record.ExpectedPositions,
		SwissPoints: record.SwissPoints, NewIdentityIDs: record.NewIdentityIDs,
		ResolvedParticipantIDs:   record.ResolvedParticipantIDs,
		UnresolvedParticipantIDs: record.UnresolvedParticipantIDs,
		RemainingPositions:       record.RemainingPositions, Group: record.Group,
		Attempt: record.Attempt, Assignment: record.Assignment, CreatedAt: record.CreatedAt,
		Positions: record.Positions,
	})
}

func goldenContinuationCommandDigest(command GoldenContinuationCommand) [sha256.Size]byte {
	payload, _ := goldenEncode(command)
	return sha256.Sum256(payload)
}

func goldenContinuationError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidGoldenContinuation, message)
}
