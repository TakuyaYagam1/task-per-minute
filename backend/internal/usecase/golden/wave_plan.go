package golden

import (
	"crypto/sha256"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func validateOpenGoldenReadyWindowCommand(command OpenGoldenReadyWindowCommand) error {
	if !ValidStateScope(command.Scope) || command.ExpectedState.Scope != command.Scope ||
		len(command.PrivateAssignments) == 0 || !validGoldenOpenPrimaryIDs(command) ||
		!validGoldenOpenRevisionIDs(command) {
		return goldenWaveError("invalid open command identity")
	}
	return nil
}

func validGoldenOpenPrimaryIDs(command OpenGoldenReadyWindowCommand) bool {
	return command.CommandID != uuid.Nil && command.AttemptID != uuid.Nil && command.WaveID != uuid.Nil &&
		command.WindowID != uuid.Nil && command.MembershipID != uuid.Nil && command.AssignmentID != uuid.Nil
}

func validGoldenOpenRevisionIDs(command OpenGoldenReadyWindowCommand) bool {
	return !command.WaveRevisionID.IsZero() && !command.WaveWindowRevisionID.IsZero() &&
		command.WindowRevisionID != uuid.Nil && command.ReadinessRevisionID != uuid.Nil &&
		command.PresenceRevisionID != uuid.Nil && command.MembershipRevisionID != uuid.Nil &&
		command.AssignmentRevisionID != uuid.Nil && command.ExecutionRevisionID != uuid.Nil
}

func validateGoldenOpenIdentityOwnership(state GoldenState, command OpenGoldenReadyWindowCommand) error {
	return ValidateFreshIdentityIDs(state, goldenOpenCommandIdentityIDs(command)...)
}

func goldenOpenCommandIdentityIDs(command OpenGoldenReadyWindowCommand) []uuid.UUID {
	identities := make([]uuid.UUID, 0, 14+len(command.PrivateAssignments))
	identities = append(identities,
		command.CommandID, command.AttemptID, command.WaveID, command.WaveRevisionID.UUID(), command.WindowID,
		command.WaveWindowRevisionID.UUID(), command.WindowRevisionID, command.ReadinessRevisionID,
		command.PresenceRevisionID, command.MembershipID, command.MembershipRevisionID,
		command.AssignmentID, command.AssignmentRevisionID, command.ExecutionRevisionID,
	)
	for _, private := range command.PrivateAssignments {
		identities = append(identities, private.AssignmentID)
	}
	return identities
}

func ValidateFreshIdentityIDs(state GoldenState, identities ...uuid.UUID) error {
	if err := waveValidateGoldenFreshIDs(state, identities...); err != nil {
		return goldenWaveError("identity ownership: %v", err)
	}
	reserved := make(map[uuid.UUID]struct{})
	for _, value := range AuthorityIdentityIDs(state.ExactPlan.Authority) {
		if value != uuid.Nil {
			reserved[value] = struct{}{}
		}
	}
	for _, reservation := range state.ExactPlan.Authority.ExistingTaskReservations {
		reserved[reservation.PlanID] = struct{}{}
		reserved[reservation.PlanRevisionID] = struct{}{}
	}
	for _, identity := range identities {
		if _, exists := reserved[identity]; exists {
			return goldenWaveError("identity aliases exact plan authority")
		}
	}
	return nil
}

func waveValidateGoldenFreshIDs(state GoldenState, candidates ...uuid.UUID) error {
	roles := CoreIdentityRoles(state)
	roles = append(roles, PlanIdentityRoles(state)...)
	roles = append(roles, WindowIdentityRoles(state)...)
	roles = append(roles, TransitionIdentityRoles(state)...)
	reserved := make(map[uuid.UUID]struct{}, len(roles))
	for _, role := range roles {
		reserved[role.Value] = struct{}{}
	}
	seen := make(map[uuid.UUID]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == uuid.Nil {
			continue
		}
		if _, exists := reserved[candidate]; exists {
			return goldenWaveError("identity aliases retained state authority")
		}
		if _, exists := seen[candidate]; exists {
			return goldenWaveError("new identities alias each other")
		}
		seen[candidate] = struct{}{}
	}
	return nil
}

func goldenStateHasUnresolvedAttempt(state GoldenState) bool {
	if len(state.Group.Attempts) == 0 {
		return false
	}
	for _, attempt := range state.Group.Attempts {
		if attempt.State == domain.GoldenAttemptStateCompleted {
			return true
		}
	}
	switch state.Group.Attempts[len(state.Group.Attempts)-1].State {
	case domain.GoldenAttemptStateCancelled, domain.GoldenAttemptStateVoid:
		return false
	case domain.GoldenAttemptStatePlanned, domain.GoldenAttemptStateWaitingReady,
		domain.GoldenAttemptStateActive, domain.GoldenAttemptStateCompleted:
		return true
	default:
		return true
	}
}

func buildGoldenAttempt(
	group domain.GoldenGroupState,
	attemptID uuid.UUID,
	participants []uuid.UUID,
) domain.GoldenAttempt {
	attempt := domain.GoldenAttempt{
		ID: attemptID, GroupID: group.ID, GroupRevisionID: group.RevisionID,
		AttemptNo: len(group.Attempts) + 1, State: domain.GoldenAttemptStateWaitingReady,
		ParticipantIDs: append([]uuid.UUID(nil), participants...),
	}
	if len(group.Attempts) > 0 {
		attempt.PreviousAttemptID = UUIDPointer(group.Attempts[len(group.Attempts)-1].ID)
	}
	return attempt
}

func goldenExactGroupPlan(
	plan ExactPlan,
	scope GoldenStateScope,
) (Group, bool) {
	for _, group := range plan.Groups {
		if group.GroupID == scope.GroupID && group.GroupRevisionID == scope.GroupRevisionID {
			return group, true
		}
	}
	return Group{}, false
}

func goldenAttemptPlanEdge(
	state GoldenState,
	scope GoldenStateScope,
) (Group, Edge, bool) {
	group, found := goldenExactGroupPlan(state.ExactPlan, scope)
	if !found {
		return Group{}, Edge{}, false
	}
	edgeIndex := len(state.Group.Attempts)
	if edgeIndex < 0 || edgeIndex >= len(group.Edges) {
		return Group{}, Edge{}, false
	}
	return group, group.Edges[edgeIndex], true
}

func validateGoldenExecutionSource(state GoldenState, execution GoldenWaveExecution) error {
	if state.Validate() != nil || execution.Validate() != nil {
		return domain.ErrInternal
	}
	if !state.Expectation().Equal(execution.Source) || state.Scope != execution.Scope {
		return ErrGoldenWaveAuthorityConflict
	}
	if !goldenSourceGroupIdentityEqual(state, execution) || !goldenSourceAttemptsEqual(state, execution) ||
		!goldenSourceActiveSetEqual(state, execution) || !goldenSourceAssignmentEqual(state, execution) {
		return ErrGoldenWaveAuthorityConflict
	}
	return nil
}

func goldenSourceGroupIdentityEqual(state GoldenState, execution GoldenWaveExecution) bool {
	if state.Group.ID != execution.Group.ID || state.Group.TournamentID != execution.Group.TournamentID ||
		state.Group.RevisionID != execution.Group.RevisionID ||
		state.Group.SourceProjectionRevisionID != execution.Group.SourceProjectionRevisionID ||
		state.Group.PositionFrom != execution.Group.PositionFrom || state.Group.PositionTo != execution.Group.PositionTo ||
		state.Group.ParticipationEstablished != execution.Group.ParticipationEstablished ||
		!reflect.DeepEqual(state.Group.Members, execution.Group.Members) ||
		len(execution.Group.Attempts) != len(state.Group.Attempts)+1 {
		return false
	}
	return true
}

func goldenSourceAttemptsEqual(state GoldenState, execution GoldenWaveExecution) bool {
	for index := range state.Group.Attempts {
		if !reflect.DeepEqual(state.Group.Attempts[index], execution.Group.Attempts[index]) {
			return false
		}
	}
	return reflect.DeepEqual(execution.Group.Attempts[len(execution.Group.Attempts)-1], execution.Attempt) &&
		execution.Attempt.AttemptNo == len(state.Group.Attempts)+1
}

func goldenSourceActiveSetEqual(state GoldenState, execution GoldenWaveExecution) bool {
	active := state.ActiveParticipantIDs()
	SortIDs(active)
	return len(active) >= 2 && EqualIDs(active, execution.Membership.ParticipantIDs) &&
		EqualIDs(active, execution.Attempt.ParticipantIDs) &&
		EqualIDs(active, PrivateAssignmentParticipantIDs(execution.Assignment.Private))
}

func goldenSourceAssignmentEqual(state GoldenState, execution GoldenWaveExecution) bool {
	_, edge, found := goldenAttemptPlanEdge(state, execution.Scope)
	return found && execution.Assignment.EdgeID == edge.ID &&
		execution.Assignment.ReservationID == edge.ReservationID &&
		execution.Assignment.ContentDigest == edge.ContentDigest &&
		reflect.DeepEqual(execution.Assignment.Snapshot, edge.Snapshot)
}

func buildGoldenPrivateAssignments(
	commands []GoldenPrivateAssignmentCommand,
	active []uuid.UUID,
	edge Edge,
) ([]GoldenPrivateAssignment, error) {
	if len(commands) != len(active) {
		return nil, goldenWaveError("private assignments do not cover active membership")
	}
	byParticipant := make(map[uuid.UUID]uuid.UUID, len(commands))
	for index, command := range commands {
		if command.ParticipantID == uuid.Nil || command.AssignmentID == uuid.Nil {
			return nil, goldenWaveError("invalid private assignment command")
		}
		if command.ParticipantID != active[index] {
			return nil, goldenWaveError("private assignments are not in exact membership order")
		}
		if _, duplicate := byParticipant[command.ParticipantID]; duplicate {
			return nil, goldenWaveError("duplicate private assignment participant")
		}
		byParticipant[command.ParticipantID] = command.AssignmentID
	}
	if len(byParticipant) != len(active) {
		return nil, goldenWaveError("private assignments do not cover active membership")
	}
	result := make([]GoldenPrivateAssignment, len(active))
	for index, participantID := range active {
		assignmentID, found := byParticipant[participantID]
		if !found {
			return nil, goldenWaveError("private assignments do not cover active membership")
		}
		result[index] = GoldenPrivateAssignment{
			ID: assignmentID, ParticipantID: participantID,
			SnapshotID: edge.Snapshot.SnapshotID, ContentDigest: edge.ContentDigest,
		}
	}
	return result, nil
}

func BuildMembers(participants []uuid.UUID) []domain.WaveMember {
	members := make([]domain.WaveMember, len(participants))
	for index, participantID := range participants {
		members[index] = domain.WaveMember{ParticipantID: participantID}
	}
	return members
}

func goldenExecutionActiveIDs(group domain.GoldenGroupState) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(group.Members))
	for _, member := range group.Members {
		if !member.Excluded {
			result = append(result, member.ParticipantID)
		}
	}
	SortIDs(result)
	return result
}

func MemberIDs(wave domain.Wave) []uuid.UUID {
	result := make([]uuid.UUID, len(wave.Members))
	for index, member := range wave.Members {
		result[index] = member.ParticipantID
	}
	SortIDs(result)
	return result
}

func PrivateAssignmentParticipantIDs(assignments []GoldenPrivateAssignment) []uuid.UUID {
	result := make([]uuid.UUID, len(assignments))
	for index, assignment := range assignments {
		result[index] = assignment.ParticipantID
	}
	SortIDs(result)
	return result
}

func goldenOpenCommandDigest(command OpenGoldenReadyWindowCommand) [sha256.Size]byte {
	payload, _ := Encode(command)
	return sha256.Sum256(payload)
}

func goldenAssignmentDigest(assignment GoldenAttemptAssignment) [sha256.Size]byte {
	type document struct {
		ID            uuid.UUID
		RevisionID    uuid.UUID
		Revision      int64
		Scope         GoldenStateScope
		AttemptID     uuid.UUID
		WaveID        uuid.UUID
		MembershipID  uuid.UUID
		Plan          GoldenPlanStateBinding
		EdgeID        uuid.UUID
		ReservationID uuid.UUID
		Snapshot      domain.AssignmentTaskSnapshot
		ContentDigest [sha256.Size]byte
		Private       []GoldenPrivateAssignment
	}
	payload, _ := Encode(document{
		ID: assignment.ID, RevisionID: assignment.RevisionID, Revision: assignment.Revision,
		Scope: assignment.Scope, AttemptID: assignment.AttemptID, WaveID: assignment.WaveID,
		MembershipID: assignment.MembershipID, Plan: assignment.Plan, EdgeID: assignment.EdgeID,
		ReservationID: assignment.ReservationID, Snapshot: assignment.Snapshot,
		ContentDigest: assignment.ContentDigest, Private: assignment.Private,
	})
	return sha256.Sum256(payload)
}

func goldenWavePayloadDigest(execution GoldenWaveExecution) [sha256.Size]byte {
	type document struct {
		Scope                           GoldenStateScope
		Source                          GoldenStateExpectation
		RevisionID                      uuid.UUID
		Revision                        int64
		PreviousRevisionID              *uuid.UUID
		Group                           domain.GoldenGroupState
		GroupBindingDigest              [sha256.Size]byte
		OpeningParticipationEstablished bool
		Attempt                         domain.GoldenAttempt
		Wave                            domain.Wave
		Membership                      GoldenWaveMembershipBinding
		Assignment                      GoldenAttemptAssignment
		Window                          GoldenReadyWindow
		OpenedAt                        time.Time
		Deadline                        time.Time
		ReceiptsDigest                  [sha256.Size]byte
		Start                           *GoldenStartRecord
	}
	payload, _ := Encode(document{
		Scope: execution.Scope, Source: execution.Source, RevisionID: execution.RevisionID,
		Revision: execution.Revision, PreviousRevisionID: execution.PreviousRevisionID,
		Group: execution.Group, GroupBindingDigest: execution.GroupBindingDigest,
		OpeningParticipationEstablished: execution.OpeningParticipationEstablished,
		Attempt:                         execution.Attempt, Wave: execution.Wave,
		Membership: execution.Membership, Assignment: execution.Assignment, Window: execution.Window,
		OpenedAt: execution.OpenedAt, Deadline: execution.Deadline,
		ReceiptsDigest: execution.ReceiptsDigest, Start: execution.Start,
	})
	return sha256.Sum256(payload)
}

func ExecutionGroupBindingDigest(
	group domain.GoldenGroupState,
	openingParticipationEstablished bool,
) ([sha256.Size]byte, error) {
	normalized := CloneGroup(group)
	normalized.ParticipationEstablished = openingParticipationEstablished
	if len(normalized.Attempts) > 0 {
		last := len(normalized.Attempts) - 1
		normalized.Attempts[last].State = domain.GoldenAttemptStateWaitingReady
		normalized.Attempts[last].StartedAt = nil
	}
	payload, err := Encode(normalized)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(payload), nil
}

func goldenWaveReceiptsDigest(receipts []GoldenWaveCommandReceipt) ([sha256.Size]byte, error) {
	normalized := cloneGoldenWaveReceipts(receipts)
	if len(normalized) > 0 {
		normalized[len(normalized)-1].Result.PayloadDigest = [sha256.Size]byte{}
	}
	payload, err := Encode(normalized)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(payload), nil
}
