package pause_test

import (
	"testing"
	"time"
)

func TestPauseResumeCAS(t *testing.T) {
	t.Parallel()

	resumedAt := time.Date(2026, time.August, 31, 13, 0, 0, 0, time.UTC)

	testPauseResumeSuccess(t, resumedAt)
	testPauseResumeValidation(t, resumedAt)
	testPauseResumeConflicts(t, resumedAt)
	testPauseResumeStoredRecords(t, resumedAt)
	testPauseResumeErrors(t, resumedAt)
}
