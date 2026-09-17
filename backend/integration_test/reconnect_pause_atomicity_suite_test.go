//go:build integration

package integration_test

import (
	"testing"

	reconnectintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/reconnect"
)

func TestReconnectContinuationPauseAtomicity(t *testing.T) {
	reconnectintegration.RunReconnectPauseAtomicity(t, sharedPool)
}
