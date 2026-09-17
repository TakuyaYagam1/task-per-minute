//go:build integration

package result

import "testing"

func TestResultCorrectionRepositoryRechecksCurrentRevisionUnderLock(t *testing.T) {
	runResultCorrectionRepositoryRechecksCurrentRevisionUnderLock(t, sharedPool)
}
