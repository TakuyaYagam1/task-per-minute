package lifecycle

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	operationusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
)

const maxReasonRunes = 512

type CommandScope = operationusecase.CommandScope
type RevisionConflictError = operationusecase.RevisionConflictError

func validCommandScope(scope CommandScope) bool {
	return scope.Operator.ActorID != uuid.Nil && scope.TournamentID != uuid.Nil && scope.CommandID != uuid.Nil
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

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
