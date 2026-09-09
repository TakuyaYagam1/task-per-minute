//go:build integration

package integration_test

import (
	"testing"
)

func TestReconnectContinuationPauseAtomicity(t *testing.T) {
	testReconnectContinuationPauseCommit(t)
	testReconnectContinuationPauseConcurrency(t)
}
