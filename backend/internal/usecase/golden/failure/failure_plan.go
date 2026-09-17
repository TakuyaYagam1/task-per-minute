package golden

import (
	"crypto/sha256"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func buildGoldenFailureReplayRecord(
	authority GoldenFailureAuthority,
	command GoldenFailureReplayCommand,
	digest [sha256.Size]byte,
	failedAt time.Time,
) (GoldenFailureRecord, error) {
	base, err := buildGoldenFailureBase(
		authority, command.CommandID, command.FailureID, command.ClosedWaveRevisionID,
		GoldenFailureRouteReplay, digest, failedAt,
	)
	if err != nil {
		return GoldenFailureRecord{}, err
	}
	replacement, ids, err := buildGoldenFailureReplacement(authority, base.Group, command, failedAt)
	if err != nil {
		return GoldenFailureRecord{}, err
	}
	base.Replacement = &replacement
	base.NewIdentityIDs = append(base.NewIdentityIDs, ids...)
	SortIDs(base.NewIdentityIDs)
	if !ValidIdentitySet(base.NewIdentityIDs) || goldenFailureAliasesAuthority(authority, base.NewIdentityIDs) {
		return GoldenFailureRecord{}, goldenFailureError("failure identity is reused")
	}
	return sealGoldenFailureRecord(base)
}

func buildGoldenFailureBase(
	authority GoldenFailureAuthority,
	commandID uuid.UUID,
	failureID uuid.UUID,
	closedWaveRevisionID domain.WaveRevisionID,
	route GoldenFailureRoute,
	digest [sha256.Size]byte,
	failedAt time.Time,
) (GoldenFailureRecord, error) {
	if closedWaveRevisionID.IsZero() || closedWaveRevisionID == authority.Active.Wave.RevisionID {
		return GoldenFailureRecord{}, goldenFailureError("invalid completed Wave revision")
	}
	attempt := CloneAttempt(authority.Active.Attempt)
	attempt.State = domain.GoldenAttemptStateVoid
	attempt.FinishedAt = failureCloneTimePointer(&failedAt)
	group := CloneGroup(authority.Active.Group)
	group.Attempts[len(group.Attempts)-1] = CloneAttempt(attempt)
	if _, err := domain.NewGoldenGroup(group); err != nil {
		return GoldenFailureRecord{}, goldenFailureError("void group is invalid")
	}
	wave := CloneExecution(authority.Active.Wave)
	wave.State = domain.WaveStateCompleted
	wave.RevisionID = closedWaveRevisionID
	if err := wave.Validate(); err != nil {
		return GoldenFailureRecord{}, goldenFailureError("complete old Wave")
	}
	record := GoldenFailureRecord{
		ID: failureID, CommandID: commandID, CommandDigest: digest, Route: route,
		Scope: authority.Active.Scope, Expected: authority.Expectation(),
		Classification: authority.Classification.Snapshot(), FailedAssignment: authority.Active.Assignment.Snapshot(),
		FailedAt: failedAt, Attempt: attempt, Group: group, OldWave: wave,
		DiscardedSubmissions: authority.Submissions.Expectation(),
		DiscardedOrder:       authority.Submissions.ProvisionalOrder(),
		PriorPositions:       authority.Positions.Snapshot(), Positions: authority.Positions.Snapshot(),
		NewIdentityIDs: []uuid.UUID{commandID, failureID, closedWaveRevisionID.UUID()},
	}
	return record.Snapshot(), nil
}

func buildGoldenFailureReplacement(
	authority GoldenFailureAuthority,
	voidGroup domain.GoldenGroupState,
	command GoldenFailureReplayCommand,
	openedAt time.Time,
) (GoldenFailureReplacement, []uuid.UUID, error) {
	nextEdge := authority.Classification.NextEdge
	if nextEdge == nil {
		return GoldenFailureReplacement{}, nil, ErrGoldenFailureRouteConflict
	}
	participants := append([]uuid.UUID(nil), authority.Classification.ParticipantIDs...)
	private, err := buildGoldenFailurePrivateAssignments(command.PrivateAssignments, participants, *nextEdge)
	if err != nil {
		return GoldenFailureReplacement{}, nil, err
	}
	attempt := domain.GoldenAttempt{
		ID: command.NextAttemptID, GroupID: authority.Active.Scope.State.GroupID,
		GroupRevisionID: authority.Active.Scope.State.GroupRevisionID,
		AttemptNo:       authority.Active.Attempt.AttemptNo + 1, PreviousAttemptID: UUIDPointer(authority.Active.Attempt.ID),
		State: domain.GoldenAttemptStatePlanned, ParticipantIDs: participants,
	}
	group := CloneGroup(voidGroup)
	group.Attempts = append(group.Attempts, CloneAttempt(attempt))
	if _, err := domain.NewGoldenGroup(group); err != nil {
		return GoldenFailureReplacement{}, nil, goldenFailureError("replacement group is invalid")
	}
	assignment := GoldenReserveAttemptAssignment{
		ID: command.NextAssignmentID, RevisionID: command.NextAssignmentRevisionID,
		Scope: authority.Active.Scope.State, AttemptID: command.NextAttemptID,
		EdgeID: nextEdge.ID, EdgePosition: nextEdge.Position, ReservationID: nextEdge.ReservationID,
		SnapshotID: nextEdge.SnapshotID, TaskID: nextEdge.TaskID, ContentDigest: nextEdge.ContentDigest, Private: private,
	}
	sealedAssignment, err := assignment.Seal()
	if err != nil {
		return GoldenFailureReplacement{}, nil, goldenFailureError("encode reserve assignment")
	}
	assignment = sealedAssignment
	deadline := openedAt.Add(domain.ReadyWindowDuration)
	wave := domain.Wave{
		ID: command.NextWaveID, TournamentID: authority.Active.Scope.State.TournamentID,
		RevisionID: command.NextWaveRevisionID, State: domain.WaveStatePlanned,
		Members: BuildMembers(participants),
	}
	if err := wave.OpenReadyWindow(command.NextWindowID, command.NextWaveWindowRevisionID, openedAt, deadline); err != nil {
		return GoldenFailureReplacement{}, nil, goldenFailureError("open replacement ready window")
	}
	membership := GoldenWaveMembershipBinding{
		ID: command.NextMembershipID, RevisionID: command.NextMembershipRevisionID, Revision: 1,
		Source: authority.State.Membership, ParticipantIDs: append([]uuid.UUID(nil), participants...),
		PayloadDigest: DigestIDs(participants),
	}
	window := GoldenReadyWindow{
		ID: command.NextWindowID, RevisionID: command.NextWindowRevisionID, Revision: 1,
		AttemptID: attempt.ID, AttemptNo: attempt.AttemptNo, OpenedAt: openedAt, Deadline: deadline,
		State: GoldenReadyWindowOpen, ReadinessRevisionID: command.NextReadinessRevisionID, ReadinessRevision: 1,
		PresenceRevisionID: command.NextPresenceRevisionID, PresenceRevision: 1,
		BasePresentParticipantIDs: append([]uuid.UUID(nil), participants...),
		PresentParticipantIDs:     append([]uuid.UUID(nil), participants...),
		ReadinessDigest:           DigestIDs(nil), PresenceDigest: DigestIDs(participants),
	}
	replacement := GoldenFailureReplacement{
		Group: group, Attempt: attempt, Assignment: assignment, Membership: membership,
		Wave: wave, Window: window, OpenedAt: openedAt, Deadline: deadline,
	}
	if err := replacement.Validate(); err != nil {
		return GoldenFailureReplacement{}, nil, err
	}
	ids := []uuid.UUID{
		command.NextAttemptID, command.NextAssignmentID, command.NextAssignmentRevisionID,
		command.NextWaveID, command.NextWaveRevisionID.UUID(), command.NextWindowID,
		command.NextWaveWindowRevisionID.UUID(), command.NextWindowRevisionID,
		command.NextReadinessRevisionID, command.NextPresenceRevisionID,
		command.NextMembershipID, command.NextMembershipRevisionID,
	}
	for _, assignment := range command.PrivateAssignments {
		ids = append(ids, assignment.AssignmentID)
	}
	SortIDs(ids)
	return replacement.Snapshot(), ids, nil
}

func buildGoldenFailurePrivateAssignments(
	requested []GoldenPrivateAssignmentCommand,
	participants []uuid.UUID,
	edge GoldenFailureEdge,
) ([]GoldenPrivateAssignment, error) {
	if len(requested) != len(participants) || len(requested) > domain.TournamentMaxParticipants {
		return nil, goldenFailureError("private assignments do not cover current membership")
	}
	byParticipant := make(map[uuid.UUID]uuid.UUID, len(requested))
	for _, item := range requested {
		if item.ParticipantID == uuid.Nil || item.AssignmentID == uuid.Nil {
			return nil, goldenFailureError("invalid private assignment identity")
		}
		if _, duplicate := byParticipant[item.ParticipantID]; duplicate {
			return nil, goldenFailureError("private assignment participant is duplicated")
		}
		byParticipant[item.ParticipantID] = item.AssignmentID
	}
	private := make([]GoldenPrivateAssignment, len(participants))
	for index, participantID := range participants {
		assignmentID, found := byParticipant[participantID]
		if !found {
			return nil, goldenFailureError("private assignments do not cover current membership")
		}
		private[index] = GoldenPrivateAssignment{
			ID: assignmentID, ParticipantID: participantID, SnapshotID: edge.SnapshotID, ContentDigest: edge.ContentDigest,
		}
	}
	return private, nil
}
