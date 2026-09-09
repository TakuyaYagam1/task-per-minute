package golden

import (
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func goldenContinuationTerminalGroupMatchesState(
	state domain.GoldenGroupState,
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
	plannedGroup domain.GoldenGroupState,
	terminalGroup domain.GoldenGroupState,
	plannedAttempt domain.GoldenAttempt,
	terminalAttempt domain.GoldenAttempt,
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
	wantTerminalAttempt := CloneAttempt(plannedAttempt)
	wantTerminalAttempt.State = domain.GoldenAttemptStateCompleted
	wantTerminalAttempt.StartedAt = continuationCloneTimePointer(terminalAttempt.StartedAt)
	wantTerminalAttempt.FinishedAt = continuationCloneTimePointer(terminalAttempt.FinishedAt)
	return reflect.DeepEqual(wantTerminalAttempt, terminalAttempt)
}

func goldenContinuationTerminalAttemptsMatch(
	plannedGroup domain.GoldenGroupState,
	terminalGroup domain.GoldenGroupState,
	plannedAttempt domain.GoldenAttempt,
	terminalAttempt domain.GoldenAttempt,
) bool {
	return plannedAttempt.State == domain.GoldenAttemptStatePlanned && plannedAttempt.StartedAt == nil &&
		plannedAttempt.FinishedAt == nil && terminalAttempt.State == domain.GoldenAttemptStateCompleted &&
		terminalAttempt.StartedAt != nil && terminalAttempt.FinishedAt != nil &&
		len(plannedGroup.Attempts) > 0 && len(terminalGroup.Attempts) == len(plannedGroup.Attempts) &&
		reflect.DeepEqual(plannedGroup.Attempts[len(plannedGroup.Attempts)-1], plannedAttempt) &&
		reflect.DeepEqual(terminalGroup.Attempts[len(terminalGroup.Attempts)-1], terminalAttempt)
}

func goldenContinuationGroupsAreStable(
	plannedGroup domain.GoldenGroupState,
	terminalGroup domain.GoldenGroupState,
) bool {
	return plannedGroup.ID == terminalGroup.ID && plannedGroup.TournamentID == terminalGroup.TournamentID &&
		plannedGroup.RevisionID == terminalGroup.RevisionID &&
		plannedGroup.SourceProjectionRevisionID == terminalGroup.SourceProjectionRevisionID &&
		plannedGroup.ParticipationEstablished == terminalGroup.ParticipationEstablished &&
		plannedGroup.PositionFrom == terminalGroup.PositionFrom && plannedGroup.PositionTo == terminalGroup.PositionTo &&
		reflect.DeepEqual(plannedGroup.Members, terminalGroup.Members)
}

func goldenContinuationAttemptPrefixesEqual(
	plannedAttempts []domain.GoldenAttempt,
	terminalAttempts []domain.GoldenAttempt,
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
	state domain.GoldenGroupState,
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
	group domain.GoldenGroupState,
	state domain.GoldenGroupState,
) bool {
	return group.ID == state.ID && group.TournamentID == state.TournamentID &&
		group.RevisionID == state.RevisionID &&
		group.SourceProjectionRevisionID == state.SourceProjectionRevisionID &&
		group.PositionFrom == state.PositionFrom && group.PositionTo == state.PositionTo &&
		reflect.DeepEqual(group.Members, state.Members)
}

func goldenContinuationAttemptPrefixMatchesState(
	groupAttempts []domain.GoldenAttempt,
	stateAttempts []domain.GoldenAttempt,
) bool {
	for index := range stateAttempts {
		if !reflect.DeepEqual(groupAttempts[index], stateAttempts[index]) {
			return false
		}
	}
	return true
}

func goldenContinuationNextAttempt(attempts []domain.GoldenAttempt) (int, uuid.UUID) {
	if len(attempts) == 0 {
		return 1, uuid.Nil
	}
	previous := attempts[len(attempts)-1]
	return previous.AttemptNo + 1, previous.ID
}

func goldenContinuationParentSuffixMatches(
	suffix []domain.GoldenAttempt,
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
	attempt domain.GoldenAttempt,
	expectedAttemptNo int,
	previousAttemptID uuid.UUID,
) bool {
	return attempt.AttemptNo == expectedAttemptNo &&
		(expectedAttemptNo != 1 || attempt.PreviousAttemptID == nil) &&
		(expectedAttemptNo == 1 ||
			(attempt.PreviousAttemptID != nil && *attempt.PreviousAttemptID == previousAttemptID))
}

func goldenContinuationPlannedAttemptIsValid(attempt domain.GoldenAttempt) bool {
	return attempt.State == domain.GoldenAttemptStatePlanned &&
		attempt.StartedAt == nil && attempt.FinishedAt == nil
}

func goldenContinuationCompletedAttemptMatchesPosition(
	attempt domain.GoldenAttempt,
	position GoldenAttemptOrderingEvidence,
) bool {
	return attempt.State == domain.GoldenAttemptStateCompleted && attempt.StartedAt != nil &&
		attempt.FinishedAt != nil && !attempt.FinishedAt.Before(*attempt.StartedAt) &&
		position.AttemptID == attempt.ID && position.AttemptNo == attempt.AttemptNo
}

func goldenTerminalAssignmentMatchesPlan(
	plan ExactPlan,
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
	plan ExactPlan,
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
	plan ExactPlan,
	groupID uuid.UUID,
	groupRevisionID domain.DerivedRevisionID,
	edgeIndex int,
) (Edge, bool) {
	if edgeIndex < 0 {
		return Edge{}, false
	}
	for _, group := range plan.Groups {
		if group.GroupID != groupID || group.GroupRevisionID != groupRevisionID || edgeIndex >= len(group.Edges) {
			continue
		}
		return group.Edges[edgeIndex], true
	}
	return Edge{}, false
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
	plan ExactPlan,
	statePlan ExactPlan,
	binding GoldenPlanStateBinding,
) bool {
	return plan.PlanID == binding.PlanID && plan.PlanRevisionID == binding.RevisionID &&
		reflect.DeepEqual(plan.Snapshot(), statePlan.Snapshot())
}

func goldenContinuationSolversRemainActive(members []domain.GoldenMember, resolved []uuid.UUID) bool {
	for _, solved := range resolved {
		member, found := FindMember(members, solved)
		if !found || member.Excluded {
			return false
		}
	}
	return true
}
