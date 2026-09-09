package golden

import (
	"crypto/sha256"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

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
	attempt := domain.GoldenAttempt{
		ID: command.NextAttemptID, GroupID: command.Scope.GroupID, GroupRevisionID: command.Scope.GroupRevisionID,
		AttemptNo:         authority.Terminal.Attempt.AttemptNo + 1,
		PreviousAttemptID: UUIDPointer(authority.Terminal.Attempt.ID),
		State:             domain.GoldenAttemptStatePlanned, ParticipantIDs: append([]uuid.UUID(nil), unresolved...),
	}
	group := CloneGroup(authority.Terminal.Group)
	group.Attempts = append(group.Attempts, CloneAttempt(attempt))
	if _, err := domain.NewGoldenGroup(group); err != nil {
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
	sealedAssignment, err := assignment.Seal()
	if err != nil {
		return GoldenContinuationRecord{}, goldenContinuationError("encode reserve assignment")
	}
	assignment = sealedAssignment
	record := GoldenContinuationRecord{
		ID: command.ContinuationID, CommandID: command.CommandID,
		CommandDigest: goldenContinuationCommandDigest(command), Scope: command.Scope,
		SourceTerminalID: authority.Terminal.ID, SourceTerminalDigest: authority.Terminal.PayloadDigest,
		SourceParentID: command.ExpectedParentID, SourceParentDigest: command.ExpectedParentDigest,
		ExpectedState: CloneExpectation(command.ExpectedState), ExpectedPlan: command.ExpectedPlan,
		ExpectedPositions: command.ExpectedPositions, SwissPoints: command.ExpectedSwissPoints,
		NewIdentityIDs:         goldenContinuationCommandIdentityIDs(command),
		ResolvedParticipantIDs: resolved, UnresolvedParticipantIDs: unresolved,
		RemainingPositions: remaining, Group: group, Attempt: attempt,
		Assignment: assignment, CreatedAt: createdAt, Positions: authority.Positions.Snapshot(),
	}
	payload, err := goldenContinuationPayload(record)
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
	SortIDs(wantResolved)
	if !EqualIDs(wantResolved, record.ResolvedParticipantIDs) {
		return goldenContinuationError("committed solver union changed")
	}
	resolved := IDSet(record.ResolvedParticipantIDs)
	wantUnresolved := make([]uuid.UUID, 0, len(record.Group.Members))
	for _, member := range record.Group.Members {
		if !member.Excluded && !resolved[member.ParticipantID] {
			wantUnresolved = append(wantUnresolved, member.ParticipantID)
		}
	}
	SortIDs(wantUnresolved)
	if !EqualIDs(wantUnresolved, record.UnresolvedParticipantIDs) {
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
	SortIDs(resolved)
	unresolved := make([]uuid.UUID, 0, len(terminal.Group.Members))
	for _, member := range terminal.Group.Members {
		if _, solved := resolvedSet[member.ParticipantID]; !member.Excluded && !solved {
			unresolved = append(unresolved, member.ParticipantID)
		}
	}
	SortIDs(unresolved)
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
	plan ExactPlan,
	terminal GoldenAttemptCommitRecord,
) (Edge, bool) {
	for _, group := range plan.Groups {
		if group.GroupID != terminal.Scope.State.GroupID ||
			group.GroupRevisionID != terminal.Scope.State.GroupRevisionID {
			continue
		}
		index := terminal.Attempt.AttemptNo
		if index < 0 || index >= len(group.Edges) {
			return Edge{}, false
		}
		return group.Edges[index], true
	}
	return Edge{}, false
}

func validateGoldenContinuationPrivate(
	requested []GoldenPrivateAssignmentCommand,
	unresolved []uuid.UUID,
	edge Edge,
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
	SortIDs(participants)
	SortIDs(assignments)
	if !EqualIDs(participants, unresolved) || !IDsCanonical(assignments) ||
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
