package postgres

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

func TestExactDraftAssignmentAcceptsSelectedGameTwoChild(t *testing.T) {
	in := AssignmentCreateInput{PlanID: uuid.New(), BranchID: uuid.New(), RosterID: uuid.New(), SeriesID: uuid.New()}
	groupID := uuid.New()
	plan := sqlc.LockAssignmentPlanRow{ID: in.PlanID, Kind: "exact_draft", State: "committed", RosterID: in.RosterID, ActiveBranchID: nullableUUIDValue(uuid.New())}
	require.False(t, matchesAssignmentPlan(plan, in), "ordinary branch matching must remain strict")
	plan.ActiveDraftBranchID = nullableUUIDValue(groupID)
	child := sqlc.LockAssignmentDraftChildScopeRow{ID: in.BranchID, PlanID: in.PlanID, RosterID: in.RosterID, SeriesID: in.SeriesID, GroupID: groupID, ChildState: "active", GroupState: "active"}
	require.True(t, matchesExactDraftAssignmentPlan(plan, in, []sqlc.LockAssignmentDraftChildScopeRow{child}))
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
			require.False(t, matchesExactDraftAssignmentPlan(plan, in, rows))
		})
	}
	plan.Kind = "exact"
	require.False(t, matchesExactDraftAssignmentPlan(plan, in, []sqlc.LockAssignmentDraftChildScopeRow{child}))
	plan.ActiveBranchID = nullableUUIDValue(in.BranchID)
	require.True(t, matchesAssignmentPlan(plan, in))
}
