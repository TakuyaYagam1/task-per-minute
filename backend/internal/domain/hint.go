package domain

import (
	"strings"
	"time"
)

const TaskHintCount = 3

type HintScheduleEntry struct {
	Index    int
	UnlockAt time.Time
}

type UnlockedHint struct {
	Index      int
	Text       string
	UnlockedAt time.Time
}

func BuildHintSchedule(startedAt time.Time, timeLimitSeconds int) []HintScheduleEntry {
	limit := time.Duration(timeLimitSeconds) * time.Second
	return []HintScheduleEntry{
		{Index: 1, UnlockAt: startedAt.Add(limit / 4)},
		{Index: 2, UnlockAt: startedAt.Add(limit / 2)},
		{Index: 3, UnlockAt: startedAt.Add(limit * 3 / 4)},
	}
}

func NormalizeTaskHints(hints []string) ([]string, bool) {
	if len(hints) > TaskHintCount {
		return nil, false
	}
	out := make([]string, TaskHintCount)
	for i, hint := range hints {
		out[i] = strings.TrimSpace(hint)
	}
	return out, true
}

func IsValidTaskHints(hints []string) bool {
	_, ok := NormalizeTaskHints(hints)
	return ok
}

func TaskHintText(hints []string, idx int) (string, bool) {
	normalized, ok := NormalizeTaskHints(hints)
	if !ok || idx < 0 || idx >= TaskHintCount {
		return "", false
	}
	text := normalized[idx]
	return text, text != ""
}

// UnlockedTaskHints returns the non-empty hints whose schedule has elapsed at
// observedAt. The returned slice intentionally omits locked and empty slots so
// a participant cannot infer undisclosed hint content from placeholders.
func UnlockedTaskHints(hints []string, schedule []HintScheduleEntry, observedAt time.Time) ([]string, bool) {
	normalized, ok := NormalizeTaskHints(hints)
	if !ok || len(schedule) != TaskHintCount || !IsValidServerTime(observedAt) {
		return nil, false
	}
	visible := make([]string, 0, TaskHintCount)
	for index, entry := range schedule {
		if entry.Index != index+1 || !IsValidServerTime(entry.UnlockAt) {
			return nil, false
		}
		if normalized[index] != "" && !observedAt.Before(entry.UnlockAt) {
			visible = append(visible, normalized[index])
		}
	}
	return visible, true
}
