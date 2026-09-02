//go:build integration && capacity

package integration_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	arenaCapacityPeakDuration             = 45 * time.Minute
	arenaCapacityPeakParticipants         = 16
	arenaCapacityPeakGames                = 8
	arenaCapacityPeakWorkers              = 8
	arenaCapacityPeakOperationInterval    = 100 * time.Millisecond
	arenaCapacityPeakMinimumThroughput    = 60.0
	arenaCapacityPeakLatencyP99LimitMS    = 400.0
	arenaCapacityPeakSchedulingLagLimitMS = 3000.0
)

func TestArenaPeakSoak(t *testing.T) {
	config := arenaCapacityConfig{
		Name:                 "arena45",
		Profile:              "peak",
		Duration:             arenaCapacityPeakDuration,
		Participants:         arenaCapacityPeakParticipants,
		Games:                arenaCapacityPeakGames,
		Workers:              arenaCapacityPeakWorkers,
		OperationInterval:    arenaCapacityPeakOperationInterval,
		MinimumThroughput:    arenaCapacityPeakMinimumThroughput,
		LatencyP99LimitMS:    arenaCapacityPeakLatencyP99LimitMS,
		SchedulingLagLimitMS: arenaCapacityPeakSchedulingLagLimitMS,
	}

	require.Equal(t, config.Games, config.Workers, "peak profile must run every game in parallel")
	report := runArenaCapacityWorkload(t, config)
	require.GreaterOrEqual(t, report.Window.DurationSeconds, arenaCapacityPeakDuration.Seconds())
	assertArenaCapacityReportPass(t, report)
}
