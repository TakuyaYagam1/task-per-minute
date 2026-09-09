//go:build integration && capacity

package integration_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	tournamentCapacityPeakDuration             = 45 * time.Minute
	tournamentCapacityPeakParticipants         = 16
	tournamentCapacityPeakGames                = 8
	tournamentCapacityPeakWorkers              = 8
	tournamentCapacityPeakOperationInterval    = 100 * time.Millisecond
	tournamentCapacityPeakMinimumThroughput    = 60.0
	tournamentCapacityPeakLatencyP99LimitMS    = 400.0
	tournamentCapacityPeakSchedulingLagLimitMS = 3000.0
)

func TestTournamentPeakSoak(t *testing.T) {
	config := tournamentCapacityConfig{
		Name:                 "tournament45",
		Profile:              "peak",
		Duration:             tournamentCapacityPeakDuration,
		Participants:         tournamentCapacityPeakParticipants,
		Games:                tournamentCapacityPeakGames,
		Workers:              tournamentCapacityPeakWorkers,
		OperationInterval:    tournamentCapacityPeakOperationInterval,
		MinimumThroughput:    tournamentCapacityPeakMinimumThroughput,
		LatencyP99LimitMS:    tournamentCapacityPeakLatencyP99LimitMS,
		SchedulingLagLimitMS: tournamentCapacityPeakSchedulingLagLimitMS,
	}

	require.Equal(t, config.Games, config.Workers, "peak profile must run every game in parallel")
	report := runTournamentCapacityWorkload(t, config)
	require.GreaterOrEqual(t, report.Window.DurationSeconds, tournamentCapacityPeakDuration.Seconds())
	assertTournamentCapacityReportPass(t, report)
}
