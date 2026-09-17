//go:build integration

package reconnect

import (
	"testing"
)

func TestReconnectContinuationSegments(t *testing.T) {
	testReconnectContinuationLifecycle(t)
	testReconnectContinuationDeadlineAndLineage(t)
	testReconnectContinuationIdentityAndCounters(t)
}
