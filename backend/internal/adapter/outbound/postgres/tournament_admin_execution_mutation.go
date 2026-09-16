package postgres

import "github.com/google/uuid"

// These root-private helpers are kept for the unmoved normal-pause workflow.
// Wave mutation implementation is owned by tournament/admin/execution.
func uniqueTournamentAdminExecutionIDs(ids []uuid.UUID) bool {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return false
		}
		if _, duplicate := seen[id]; duplicate {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

func sameTournamentAdminExecutionIDs(first, second []uuid.UUID) bool {
	if len(first) != len(second) || !uniqueTournamentAdminExecutionIDs(first) ||
		!uniqueTournamentAdminExecutionIDs(second) {
		return false
	}
	seen := make(map[uuid.UUID]struct{}, len(first))
	for _, id := range first {
		seen[id] = struct{}{}
	}
	for _, id := range second {
		if _, ok := seen[id]; !ok {
			return false
		}
	}
	return true
}
