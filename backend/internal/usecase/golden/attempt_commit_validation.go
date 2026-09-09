package golden

import (
	"math"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"

	"github.com/google/uuid"
)

func validateGoldenAttemptCommitCommand(command GoldenAttemptCommitCommand) error {
	if !command.Scope.IsValid() || command.CommandID == uuid.Nil || command.CommitID == uuid.Nil ||
		command.NextPositionRevisionID == uuid.Nil || command.ExpectedSwissPoints.Validate() != nil ||
		(command.Reason != GoldenAttemptTerminalAllSolved && command.Reason != GoldenAttemptTerminalDeadline) {
		return goldenAttemptCommitError("invalid command identity or terminal reason")
	}
	if !ValidIdentitySet([]uuid.UUID{command.CommandID, command.CommitID, command.NextPositionRevisionID}) {
		return goldenAttemptCommitError("command identity is aliased")
	}
	return nil
}

func validateGoldenAttemptCommitAuthority(
	authority GoldenAttemptCommitAuthority,
	command GoldenAttemptCommitCommand,
	finishedAt time.Time,
) error {
	if !validGoldenAttemptCommitAuthorityDocuments(authority, command) {
		return domain.ErrInternal
	}
	if !goldenAttemptCommitHeadsMatch(authority, command) {
		return ErrGoldenAttemptCommitAuthorityConflict
	}
	execution := authority.Execution
	if !goldenAttemptCommitExecutionActive(command.Scope, execution, finishedAt) {
		return ErrGoldenAttemptCommitAuthorityConflict
	}
	if !goldenAttemptCommitLedgersMatchExecution(authority, command.Scope, execution) {
		return ErrGoldenAttemptCommitAuthorityConflict
	}
	if !goldenAttemptTerminalCondition(authority, command, finishedAt) {
		return ErrGoldenAttemptNotTerminal
	}
	if len(authority.Positions.Positions)+len(authority.Submissions.Submissions) >
		authority.Positions.PositionTo-authority.Positions.PositionFrom+1 {
		return goldenAttemptCommitError("position interval would overflow")
	}
	if err := validateGoldenAttemptCommitSubmissions(authority, command.Scope, execution, finishedAt); err != nil {
		return err
	}
	if authority.Positions.Revision == math.MaxInt64 ||
		goldenPositionRevisionIDUsed(authority.Positions, command.NextPositionRevisionID) {
		return goldenAttemptCommitError("position successor identity is reused")
	}
	if !goldenAttemptCommitNewIDsAreFresh(authority, command) {
		return goldenAttemptCommitError("terminal identity aliases retained authority")
	}
	return nil
}

func goldenAttemptCommitNewIDsAreFresh(
	authority GoldenAttemptCommitAuthority,
	command GoldenAttemptCommitCommand,
) bool {
	reserved := RetainedIdentitySet(authority.Execution)
	ReserveLedgerIdentities(reserved, authority.Submissions)
	ReservePositionLedgerIdentities(reserved, authority.Positions)
	reserved[authority.SwissPoints.RevisionID] = struct{}{}
	identities := []uuid.UUID{command.CommandID, command.CommitID, command.NextPositionRevisionID}
	seen := make(map[uuid.UUID]struct{}, len(identities))
	for _, identity := range identities {
		if _, found := reserved[identity]; found {
			return false
		}
		if _, found := seen[identity]; found {
			return false
		}
		seen[identity] = struct{}{}
	}
	return true
}

func ReservePositionLedgerIdentities(
	reserved map[uuid.UUID]struct{},
	ledger GoldenPositionLedger,
) {
	for _, revisionID := range ledger.RevisionIDs {
		reserved[revisionID] = struct{}{}
	}
	for _, position := range ledger.Positions {
		reserved[position.ParticipantID] = struct{}{}
		reserved[position.AttemptID] = struct{}{}
		reserved[position.CommitID] = struct{}{}
	}
	for _, attempt := range ledger.Attempts {
		reserved[attempt.AttemptID] = struct{}{}
		reserved[attempt.SubmissionHead.RevisionID] = struct{}{}
		reserved[attempt.SubmissionHead.Scope.State.TournamentID] = struct{}{}
		reserved[attempt.SubmissionHead.Scope.State.GroupID] = struct{}{}
		reserved[attempt.SubmissionHead.Scope.AttemptID] = struct{}{}
		reserved[attempt.SubmissionHead.Scope.WaveID] = struct{}{}
		reserved[attempt.SubmissionHead.Scope.AssignmentID] = struct{}{}
		reserved[attempt.SubmissionHead.Scope.SnapshotID] = struct{}{}
		reserved[attempt.SubmissionHead.Scope.TaskID] = struct{}{}
		for _, item := range attempt.Order {
			reserved[item.ParticipantID] = struct{}{}
		}
	}
}

func validGoldenAttemptCommitAuthorityDocuments(
	authority GoldenAttemptCommitAuthority,
	command GoldenAttemptCommitCommand,
) bool {
	return authority.Scope == command.Scope && authority.Execution.Validate() == nil &&
		authority.Submissions.Validate() == nil && authority.Positions.Validate() == nil &&
		authority.SwissPoints.Validate() == nil
}

func goldenAttemptCommitHeadsMatch(
	authority GoldenAttemptCommitAuthority,
	command GoldenAttemptCommitCommand,
) bool {
	return authority.Execution.Expectation().Equal(command.ExpectedExecution) &&
		authority.Submissions.Expectation().Equal(command.ExpectedSubmissions) &&
		authority.Positions.Expectation().Equal(command.ExpectedPositions) &&
		authority.SwissPoints == command.ExpectedSwissPoints
}

func goldenAttemptCommitExecutionActive(
	scope GoldenSubmissionScope,
	execution GoldenWaveExecution,
	finishedAt time.Time,
) bool {
	return ScopeMatchesExecution(scope, execution) && execution.Start != nil &&
		execution.Wave.State == domain.WaveStateActive &&
		execution.Attempt.State == domain.GoldenAttemptStateActive &&
		!finishedAt.Before(execution.Start.StartedAt)
}

func goldenAttemptCommitLedgersMatchExecution(
	authority GoldenAttemptCommitAuthority,
	scope GoldenSubmissionScope,
	execution GoldenWaveExecution,
) bool {
	return authority.Submissions.Scope == scope && authority.Positions.Scope == scope.State &&
		authority.Positions.PositionFrom == execution.Group.PositionFrom &&
		authority.Positions.PositionTo == execution.Group.PositionTo
}

func goldenAttemptTerminalCondition(
	authority GoldenAttemptCommitAuthority,
	command GoldenAttemptCommitCommand,
	finishedAt time.Time,
) bool {
	if command.Reason == GoldenAttemptTerminalDeadline {
		return !finishedAt.Before(authority.Execution.Start.Deadline)
	}
	return len(authority.Submissions.Submissions) == len(authority.Execution.Attempt.ParticipantIDs)
}

func validateGoldenAttemptCommitSubmissions(
	authority GoldenAttemptCommitAuthority,
	scope GoldenSubmissionScope,
	execution GoldenWaveExecution,
	finishedAt time.Time,
) error {
	positioned := make(map[uuid.UUID]struct{}, len(authority.Positions.Positions))
	for _, position := range authority.Positions.Positions {
		positioned[position.ParticipantID] = struct{}{}
	}
	for _, submission := range authority.Submissions.Submissions {
		if submission.Scope != scope || !ContainsID(execution.Attempt.ParticipantIDs, submission.ParticipantID) ||
			submission.CommittedAt.After(finishedAt) || !submission.CommittedAt.Before(execution.Start.Deadline) {
			return ErrGoldenAttemptCommitAuthorityConflict
		}
		if _, duplicate := positioned[submission.ParticipantID]; duplicate {
			return goldenAttemptCommitError("submission participant already has a position")
		}
	}
	return nil
}
