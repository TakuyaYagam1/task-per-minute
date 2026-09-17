package model

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
)

func validateFrozenDeadline(value PauseFrozenDeadline, active bool) error {
	if !validFrozenDeadlineHeader(value) {
		return normalPauseError("invalid frozen deadline")
	}
	if !value.Kind.isValid() {
		return normalPauseError("unknown frozen deadline kind")
	}
	if active {
		if value.ResumedAt != nil || value.ResumedDeadline != nil {
			return normalPauseError("active frozen deadline has resume evidence")
		}
		return nil
	}
	expectedDeadline, ok := safePauseTimeAddPointer(value.ResumedAt, value.Remaining)
	if value.ResumedAt == nil || value.ResumedDeadline == nil || !pauseValidServerTime(*value.ResumedAt) ||
		value.ResumedAt.Before(value.FrozenAt) || !pauseValidServerTime(*value.ResumedDeadline) || !ok ||
		!value.ResumedDeadline.Equal(expectedDeadline) {
		return normalPauseError("resolved deadline lacks shifted evidence")
	}
	return nil
}

func validateFrozenDeadlineSet(graph PauseGraph, paused bool) error {
	if !paused && len(graph.FrozenDeadlines) == 0 {
		return nil
	}
	expected := eligiblePauseDeadlines(graph, paused)
	seen := make(map[pauseDeadlineIdentity]struct{}, len(graph.FrozenDeadlines))
	for _, frozen := range graph.FrozenDeadlines {
		if err := validateFrozenDeadline(frozen, paused); err != nil {
			return err
		}
		key := pauseDeadlineIdentity{Kind: frozen.Kind, OwnerID: frozen.OwnerID}
		deadline, exists := expected[key]
		if !exists {
			return ErrNormalPauseGraphIncomplete
		}
		if _, duplicate := seen[key]; duplicate {
			return normalPauseError("duplicate frozen deadline")
		}
		if paused {
			if graph.PausedAt == nil || !frozen.FrozenAt.Equal(*graph.PausedAt) || (!deadline.IsZero() && !frozen.OriginalDeadline.Equal(deadline)) {
				return ErrNormalPauseGraphIncomplete
			}
		} else if frozen.ResumedDeadline == nil || !deadline.Equal(*frozen.ResumedDeadline) {
			return ErrNormalPauseGraphIncomplete
		}
		seen[key] = struct{}{}
	}
	if len(seen) != len(expected) {
		return ErrNormalPauseGraphIncomplete
	}
	return nil
}

func validFrozenDeadlineHeader(value PauseFrozenDeadline) bool {
	return value.OwnerID != uuid.Nil && value.Revision >= 1 && pauseValidServerTime(value.OriginalDeadline) &&
		pauseValidServerTime(value.FrozenAt) && value.Remaining > 0 && value.Remaining <= maxFrozenPauseDuration &&
		value.OriginalDeadline.Equal(value.FrozenAt.Add(value.Remaining))
}

func validPauseGraphScope(scope pausedomain.GraphScope) bool {
	return scope.Validate() == nil
}

func (reason PauseReason) allowsNormalPause() bool {
	return reason == PauseReasonOperator || reason == PauseReasonPlatform || reason == PauseReasonExecutionEpoch
}

func replaceSeriesGame(series *domain.Series, replacement domain.Game) bool {
	for slotIndex := range series.Slots {
		for gameIndex := range series.Slots[slotIndex].Attempts {
			if series.Slots[slotIndex].Attempts[gameIndex].ID == replacement.ID {
				series.Slots[slotIndex].Attempts[gameIndex] = pausedomain.CloneGame(replacement)
				return true
			}
		}
	}
	return false
}

func normalPauseError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidNormalPauseGraph, message)
}
