package golden

import (
	"crypto/sha256"
	"fmt"

	"github.com/google/uuid"
)

func validateGoldenOperationIDs(state GoldenState, operation goldenParticipationOperation) error {
	return stateValidateGoldenFreshIDs(
		state,
		operation.commandID,
		operation.nextStateRevisionID,
		operation.nextWindowRevisionID,
		operation.nextReadinessRevisionID,
		operation.nextPresenceRevisionID,
	)
}

func stateValidateGoldenFreshIDs(state GoldenState, candidates ...uuid.UUID) error {
	roles := goldenCoreIdentityRoles(state)
	roles = append(roles, goldenPlanIdentityRoles(state)...)
	roles = append(roles, goldenWindowIdentityRoles(state)...)
	roles = append(roles, goldenTransitionIdentityRoles(state)...)
	reserved := make(map[uuid.UUID]struct{}, len(roles))
	for _, role := range roles {
		reserved[role.value] = struct{}{}
	}
	seen := make(map[uuid.UUID]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == uuid.Nil {
			continue
		}
		if _, exists := reserved[candidate]; exists {
			return goldenStateError("command identity aliases retained authority")
		}
		if _, exists := seen[candidate]; exists {
			return goldenStateError("command identities alias each other")
		}
		seen[candidate] = struct{}{}
	}
	return nil
}

func validateGoldenStateIdentity(state GoldenState) error {
	invalidStatePredecessor := state.Revision == 1 && state.PreviousRevisionID != nil
	if state.Revision > 1 {
		invalidStatePredecessor = state.PreviousRevisionID == nil
		if state.PreviousRevisionID != nil {
			invalidStatePredecessor = *state.PreviousRevisionID == uuid.Nil || *state.PreviousRevisionID == state.RevisionID
		}
	}
	if goldenAny(
		!validGoldenStateScope(state.Scope), state.RevisionID == uuid.Nil, state.Revision < 1,
		invalidStatePredecessor, state.Plan.PlanID == uuid.Nil, state.Plan.RevisionID == uuid.Nil,
		state.Plan.PlanID == state.Plan.RevisionID, state.Plan.ProofDigest == [sha256.Size]byte{},
		state.Plan.EdgeDigest == [sha256.Size]byte{}, state.Plan.ParticipantDigest == [sha256.Size]byte{},
		state.Membership.RevisionID == uuid.Nil, state.Membership.Revision < 1,
	) {
		return goldenStateError("invalid identity or revision lineage")
	}
	if state.Membership.PayloadDigest != goldenMembershipDigest(state.Group.Members) {
		return goldenStateError("membership digest changed")
	}
	invalidMembershipPredecessor := state.Membership.Revision == 1 && state.Membership.PreviousRevisionID != nil
	if state.Membership.Revision > 1 {
		invalidMembershipPredecessor = state.Membership.PreviousRevisionID == nil
		if state.Membership.PreviousRevisionID != nil {
			invalidMembershipPredecessor = *state.Membership.PreviousRevisionID == uuid.Nil ||
				*state.Membership.PreviousRevisionID == state.Membership.RevisionID
		}
	}
	if invalidMembershipPredecessor {
		return goldenStateError("invalid membership revision lineage")
	}
	return nil
}

type stateGoldenIdentityRole struct {
	value uuid.UUID
	role  string
}

func validateGoldenIdentityRoles(state GoldenState) error {
	roles := goldenCoreIdentityRoles(state)
	roles = append(roles, goldenPlanIdentityRoles(state)...)
	roles = append(roles, goldenWindowIdentityRoles(state)...)
	roles = append(roles, goldenTransitionIdentityRoles(state)...)
	owners := make(map[uuid.UUID]string, len(roles))
	for _, identity := range roles {
		if identity.value == uuid.Nil {
			continue
		}
		if existing, found := owners[identity.value]; found && existing != identity.role {
			return goldenStateError("identity is reused across Golden authority roles")
		}
		owners[identity.value] = identity.role
	}
	return nil
}

func goldenCoreIdentityRoles(state GoldenState) []stateGoldenIdentityRole {
	roles := []stateGoldenIdentityRole{
		{state.Scope.TournamentID, "tournament"},
		{state.Scope.GroupID, goldenEntityRole("group", state.Scope.GroupID)},
		{state.Scope.GroupRevisionID.UUID(), goldenEntityRole("group-revision", state.Scope.GroupRevisionID.UUID())},
		{state.Topology.SourceProjectionRevisionID().UUID(), "source-projection-revision"},
		{state.RevisionID, goldenStateRevisionRole(state.Revision)},
		{state.Membership.RevisionID, goldenMembershipRevisionRole(state.Membership.Revision)},
		{state.Plan.PlanID, "plan"}, {state.Plan.RevisionID, "plan-revision"},
		{state.ExactPlan.Scope.PlanSetID, "plan-set"},
	}
	if state.PreviousRevisionID != nil {
		roles = append(roles, stateGoldenIdentityRole{*state.PreviousRevisionID, goldenStateRevisionRole(state.Revision - 1)})
	}
	if state.Membership.PreviousRevisionID != nil {
		roles = append(roles, stateGoldenIdentityRole{
			*state.Membership.PreviousRevisionID,
			goldenMembershipRevisionRole(state.Membership.Revision - 1),
		})
	}
	for _, member := range state.Group.Members {
		roles = append(roles, stateGoldenIdentityRole{member.ParticipantID, goldenEntityRole("participant", member.ParticipantID)})
	}
	for _, attempt := range state.Group.Attempts {
		roles = append(roles, stateGoldenIdentityRole{attempt.ID, goldenEntityRole("attempt", attempt.ID)})
		if attempt.PreviousAttemptID != nil {
			roles = append(roles, stateGoldenIdentityRole{*attempt.PreviousAttemptID, goldenEntityRole("attempt", *attempt.PreviousAttemptID)})
		}
	}
	return roles
}

func goldenPlanIdentityRoles(state GoldenState) []stateGoldenIdentityRole {
	revisions := state.ExactPlan.Expected.Revisions
	roles := []stateGoldenIdentityRole{
		{revisions.SourceProjectionRevisionID.UUID(), "source-projection-revision"},
		{revisions.GroupSetRevisionID, "plan-group-set-revision"},
		{revisions.PoolRevisionID, "plan-pool-revision"},
		{revisions.HistoryRevisionID, "plan-history-revision"},
		{revisions.TaskHealthRevisionID, "plan-health-revision"},
		{revisions.ArtifactRevisionID, "plan-artifact-revision"},
		{revisions.ReservationRevisionID, "plan-reservation-revision"},
		{revisions.MembershipRevisionID, "plan-membership-revision"},
		{state.ExactPlan.Authority.Pool.ID, "plan-pool-revision"},
	}
	for _, group := range state.ExactPlan.Groups {
		roles = append(roles,
			stateGoldenIdentityRole{group.GroupID, goldenEntityRole("group", group.GroupID)},
			stateGoldenIdentityRole{group.GroupRevisionID.UUID(), goldenEntityRole("group-revision", group.GroupRevisionID.UUID())},
			stateGoldenIdentityRole{group.SourceProjectionRevisionID.UUID(), "source-projection-revision"},
		)
		for _, participantID := range group.ParticipantIDs {
			roles = append(roles, stateGoldenIdentityRole{participantID, goldenEntityRole("participant", participantID)})
		}
		for _, edge := range group.Edges {
			roles = append(roles,
				stateGoldenIdentityRole{edge.ID, goldenEntityRole("plan-edge", edge.ID)},
				stateGoldenIdentityRole{edge.ReservationID, goldenEntityRole("task-reservation", edge.ReservationID)},
				stateGoldenIdentityRole{edge.Snapshot.SnapshotID, goldenEntityRole("task-snapshot", edge.Snapshot.SnapshotID)},
				stateGoldenIdentityRole{edge.Snapshot.TaskID, goldenEntityRole("task", edge.Snapshot.TaskID)},
			)
		}
	}
	for _, authorityGroup := range state.ExactPlan.Authority.Groups {
		revision := authorityGroup.Revision
		roles = append(roles,
			stateGoldenIdentityRole{revision.GroupID(), goldenEntityRole("group", revision.GroupID())},
			stateGoldenIdentityRole{revision.RevisionID().UUID(), goldenEntityRole("group-revision", revision.RevisionID().UUID())},
		)
		for _, member := range revision.Members() {
			roles = append(roles, stateGoldenIdentityRole{member.ParticipantID, goldenEntityRole("participant", member.ParticipantID)})
		}
	}
	return roles
}

func goldenWindowIdentityRoles(state GoldenState) []stateGoldenIdentityRole {
	roles := make([]stateGoldenIdentityRole, 0, len(state.Windows)*7)
	for _, window := range state.Windows {
		roles = append(roles,
			stateGoldenIdentityRole{window.ID, goldenEntityRole("window", window.ID)},
			stateGoldenIdentityRole{window.RevisionID, goldenWindowRevisionRole(window.ID, "state", window.Revision)},
			stateGoldenIdentityRole{window.ReadinessRevisionID, goldenWindowRevisionRole(window.ID, "readiness", window.ReadinessRevision)},
			stateGoldenIdentityRole{window.PresenceRevisionID, goldenWindowRevisionRole(window.ID, "presence", window.PresenceRevision)},
		)
		if window.PreviousRevisionID != nil {
			roles = append(roles, stateGoldenIdentityRole{*window.PreviousRevisionID, goldenWindowRevisionRole(window.ID, "state", window.Revision-1)})
		}
		if window.ReadinessPreviousRevisionID != nil {
			roles = append(roles, stateGoldenIdentityRole{*window.ReadinessPreviousRevisionID, goldenWindowRevisionRole(window.ID, "readiness", window.ReadinessRevision-1)})
		}
		if window.PresencePreviousRevisionID != nil {
			roles = append(roles, stateGoldenIdentityRole{*window.PresencePreviousRevisionID, goldenWindowRevisionRole(window.ID, "presence", window.PresenceRevision-1)})
		}
	}
	return roles
}

func goldenTransitionIdentityRoles(state GoldenState) []stateGoldenIdentityRole {
	roles := make([]stateGoldenIdentityRole, 0, len(state.ReadyEvents)*7+len(state.NoShows)*6+4)
	for _, event := range state.ReadyEvents {
		roles = append(roles, goldenExpectationIdentityRoles(event.ExpectedState)...)
		roles = append(roles, goldenWindowExpectationIdentityRoles(event.ExpectedWindow)...)
		windowStep := int64(0)
		if goldenReadyEventChangesWindow(event.Type) {
			windowStep = 1
		}
		presenceStep := int64(0)
		if event.Type == GoldenReadyEventDisconnected {
			presenceStep = 1
		}
		roles = append(roles,
			stateGoldenIdentityRole{event.CommandID, goldenEntityRole("command", event.CommandID)},
			stateGoldenIdentityRole{event.ResultStateRevisionID, goldenStateRevisionRole(event.ExpectedState.Revision + 1)},
			stateGoldenIdentityRole{event.ResultWindowRevisionID, goldenWindowRevisionRole(event.WindowID, "state", event.ExpectedWindow.Revision+windowStep)},
			stateGoldenIdentityRole{event.ResultReadinessRevisionID, goldenWindowRevisionRole(event.WindowID, "readiness", event.ExpectedWindow.ReadinessRevision+windowStep)},
			stateGoldenIdentityRole{event.ResultPresenceRevisionID, goldenWindowRevisionRole(event.WindowID, "presence", event.ExpectedWindow.PresenceRevision+presenceStep)},
		)
	}
	for _, resolution := range state.NoShows {
		roles = append(roles, goldenExpectationIdentityRoles(resolution.ExpectedState)...)
		roles = append(roles, goldenWindowExpectationIdentityRoles(resolution.ExpectedWindow)...)
		roles = append(roles,
			stateGoldenIdentityRole{resolution.CommandID, goldenEntityRole("command", resolution.CommandID)},
			stateGoldenIdentityRole{resolution.ResultStateRevisionID, goldenStateRevisionRole(resolution.ExpectedState.Revision + 1)},
			stateGoldenIdentityRole{resolution.ResultWindowRevisionID, goldenWindowRevisionRole(resolution.WindowID, "state", resolution.ExpectedWindow.Revision+1)},
			stateGoldenIdentityRole{resolution.ResultMembershipRevisionID, goldenMembershipRevisionRole(resolution.ExpectedState.Membership.Revision + 1)},
		)
	}
	if state.Allocation != nil {
		roles = append(roles, goldenExpectationIdentityRoles(state.Allocation.ExpectedState)...)
		roles = append(roles,
			stateGoldenIdentityRole{state.Allocation.ID, goldenEntityRole("allocation", state.Allocation.ID)},
			stateGoldenIdentityRole{state.Allocation.CommandID, goldenEntityRole("command", state.Allocation.CommandID)},
			stateGoldenIdentityRole{state.Allocation.ResultStateRevisionID, goldenStateRevisionRole(state.Allocation.ExpectedState.Revision + 1)},
		)
	}
	return roles
}

func goldenExpectationIdentityRoles(expected GoldenStateExpectation) []stateGoldenIdentityRole {
	roles := []stateGoldenIdentityRole{
		{expected.RevisionID, goldenStateRevisionRole(expected.Revision)},
		{expected.Membership.RevisionID, goldenMembershipRevisionRole(expected.Membership.Revision)},
	}
	if expected.Membership.PreviousRevisionID != nil {
		roles = append(roles, stateGoldenIdentityRole{
			*expected.Membership.PreviousRevisionID,
			goldenMembershipRevisionRole(expected.Membership.Revision - 1),
		})
	}
	return roles
}

func goldenWindowExpectationIdentityRoles(expected GoldenReadyWindowExpectation) []stateGoldenIdentityRole {
	return []stateGoldenIdentityRole{
		{expected.RevisionID, goldenWindowRevisionRole(expected.WindowID, "state", expected.Revision)},
		{expected.ReadinessRevisionID, goldenWindowRevisionRole(expected.WindowID, "readiness", expected.ReadinessRevision)},
		{expected.PresenceRevisionID, goldenWindowRevisionRole(expected.WindowID, "presence", expected.PresenceRevision)},
	}
}

func goldenEntityRole(kind string, value uuid.UUID) string {
	return kind + ":" + value.String()
}

func goldenStateRevisionRole(revision int64) string {
	return fmt.Sprintf("state-revision:%d", revision)
}

func goldenMembershipRevisionRole(revision int64) string {
	return fmt.Sprintf("membership-revision:%d", revision)
}

func goldenWindowRevisionRole(windowID uuid.UUID, kind string, revision int64) string {
	return fmt.Sprintf("window:%s:%s-revision:%d", windowID, kind, revision)
}
