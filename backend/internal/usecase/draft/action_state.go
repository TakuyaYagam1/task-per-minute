package draft

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func draftLegalCategories(draft domain.Draft) []domain.Category {
	used := make(map[domain.Category]struct{}, len(draft.Actions))
	for _, action := range draft.Actions {
		used[action.Category] = struct{}{}
	}
	legal := make([]domain.Category, 0, len(draft.Pool)-len(used))
	for _, category := range draft.Pool {
		if _, exists := used[category]; !exists {
			legal = append(legal, category)
		}
	}
	return legal
}

func draftFinalTurn(draft domain.Draft) bool {
	return (draft.Format == domain.SeriesFormatBO1 && draft.Turn == 2) ||
		(draft.Format == domain.SeriesFormatBO3 && draft.Turn == 4)
}

func FinalTurn(draft domain.Draft) bool {
	return draftFinalTurn(draft)
}

func draftExpectation(draft Execution) RevisionExpectation {
	return RevisionExpectation{
		RevisionID: draft.RevisionID, Revision: draft.Revision, ServiceEpoch: draft.ServiceEpoch,
	}
}

func Expectation(draft Execution) RevisionExpectation {
	return draftExpectation(draft)
}

func draftExpectationMatches(
	draft Execution,
	revisionID uuid.UUID,
	revision int64,
	serviceEpoch uuid.UUID,
) bool {
	return draft.RevisionID == revisionID && draft.Revision == revision && draft.ServiceEpoch == serviceEpoch
}

func draftTimeoutArm(draft Execution) *TimeoutArm {
	if draft.State != ExecutionStateActive || draft.AbsoluteDeadline == nil {
		return nil
	}
	return &TimeoutArm{
		DraftID: draft.ID, RevisionID: draft.RevisionID, Revision: draft.Revision,
		ServiceEpoch: draft.ServiceEpoch, Turn: draft.Turn, Deadline: *draft.AbsoluteDeadline,
	}
}
