package game_test

import (
	"testing"
	"time"
)

func TestPauseResumeSinglePresenceMatrix(t *testing.T) {
	t.Parallel()
	decidedAt := time.Date(2026, time.August, 31, 15, 0, 0, 0, time.UTC)

	testPauseResumeSinglePresenceSuccess(t, decidedAt)
	testPauseResumeSinglePresenceValidation(t, decidedAt)
	testPauseResumeSinglePresenceConflicts(t, decidedAt)
	testPauseResumeSinglePresenceHead(t, decidedAt)
}
