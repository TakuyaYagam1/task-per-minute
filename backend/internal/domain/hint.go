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

func IsValidTaskHints(hints []string) bool {
	if len(hints) != TaskHintCount {
		return false
	}
	for _, hint := range hints {
		if strings.TrimSpace(hint) == "" {
			return false
		}
	}
	return true
}
