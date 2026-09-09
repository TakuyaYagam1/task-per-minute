package golden

import (
	"crypto/sha256"

	"github.com/google/uuid"
)

func validateGoldenContinuationCommand(command GoldenContinuationCommand) error {
	if !validGoldenContinuationCommandHeader(command) {
		return goldenContinuationError("invalid continuation command identity")
	}
	if !validGoldenContinuationCommandParent(command) {
		return goldenContinuationError("invalid parent continuation reference")
	}
	newIdentities := goldenContinuationCommandIdentityIDs(command)
	if !ValidIdentitySet(newIdentities) {
		return goldenContinuationError("continuation command identity is aliased")
	}
	if goldenContinuationCommandAliasesSource(command, newIdentities) {
		return goldenContinuationError("continuation identity aliases a source receipt")
	}
	return nil
}

func validGoldenContinuationCommandHeader(command GoldenContinuationCommand) bool {
	return ValidStateScope(command.Scope) && command.CommandID != uuid.Nil &&
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
	return ContainsID(newIdentities, command.ExpectedTerminalID) ||
		(command.ExpectedParentID != uuid.Nil && ContainsID(newIdentities, command.ExpectedParentID))
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
	SortIDs(identities)
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
	SortIDs(identities)
	return identities
}

func validateGoldenContinuationNewIdentities(
	authority GoldenContinuationAuthority,
	command GoldenContinuationCommand,
) error {
	identities := goldenContinuationCommandIdentityIDs(command)
	if err := ValidateFreshIdentityIDs(authority.State, identities...); err != nil {
		return goldenContinuationError("successor identity aliases retained state or plan authority")
	}
	reserved := make(map[uuid.UUID]struct{})
	ReservePositionLedgerIdentities(reserved, authority.Positions)
	ReservePositionLedgerIdentities(reserved, authority.Terminal.PriorPositions)
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
	ReservePositionLedgerIdentities(reserved, record.Positions)
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
