package correction

import (
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

const maxReasonRunes = 512

func validCommandScope(scope CommandScope) bool {
	return scope.Operator.ActorID != uuid.Nil && scope.TournamentID != uuid.Nil && scope.CommandID != uuid.Nil
}

func validText(value string, maxRunes int) bool {
	trimmed := strings.TrimSpace(value)
	return utf8.ValidString(value) && trimmed != "" && trimmed == value && utf8.RuneCountInString(value) <= maxRunes
}
