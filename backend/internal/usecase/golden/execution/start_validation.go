package execution

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func validateStartRecord(execution GoldenWaveExecution) error {
	start := execution.Start
	if start == nil {
		return nil
	}
	if !startPointersPresent(execution, *start) || !startAnchorsMatch(execution, *start) ||
		!startTimeLineageMatches(execution, *start) || !startWindowEvidenceMatches(execution, *start) {
		return executionError("invalid retained Golden start")
	}
	if err := validateStartReceipt(execution, *start); err != nil {
		return err
	}
	if startedReceiptCount(execution.Receipts) != 1 {
		return executionError("Golden start receipt is not unique")
	}
	return validateStartedAssignments(execution, *start)
}

func startPointersPresent(execution GoldenWaveExecution, start GoldenStartRecord) bool {
	return len(execution.Receipts) > 0 && execution.Wave.StartedAt != nil &&
		execution.Wave.ReadyWindow != nil && execution.Wave.ReadyWindow.ConsumedAt != nil &&
		execution.Attempt.StartedAt != nil && execution.PreviousRevisionID != nil &&
		execution.Window.PreviousRevisionID != nil && start.CommandID != uuid.Nil
}

func startAnchorsMatch(execution GoldenWaveExecution, start GoldenStartRecord) bool {
	return start.Scope == execution.Scope && start.AttemptID == execution.Attempt.ID &&
		start.WaveID == execution.Wave.ID && start.WindowID == execution.Window.ID &&
		start.ExpectedState.Equal(execution.Source) && start.ResultExecutionRevisionID == execution.RevisionID &&
		start.ResultWindowRevisionID == execution.Window.RevisionID
}

func startTimeLineageMatches(execution GoldenWaveExecution, start GoldenStartRecord) bool {
	return domain.IsValidServerTime(start.StartedAt) && domain.IsValidServerTime(start.Deadline) &&
		!start.StartedAt.Before(execution.OpenedAt) && !start.StartedAt.After(execution.Deadline) &&
		execution.Wave.StartedAt.Equal(start.StartedAt) &&
		execution.Wave.ReadyWindow.ConsumedAt.Equal(start.StartedAt) &&
		execution.Attempt.StartedAt.Equal(start.StartedAt) &&
		execution.Revision == start.ExpectedExecution.Revision+1 &&
		*execution.PreviousRevisionID == start.ExpectedExecution.RevisionID &&
		execution.Window.Revision == start.ExpectedExecution.Window.Revision+1 &&
		*execution.Window.PreviousRevisionID == start.ExpectedExecution.Window.RevisionID
}

func startWindowEvidenceMatches(execution GoldenWaveExecution, start GoldenStartRecord) bool {
	expectedWindow := start.ExpectedExecution.Window
	return execution.Window.ReadinessRevisionID == expectedWindow.ReadinessRevisionID &&
		execution.Window.ReadinessRevision == expectedWindow.ReadinessRevision &&
		execution.Window.ReadinessDigest == expectedWindow.ReadinessDigest &&
		execution.Window.PresenceRevisionID == expectedWindow.PresenceRevisionID &&
		execution.Window.PresenceRevision == expectedWindow.PresenceRevision &&
		execution.Window.PresenceDigest == expectedWindow.PresenceDigest &&
		start.Deadline.Equal(start.StartedAt.Add(timeLimit(execution))) &&
		start.Authority.Identity.Validate() == nil &&
		start.Authority.Identity.TournamentID == execution.Scope.TournamentID &&
		start.Authority.LeaseRevision >= 1 && start.Authority.LeaseDigest != [32]byte{} &&
		start.PayloadDigest == GoldenStartRecordDigest(start) && len(start.Assignments) == len(execution.Assignment.Private)
}

func timeLimit(execution GoldenWaveExecution) time.Duration {
	return time.Duration(execution.Assignment.Snapshot.TimeLimit) * time.Second
}

func validateStartReceipt(execution GoldenWaveExecution, start GoldenStartRecord) error {
	lastReceipt := execution.Receipts[len(execution.Receipts)-1]
	if lastReceipt.Kind != GoldenWaveCommandStarted || lastReceipt.CommandID != start.CommandID ||
		lastReceipt.ParticipantID != uuid.Nil || !lastReceipt.OccurredAt.Equal(start.StartedAt) ||
		!start.ExpectedExecution.Equal(lastReceipt.ExpectedValue()) {
		return executionError("Golden start expectation changed")
	}
	return nil
}

func startedReceiptCount(receipts []GoldenWaveCommandReceipt) int {
	count := 0
	for _, receipt := range receipts {
		if receipt.Kind == GoldenWaveCommandStarted {
			count++
		}
	}
	return count
}

func validateStartedAssignments(execution GoldenWaveExecution, start GoldenStartRecord) error {
	for index, assignment := range start.Assignments {
		private := execution.Assignment.Private[index]
		if assignment.AssignmentID != private.ID || assignment.ParticipantID != private.ParticipantID ||
			assignment.SnapshotID != private.SnapshotID || assignment.ContentDigest != private.ContentDigest ||
			!assignment.StartedAt.Equal(start.StartedAt) || !assignment.Deadline.Equal(start.Deadline) ||
			assignment.Authority != start.Authority.Identity || assignment.AuthorityDigest != start.Authority.LeaseDigest ||
			!assignment.DeliveryEnabled {
			return executionError("started private assignment changed")
		}
	}
	return nil
}
