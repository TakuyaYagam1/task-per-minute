package correction

import "github.com/TakuyaYagam1/task-per-minute/internal/domain"

//nolint:gocyclo // Iterative traversal keeps cycle, bounds, and deterministic ordering explicit.
func (i correctionCutoffIndex) orderedDescendants(
	target domain.DerivedRevisionID,
) ([]domain.DerivedRevision, map[domain.DerivedRevisionID]struct{}, error) {
	affected := map[domain.DerivedRevisionID]struct{}{target: {}}
	pending := []domain.DerivedRevisionID{target}
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		for _, derived := range i.outgoing[current] {
			if _, seen := affected[derived]; seen {
				continue
			}
			affected[derived] = struct{}{}
			pending = append(pending, derived)
		}
	}

	indegree := make(map[domain.DerivedRevisionID]int, len(affected)-1)
	for _, revision := range i.ordered {
		if revision.ID() != target {
			if _, included := affected[revision.ID()]; included {
				indegree[revision.ID()] = 0
			}
		}
	}
	for source := range affected {
		for _, derived := range i.outgoing[source] {
			if source == target {
				continue
			}
			if _, included := indegree[derived]; included {
				indegree[derived]++
			}
		}
	}
	queue := make([]domain.DerivedRevisionID, 0, len(indegree))
	for _, revision := range i.ordered {
		if count, included := indegree[revision.ID()]; included && count == 0 {
			queue = append(queue, revision.ID())
		}
	}
	descendants := make([]domain.DerivedRevision, 0, len(indegree))
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		descendants = append(descendants, i.byID[current])
		for _, derived := range i.outgoing[current] {
			count, included := indegree[derived]
			if !included {
				continue
			}
			count--
			indegree[derived] = count
			if count == 0 {
				queue = append(queue, derived)
			}
		}
	}
	if len(descendants) != len(indegree) {
		return nil, nil, rejectCorrection(
			RejectionMalformed, ErrInvalid, "descendant graph is cyclic",
		)
	}
	return descendants, affected, nil
}
