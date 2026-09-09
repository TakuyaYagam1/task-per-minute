//go:build integration

package integration_test

import (
	"testing"
)

func TestReconnectContinuationSegments(t *testing.T) {
	testReconnectContinuationLifecycle(t)
	testReconnectContinuationDeadlineAndLineage(t)
	testReconnectContinuationIdentityAndCounters(t)
}
