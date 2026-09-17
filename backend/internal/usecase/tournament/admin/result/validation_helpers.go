package result

import (
	"bytes"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

const maxReasonRunes = 512

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

func validText(value string, maxRunes int) bool {
	trimmed := strings.TrimSpace(value)
	return utf8.ValidString(value) && trimmed != "" && trimmed == value && utf8.RuneCountInString(value) <= maxRunes
}

func validToken(value string, maximum int) bool {
	if !validText(value, maximum) {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("._:-", character) {
			continue
		}
		return false
	}
	return true
}
