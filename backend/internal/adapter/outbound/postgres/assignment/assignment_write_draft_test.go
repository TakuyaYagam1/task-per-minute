package assignment_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	assignmentadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

func TestExactDraftAssignmentAcceptsSelectedGameTwoChild(t *testing.T) {
	in := assignmentadapter.AssignmentCreateInput{PlanID: uuid.New(), BranchID: uuid.New(), RosterID: uuid.New(), SeriesID: uuid.New()}
	groupID := uuid.New()
	plan := sqlc.LockAssignmentPlanRow{ID: in.PlanID, Kind: "exact_draft", State: "committed", RosterID: in.RosterID, ActiveBranchID: testNullableUUID(uuid.New())}
	require.False(t, assignmentadapter.MatchesAssignmentPlan(plan, in), "ordinary branch matching must remain strict")
	plan.ActiveDraftBranchID = testNullableUUID(groupID)
	child := sqlc.LockAssignmentDraftChildScopeRow{ID: in.BranchID, PlanID: in.PlanID, RosterID: in.RosterID, SeriesID: in.SeriesID, GroupID: groupID, ChildState: "active", GroupState: "active"}
	require.True(t, assignmentadapter.MatchesExactDraftAssignmentPlan(plan, in, []sqlc.LockAssignmentDraftChildScopeRow{child}))
	for _, field := range []string{"group", "plan", "roster", "series", "child", "inactive child", "inactive group", "duplicate", "missing"} {
		t.Run(field, func(t *testing.T) {
			changed := child
			switch field {
			case "group":
				changed.GroupID = uuid.New()
			case "plan":
				changed.PlanID = uuid.New()
			case "roster":
				changed.RosterID = uuid.New()
			case "series":
				changed.SeriesID = uuid.New()
			case "child":
				changed.ID = uuid.New()
			case "inactive child":
				changed.ChildState = "reserved"
			case "inactive group":
				changed.GroupState = "released"
			}
			rows := []sqlc.LockAssignmentDraftChildScopeRow{changed}
			if field == "duplicate" {
				rows = append(rows, changed)
			}
			if field == "missing" {
				rows = nil
			}
			require.False(t, assignmentadapter.MatchesExactDraftAssignmentPlan(plan, in, rows))
		})
	}
	plan.Kind = "exact"
	require.False(t, assignmentadapter.MatchesExactDraftAssignmentPlan(plan, in, []sqlc.LockAssignmentDraftChildScopeRow{child}))
	plan.ActiveBranchID = testNullableUUID(in.BranchID)
	require.True(t, assignmentadapter.MatchesAssignmentPlan(plan, in))
}

func testNullableUUID(value uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: value, Valid: true}
}
