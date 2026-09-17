//go:build integration

package reconnect

import "testing"

func TestReconnectContinuationPauseAtomicity(t *testing.T) {
	RunReconnectPauseAtomicity(t, sharedPool)
}

func TestReconnectContinuationWaveMembershipFence(t *testing.T) {
	RunReconnectWaveMembershipFence(t, sharedPool)
}
