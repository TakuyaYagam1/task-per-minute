package game_test

import (
	"testing"
	"time"
)

func TestReconnectTimeoutAutoloss(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, time.September, 1, 13, 0, 0, 0, time.UTC)

	testReconnectTimeoutOutcomes(t, base)
	testReconnectTimeoutTiming(t, base)
	testReconnectTimeoutSeries(t, base)
	testReconnectTimeoutValidation(t, base)
	testReconnectTimeoutReceipts(t, base)
}
