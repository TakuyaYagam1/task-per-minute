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
