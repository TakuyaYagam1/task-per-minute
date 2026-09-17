package execution

import (
	"bytes"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pairingusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/pairing"
)

const maxReasonRunes = 512

func validOperator(scope CommandScope) bool {
	return scope.Operator.ActorID != uuid.Nil
}

func validCommandScope(scope CommandScope) bool {
	return validOperator(scope) && scope.TournamentID != uuid.Nil && scope.CommandID != uuid.Nil
}

func addUniqueID(values map[uuid.UUID]struct{}, value uuid.UUID) bool {
	if _, exists := values[value]; exists {
		return false
	}
	values[value] = struct{}{}
	return true
}

func addUniqueInt(values map[int]struct{}, value int) bool {
	if _, exists := values[value]; exists {
		return false
	}
	values[value] = struct{}{}
	return true
}

func containsID(values map[uuid.UUID]struct{}, value uuid.UUID) bool {
	_, exists := values[value]
	return exists
}

func idSet(values []uuid.UUID) map[uuid.UUID]struct{} {
	result := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
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

func validEventTime(value *time.Time, createdAt, updatedAt time.Time) bool {
	return value == nil || domain.IsValidServerTime(*value) && !value.Before(createdAt) && !value.After(updatedAt)
}

func validText(value string, maxRunes int) bool {
	trimmed := strings.TrimSpace(value)
	return utf8.ValidString(value) && trimmed != "" && trimmed == value && utf8.RuneCountInString(value) <= maxRunes
}

func validOptionalText(value string, maxRunes int) bool {
	return value == "" || validText(value, maxRunes)
}

func validSwissStanding(standing SwissStandingView, total int) bool {
	return pairingusecase.ValidSwissStanding(standing, total)
}
