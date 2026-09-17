package configuration

import (
	"bytes"
	"slices"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func validCommandScope(scope CommandScope) bool {
	return scope.Operator.ActorID != uuid.Nil && scope.TournamentID != uuid.Nil && scope.CommandID != uuid.Nil
}

func validUniqueIDs(values []uuid.UUID, minimum, maximum int) bool {
	if len(values) < minimum || len(values) > maximum {
		return false
	}
	canonical := append([]uuid.UUID(nil), values...)
	slices.SortFunc(canonical, func(first, second uuid.UUID) int { return bytes.Compare(first[:], second[:]) })
	for index, value := range canonical {
		if value == uuid.Nil || index > 0 && value == canonical[index-1] {
			return false
		}
	}
	return true
}

func canonicalCategories(categories []domain.Category) []domain.Category {
	result := append([]domain.Category(nil), categories...)
	slices.Sort(result)
	return result
}

func cloneUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
