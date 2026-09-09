package game_test

import (
	"testing"
	"time"
)

func TestNormalPauseGraphEntry(t *testing.T) {
	t.Parallel()

	pausedAt := time.Date(2026, time.August, 31, 11, 0, 0, 0, time.UTC)

	testNormalPauseGraphEntrySuccess(t, pausedAt)
	testNormalPauseGraphEntryValidation(t, pausedAt)
	testNormalPauseGraphEntryConflicts(t, pausedAt)
	testNormalPauseGraphEntryStoredRecords(t, pausedAt)
	testNormalPauseGraphEntryDraft(t, pausedAt)
	testNormalPauseGraphEntryErrors(t, pausedAt)
}
