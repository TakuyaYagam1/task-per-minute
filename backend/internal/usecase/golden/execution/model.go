package execution

import (
	"github.com/google/uuid"

	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
)

func (e GoldenWaveExecution) Expectation() GoldenWaveExecutionExpectation {
	return GoldenWaveExecutionExpectation{
		Scope: e.Scope, Source: goldenstate.CloneExpectation(e.Source), RevisionID: e.RevisionID,
		Revision: e.Revision, PayloadDigest: e.PayloadDigest, AttemptID: e.Attempt.ID,
		WaveID: e.Wave.ID, WaveRevisionID: e.Wave.RevisionID, Window: e.Window.Expectation(),
		MembershipID: e.Membership.ID, MembershipRevisionID: e.Membership.RevisionID,
		MembershipRevision: e.Membership.Revision, MembershipDigest: e.Membership.PayloadDigest,
		AssignmentID: e.Assignment.ID, AssignmentRevisionID: e.Assignment.RevisionID,
		AssignmentRevision: e.Assignment.Revision, AssignmentDigest: e.Assignment.PayloadDigest,
		Started: e.Start != nil,
	}
}

func (e GoldenWaveExecution) Snapshot() GoldenWaveExecution {
	clone := e
	clone.Source = goldenstate.CloneExpectation(e.Source)
	clone.PreviousRevisionID = executionCloneUUID(e.PreviousRevisionID)
	clone.Group = goldenstate.CloneGroup(e.Group)
	clone.Attempt = goldenstate.CloneAttempt(e.Attempt)
	clone.Wave = CloneExecution(e.Wave)
	clone.Membership = cloneMembershipBinding(e.Membership)
	clone.Assignment = cloneAttemptAssignment(e.Assignment)
	clone.Window = goldenstate.CloneReadyWindow(e.Window)
	clone.Receipts = cloneWaveReceipts(e.Receipts)
	clone.Start = cloneStartRecord(e.Start)
	return clone
}

func IdentitySetFromExpectation(expectation GoldenWaveExecutionExpectation) map[uuid.UUID]struct{} {
	values := []uuid.UUID{
		expectation.RevisionID, expectation.AttemptID, expectation.WaveID, expectation.WaveRevisionID.UUID(),
		expectation.Window.WindowID, expectation.Window.RevisionID, expectation.Window.ReadinessRevisionID,
		expectation.Window.PresenceRevisionID, expectation.MembershipID, expectation.MembershipRevisionID,
		expectation.AssignmentID, expectation.AssignmentRevisionID,
	}
	result := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value != uuid.Nil {
			result[value] = struct{}{}
		}
	}
	return result
}

func ExecutionHasReceipt(execution GoldenWaveExecution, expected GoldenWaveCommandReceipt) bool {
	receipt, found := ExecutionReceiptByCommand(execution, expected.CommandID)
	return found && WaveReceiptsEqual(receipt, expected)
}

func ExecutionReceiptByCommand(
	execution GoldenWaveExecution,
	commandID uuid.UUID,
) (GoldenWaveCommandReceipt, bool) {
	for _, receipt := range execution.Receipts {
		if receipt.CommandID == commandID {
			return receipt, true
		}
	}
	return GoldenWaveCommandReceipt{}, false
}

func WaveReceiptsEqual(first, second GoldenWaveCommandReceipt) bool {
	if first.CommandID != second.CommandID || first.Scope != second.Scope || first.Kind != second.Kind ||
		first.CommandDigest != second.CommandDigest || !first.Result.Equal(second.Result) ||
		!first.OccurredAt.Equal(second.OccurredAt) || first.ParticipantID != second.ParticipantID ||
		!executionEqualIDs(first.UnusedIdentityIDs, second.UnusedIdentityIDs) {
		return false
	}
	if first.Expected == nil || second.Expected == nil {
		return first.Expected == nil && second.Expected == nil
	}
	return first.Expected.Equal(*second.Expected)
}

func RetainedIdentitySet(execution GoldenWaveExecution) map[uuid.UUID]struct{} {
	values := []uuid.UUID{
		execution.Scope.TournamentID, execution.Scope.GroupID, execution.Scope.GroupRevisionID.UUID(),
		execution.Source.RevisionID, execution.Source.Membership.RevisionID,
		execution.Source.Plan.PlanID, execution.Source.Plan.RevisionID,
		execution.Source.SourceProjectionRevisionID.UUID(),
		execution.RevisionID, execution.Attempt.ID, execution.Wave.ID, execution.Wave.RevisionID.UUID(),
		execution.Window.ID, execution.Wave.ReadyWindow.RevisionID.UUID(), execution.Window.RevisionID,
		execution.Window.ReadinessRevisionID, execution.Window.PresenceRevisionID,
		execution.Membership.ID, execution.Membership.RevisionID,
		execution.Assignment.ID, execution.Assignment.RevisionID, execution.Assignment.EdgeID,
		execution.Assignment.ReservationID, execution.Assignment.Snapshot.SnapshotID,
		execution.Assignment.Snapshot.TaskID,
	}
	if execution.PreviousRevisionID != nil {
		values = append(values, *execution.PreviousRevisionID)
	}
	if execution.Source.Membership.PreviousRevisionID != nil {
		values = append(values, *execution.Source.Membership.PreviousRevisionID)
	}
	for _, member := range execution.Group.Members {
		values = append(values, member.ParticipantID)
	}
	for _, attempt := range execution.Group.Attempts {
		values = append(values, attempt.ID)
		values = append(values, attempt.ParticipantIDs...)
	}
	for _, private := range execution.Assignment.Private {
		values = append(values, private.ID)
	}
	for _, receipt := range execution.Receipts {
		values = append(values, receipt.CommandID, receipt.Result.RevisionID,
			receipt.Result.Window.RevisionID, receipt.Result.Window.ReadinessRevisionID,
			receipt.Result.Window.PresenceRevisionID)
		values = append(values, receipt.UnusedIdentityIDs...)
		if receipt.Expected != nil {
			values = append(values, receipt.Expected.RevisionID,
				receipt.Expected.Window.RevisionID, receipt.Expected.Window.ReadinessRevisionID,
				receipt.Expected.Window.PresenceRevisionID)
		}
	}
	if execution.Start != nil {
		values = append(values,
			execution.Start.CommandID, execution.Start.ResultExecutionRevisionID,
			execution.Start.ResultWindowRevisionID, execution.Start.Authority.Identity.HolderID,
			execution.Start.Authority.Identity.LeaseID,
		)
		for _, assignment := range execution.Start.Assignments {
			values = append(values, assignment.AssignmentID, assignment.ParticipantID, assignment.SnapshotID)
		}
	}
	result := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value != uuid.Nil {
			result[value] = struct{}{}
		}
	}
	return result
}
