package plan

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// goldenGroupCommandReserveCount returns the active primary-plus-reserve
// prefix. The command shape remains a fixed three-slot transport for
// compatibility with persisted callers, while zero trailing slots encode a
// shorter configured chain without ever becoming part of a plan.
func goldenGroupCommandReserveCount(command GroupCommand) (int, error) {
	ids := [domain.MaxAssignmentReserveCount + 1][3]uuid.UUID{
		{command.EdgeIDs[0], command.ReservationIDs[0], command.SnapshotIDs[0]},
		{command.EdgeIDs[1], command.ReservationIDs[1], command.SnapshotIDs[1]},
		{command.EdgeIDs[2], command.ReservationIDs[2], command.SnapshotIDs[2]},
	}
	active := -1
	for index, group := range ids {
		allZero := true
		anyZero := false
		for _, id := range group {
			if id == uuid.Nil {
				anyZero = true
				continue
			}
			allZero = false
		}
		if allZero {
			if index == 0 {
				return 0, fmt.Errorf("%w: missing Golden primary edge", ErrInvalidExactPlan)
			}
			for _, trailing := range ids[index:] {
				for _, id := range trailing {
					if id != uuid.Nil {
						return 0, fmt.Errorf("%w: non-contiguous Golden reserve chain", ErrInvalidExactPlan)
					}
				}
			}
			break
		}
		if anyZero {
			return 0, fmt.Errorf("%w: partial Golden edge identity", ErrInvalidExactPlan)
		}
		active = index
	}
	if active < 0 {
		return 0, fmt.Errorf("%w: missing Golden primary edge", ErrInvalidExactPlan)
	}
	return active, nil
}

func goldenCommandReserveCount(command Command) (int, error) {
	if len(command.GroupCommands) == 0 {
		return 0, fmt.Errorf("%w: missing Golden group commands", ErrInvalidExactPlan)
	}
	want := -1
	for _, group := range command.GroupCommands {
		count, err := goldenGroupCommandReserveCount(group)
		if err != nil {
			return 0, err
		}
		if want == -1 {
			want = count
			continue
		}
		if count != want {
			return 0, fmt.Errorf("%w: Golden groups use different reserve counts", ErrInvalidExactPlan)
		}
	}
	return want, nil
}

func goldenPlanReserveCount(groups []Group) (int, error) {
	if len(groups) == 0 {
		return 0, fmt.Errorf("%w: missing Golden plan groups", ErrInvalidExactPlan)
	}
	want := -1
	for _, group := range groups {
		if len(group.Edges) < 1 || len(group.Edges) > domain.MaxAssignmentReserveCount+1 {
			return 0, fmt.Errorf("%w: invalid Golden group chain length", ErrInvalidExactPlan)
		}
		count := len(group.Edges) - 1
		if want == -1 {
			want = count
			continue
		}
		if count != want {
			return 0, fmt.Errorf("%w: Golden groups use different reserve counts", ErrInvalidExactPlan)
		}
	}
	return want, nil
}
