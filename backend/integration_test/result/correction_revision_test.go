//go:build integration

package result

import "testing"

func TestCorrectionRevisionRollback(t *testing.T) {
	runCorrectionRevisionRollback(t, sharedPool)
}
