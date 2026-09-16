package plan

import (
	"crypto/sha256"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/taskexec"
)

func (p ExactPlan) Validate() error {
	canonical, matching, err := validateGoldenExactPlanAuthority(p)
	if err != nil {
		return err
	}
	if err := validateGoldenExactPlanGroups(p.Groups, canonical, matching); err != nil {
		return err
	}
	return validateGoldenExactPlanProof(p)
}

func validateGoldenExactPlanAuthority(
	plan ExactPlan,
) (Authority, [][]int, error) {
	if err := validateGoldenExactPlanIdentity(plan); err != nil {
		return Authority{}, nil, err
	}
	canonical, evidence, err := normalizeGoldenExactPlanAuthority(plan.Authority)
	if err != nil {
		return Authority{}, nil, err
	}
	canonical.Evidence = evidence
	if plan.Expected != canonical.Expectation() || !reflect.DeepEqual(canonical, plan.Authority) {
		return Authority{}, nil, goldenExactPlanError("retained authority is not canonical")
	}
	if len(plan.Groups) != len(canonical.Groups) {
		return Authority{}, nil, goldenExactPlanError("plan does not cover every active group")
	}
	retainedCommand, err := goldenExactPlanCommandFromPlan(plan)
	if err != nil {
		return Authority{}, nil, err
	}
	if err := validateGoldenExactPlanCommandIdentity(retainedCommand); err != nil {
		return Authority{}, nil, err
	}
	if err := validateGoldenCommandAuthorityAliases(retainedCommand, canonical); err != nil {
		return Authority{}, nil, err
	}
	matching, err := goldenExactMatching(canonical)
	if err != nil {
		return Authority{}, nil, err
	}
	return canonical, matching, nil
}

func validateGoldenExactPlanIdentity(plan ExactPlan) error {
	if plan.Scope != plan.Authority.Scope || plan.PlanID == uuid.Nil || plan.PlanRevisionID == uuid.Nil ||
		plan.PlanID == plan.PlanRevisionID || plan.CreatedAt.IsZero() || plan.CreatedAt.Location() != time.UTC {
		return goldenExactPlanError("invalid plan identity")
	}
	return nil
}

func validateGoldenExactPlanGroups(
	groups []Group,
	authority Authority,
	matching [][]int,
) error {
	selected := make(map[domain.TaskVersionRef]struct{}, len(groups)*(domain.AssignmentReserveCount+1))
	for groupIndex, group := range groups {
		if err := validateGoldenExactGroupPlan(groupIndex, group, authority, matching, selected); err != nil {
			return err
		}
	}
	return nil
}

func validateGoldenExactGroupPlan(
	groupIndex int,
	group Group,
	authority Authority,
	matching [][]int,
	selected map[domain.TaskVersionRef]struct{},
) error {
	authorityGroup := authority.Groups[groupIndex]
	if !goldenExactGroupMatchesAuthority(group, authorityGroup) {
		return goldenExactPlanError("group plan does not match current membership")
	}
	for edgeIndex, edge := range group.Edges {
		if err := validateGoldenExactPlanEdge(groupIndex, edgeIndex, edge, authority, matching, selected); err != nil {
			return err
		}
	}
	return nil
}

func goldenExactGroupMatchesAuthority(
	group Group,
	authority GroupAuthority,
) bool {
	from, to := authority.Revision.Positions()
	return group.GroupID == authority.Revision.GroupID() &&
		group.GroupRevisionID == authority.Revision.RevisionID() &&
		group.SourceProjectionRevisionID == authority.Revision.SourceProjectionRevisionID() &&
		group.PositionFrom == from && group.PositionTo == to &&
		reflect.DeepEqual(group.ParticipantIDs, authority.ActiveParticipantIDs) &&
		len(group.Edges) == domain.AssignmentReserveCount+1
}

func validateGoldenExactPlanEdge(
	groupIndex int,
	edgeIndex int,
	edge Edge,
	authority Authority,
	matching [][]int,
	selected map[domain.TaskVersionRef]struct{},
) error {
	if !validGoldenExactPlanEdgeIdentity(edgeIndex, edge) {
		return goldenExactPlanError("invalid selected edge")
	}
	candidate := authority.Candidates[matching[groupIndex][edgeIndex]]
	wantSnapshot, buildErr := taskexec.BuildSnapshot(taskexec.SnapshotInput{
		SnapshotID: edge.Snapshot.SnapshotID, Version: candidate.Version,
		Kind: domain.AssignmentTaskKindGolden, Task: goldenExactSnapshotTask(candidate.Task),
	})
	if buildErr != nil || !reflect.DeepEqual(wantSnapshot, edge.Snapshot) {
		return goldenExactPlanError("selected snapshot does not follow canonical matching")
	}
	digest, digestErr := taskexec.SnapshotDigest(wantSnapshot)
	if digestErr != nil || digest != edge.ContentDigest {
		return goldenExactPlanError("selected artifact digest changed")
	}
	ref := domain.TaskVersionRef{TaskID: edge.Snapshot.TaskID, Version: edge.Snapshot.Version}
	if _, duplicate := selected[ref]; duplicate {
		return goldenExactPlanError("task version is reserved more than once")
	}
	candidateIndex := goldenCandidateIndex(authority.Candidates, ref)
	if candidateIndex != matching[groupIndex][edgeIndex] ||
		!goldenCandidateEligibleForGroup(authority, groupIndex, candidateIndex) {
		return goldenExactPlanError("selected task is not eligible for every active member")
	}
	selected[ref] = struct{}{}
	return nil
}

func validGoldenExactPlanEdgeIdentity(edgeIndex int, edge Edge) bool {
	return edge.ID != uuid.Nil && edge.ReservationID != uuid.Nil && edge.Position == edgeIndex+1 &&
		edge.Snapshot.Kind == domain.AssignmentTaskKindGolden && edge.Snapshot.Validate() == nil
}

func goldenExactPlanCommandFromPlan(plan ExactPlan) (Command, error) {
	command := Command{
		Scope: plan.Scope, PlanID: plan.PlanID, PlanRevisionID: plan.PlanRevisionID,
		Expected: plan.Expected, CreatedAt: plan.CreatedAt,
		GroupCommands: make([]GroupCommand, len(plan.Groups)),
	}
	for groupIndex, group := range plan.Groups {
		if len(group.Edges) != domain.AssignmentReserveCount+1 {
			return Command{}, goldenExactPlanError("group does not contain one primary and two reserves")
		}
		command.GroupCommands[groupIndex].GroupID = group.GroupID
		command.GroupCommands[groupIndex].GroupRevisionID = group.GroupRevisionID
		for edgeIndex, edge := range group.Edges {
			command.GroupCommands[groupIndex].EdgeIDs[edgeIndex] = edge.ID
			command.GroupCommands[groupIndex].ReservationIDs[edgeIndex] = edge.ReservationID
			command.GroupCommands[groupIndex].SnapshotIDs[edgeIndex] = edge.Snapshot.SnapshotID
		}
	}
	return command, nil
}

func validateGoldenCommandAuthorityAliases(
	command Command,
	authority Authority,
) error {
	seen := make(map[uuid.UUID]struct{})
	for _, id := range AuthorityIdentityIDs(authority) {
		seen[id] = struct{}{}
	}
	for _, reservation := range authority.ExistingTaskReservations {
		seen[reservation.PlanID] = struct{}{}
		seen[reservation.PlanRevisionID] = struct{}{}
	}
	stable := []uuid.UUID{command.PlanID, command.PlanRevisionID}
	for _, group := range command.GroupCommands {
		for index := range group.EdgeIDs {
			stable = append(stable, group.EdgeIDs[index], group.ReservationIDs[index], group.SnapshotIDs[index])
		}
	}
	for _, id := range stable {
		if _, alias := seen[id]; alias {
			return goldenExactPlanError("stable plan identity aliases retained authority")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func validateGoldenExactPlanCommandIdentity(command Command) error {
	if err := validateGoldenExactPlanCommandBase(command); err != nil {
		return err
	}
	boundIDs := goldenExactPlanCommandBoundIDs(command)
	seenStable := make(map[uuid.UUID]struct{}, len(boundIDs)+len(command.GroupCommands)*11)
	if err := addGoldenCommandStableIDs(seenStable, boundIDs, "bound command identity"); err != nil {
		return err
	}
	seenGroups := make(map[uuid.UUID]struct{}, len(command.GroupCommands))
	for _, group := range command.GroupCommands {
		if err := validateGoldenExactGroupCommandIdentity(group, seenGroups, seenStable); err != nil {
			return err
		}
	}
	return nil
}

func validateGoldenExactPlanCommandBase(command Command) error {
	if !validGoldenExactPlanScope(command.Scope) || command.PlanID == uuid.Nil ||
		command.PlanRevisionID == uuid.Nil || command.PlanID == command.PlanRevisionID ||
		command.CreatedAt.IsZero() || command.CreatedAt.Location() != time.UTC ||
		!validGoldenExactPlanRevisions(command.Expected.Revisions) ||
		goldenExpectationHasZeroDigest(command.Expected) || len(command.GroupCommands) == 0 {
		return goldenExactPlanError("invalid command identity or expectation")
	}
	return nil
}

func goldenExactPlanCommandBoundIDs(command Command) []uuid.UUID {
	return []uuid.UUID{
		command.Scope.TournamentID, command.Scope.PlanSetID, command.PlanID, command.PlanRevisionID,
		command.Expected.Revisions.SourceProjectionRevisionID.UUID(),
		command.Expected.Revisions.GroupSetRevisionID, command.Expected.Revisions.PoolRevisionID,
		command.Expected.Revisions.HistoryRevisionID, command.Expected.Revisions.TaskHealthRevisionID,
		command.Expected.Revisions.ArtifactRevisionID, command.Expected.Revisions.ReservationRevisionID,
		command.Expected.Revisions.MembershipRevisionID,
	}
}

func validateGoldenExactGroupCommandIdentity(
	group GroupCommand,
	seenGroups map[uuid.UUID]struct{},
	seenStable map[uuid.UUID]struct{},
) error {
	if group.GroupID == uuid.Nil || group.GroupRevisionID.IsZero() {
		return goldenExactPlanError("missing group command identity")
	}
	if _, duplicate := seenGroups[group.GroupID]; duplicate {
		return goldenExactPlanError("duplicate group command")
	}
	seenGroups[group.GroupID] = struct{}{}
	if err := addGoldenCommandStableIDs(
		seenStable,
		[]uuid.UUID{group.GroupID, group.GroupRevisionID.UUID()},
		"group command identity",
	); err != nil {
		return err
	}
	for index := range group.EdgeIDs {
		if err := addGoldenCommandStableIDs(
			seenStable,
			[]uuid.UUID{group.EdgeIDs[index], group.ReservationIDs[index], group.SnapshotIDs[index]},
			"stable edge identity",
		); err != nil {
			return err
		}
	}
	return nil
}

func addGoldenCommandStableIDs(
	seen map[uuid.UUID]struct{},
	ids []uuid.UUID,
	kind string,
) error {
	for _, id := range ids {
		if id == uuid.Nil {
			return goldenExactPlanError("missing %s", kind)
		}
		if _, duplicate := seen[id]; duplicate {
			return goldenExactPlanError("%s is reused", kind)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func goldenExpectationHasZeroDigest(expected Expectation) bool {
	zero := [sha256.Size]byte{}
	return expected.SourcePayloadDigest == zero || expected.GroupDigest == zero ||
		expected.PoolDigest == zero || expected.HistoryDigest == zero ||
		expected.TaskHealthDigest == zero || expected.ArtifactDigest == zero ||
		expected.ReservationDigest == zero || expected.MembershipDigest == zero
}
