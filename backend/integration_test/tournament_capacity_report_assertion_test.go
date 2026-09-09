//go:build integration && capacity

package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeTournamentCapacityReport(t *testing.T, report tournamentCapacityReport) {
	t.Helper()
	assertTournamentCapacityReportSemantics(t, report)

	data, err := json.MarshalIndent(report, "", "  ")
	require.NoError(t, err)
	validateTournamentCapacityJSON(t, data)

	name := report.Workload.Name + "-" + report.Workload.Profile + ".json"
	path := filepath.Join(t.ArtifactDir(), name)
	require.NoError(t, os.WriteFile(path, append(data, '\n'), 0o600))
	compact, err := json.Marshal(report)
	require.NoError(t, err)
	t.Logf("TOURNAMENT_CAPACITY_REPORT %s", compact)
	t.Logf("capacity report artifact: %s", name)
}

func assertTournamentCapacityReportPass(tb testing.TB, report tournamentCapacityReport) {
	tb.Helper()
	assertTournamentCapacityReportSemantics(tb, report)
	require.Equal(tb, tournamentCapacityPass, report.Operations.Correctness.Status)
	require.Equal(tb, tournamentCapacityPass, report.LatencyMS.Status)
	require.Equal(tb, tournamentCapacityPass, report.Throughput.Status)
	require.Equal(tb, tournamentCapacityPass, report.Lag.Status)
	require.Equal(tb, tournamentCapacityPass, report.Reconnect.Status)
	require.Equal(tb, tournamentCapacityPass, report.Resources.Goroutines.Status)
	require.Equal(tb, tournamentCapacityPass, report.Resources.HeapAllocBytes.Status)
	require.Equal(tb, tournamentCapacityPass, report.Database.Status)
	require.Equal(tb, tournamentCapacityPass, report.EnvironmentEquivalence.Status)
	for _, threshold := range report.Thresholds {
		require.Equal(tb, tournamentCapacityPass, threshold.Status, threshold.Metric)
	}
}

func assertTournamentCapacityReportSemantics(tb testing.TB, report tournamentCapacityReport) {
	tb.Helper()
	require.Equal(tb, tournamentCapacityReportSchema, report.Schema)
	require.Equal(tb, tournamentCapacityReportVersion, report.Version)
	require.Equal(tb, report.Operations.Total, report.Operations.Succeeded+report.Operations.Failed)
	require.Equal(tb, report.Operations.Correctness.Checks,
		report.Operations.Correctness.Passed+report.Operations.Correctness.Failed)
	require.Equal(tb, tournamentCapacityStatusFor(report.Operations.Correctness.Failed == 0),
		report.Operations.Correctness.Status)
	require.LessOrEqual(tb, report.LatencyMS.P50, report.LatencyMS.P95)
	require.LessOrEqual(tb, report.LatencyMS.P95, report.LatencyMS.P99)
	require.LessOrEqual(tb, report.LatencyMS.P99, report.LatencyMS.Max)
	require.Equal(tb, tournamentCapacityStatusFor(report.LatencyMS.P99 <= report.LatencyMS.P99Limit),
		report.LatencyMS.Status)
	require.Equal(tb,
		tournamentCapacityStatusFor(report.Throughput.OperationsPerSecond >= report.Throughput.Minimum),
		report.Throughput.Status)
	require.Equal(tb, tournamentCapacityStatusFor(report.Lag.MaxMS <= report.Lag.MaxLimitMS), report.Lag.Status)
	require.Positive(tb, report.Reconnect.Attempts)
	require.GreaterOrEqual(tb, report.Reconnect.Succeeded, int64(0))
	require.EqualValues(tb, report.Load.Games, report.Reconnect.Attempts)
	require.Equal(tb, report.Reconnect.Attempts, report.Reconnect.Succeeded+report.Reconnect.Failed)
	require.Equal(
		tb,
		tournamentCapacityStatusFor(
			report.Reconnect.Attempts > 0 &&
				report.Reconnect.Succeeded > 0 &&
				report.Reconnect.Failed == 0,
		),
		report.Reconnect.Status,
	)
	require.Equal(tb,
		tournamentCapacityStatusFor(report.Resources.Goroutines.ObservedMax <= report.Resources.Goroutines.Limit),
		report.Resources.Goroutines.Status)
	require.Equal(tb,
		tournamentCapacityStatusFor(report.Resources.HeapAllocBytes.ObservedMax <= report.Resources.HeapAllocBytes.Limit),
		report.Resources.HeapAllocBytes.Status)
	require.LessOrEqual(tb, report.Database.ObservedMax, report.Database.PoolLimit)
	require.Equal(tb,
		tournamentCapacityStatusFor(report.Database.ObservedMax <= report.Database.PoolLimit),
		report.Database.Status)
	require.Equal(tb, tournamentCapacityEquivalenceScope, report.EnvironmentEquivalence.Scope)
	require.True(tb, report.EnvironmentEquivalence.ProductionRepository)
	require.Equal(tb, "postgresql", report.EnvironmentEquivalence.DatabaseEngine)
	require.Equal(tb, 18, report.EnvironmentEquivalence.DatabaseMajor)
	require.Equal(tb, "goose", report.EnvironmentEquivalence.MigrationEngine)
	require.Equal(tb, tournamentCapacityPostgresImage, report.EnvironmentEquivalence.ContainerImage)
	require.Equal(tb, tournamentCapacityStatusFor(
		report.EnvironmentEquivalence.ProductionRepository &&
			report.EnvironmentEquivalence.DatabaseEngine == "postgresql" &&
			report.EnvironmentEquivalence.DatabaseMajor == 18 &&
			len(report.EnvironmentEquivalence.Differences) == 0,
	),
		report.EnvironmentEquivalence.Status)

	startedAt, err := time.Parse(time.RFC3339Nano, report.Window.StartedAt)
	require.NoError(tb, err)
	endedAt, err := time.Parse(time.RFC3339Nano, report.Window.EndedAt)
	require.NoError(tb, err)
	require.True(tb, endedAt.After(startedAt))
	require.InDelta(tb, endedAt.Sub(startedAt).Seconds(), report.Window.DurationSeconds, 0.001)

	var workloadDurationSeconds int64
	switch report.Workload.Profile {
	case "nominal":
		require.Equal(tb, "tournament60", report.Workload.Name)
		require.Equal(tb, 16, report.Load.Participants)
		require.Equal(tb, 8, report.Load.Games)
		require.Equal(tb, 4, report.Load.Workers)
		require.GreaterOrEqual(tb, report.Window.DurationSeconds, 3600.0)
		workloadDurationSeconds = 3600
	case "peak":
		require.Equal(tb, "tournament45", report.Workload.Name)
		require.Equal(tb, 16, report.Load.Participants)
		require.Equal(tb, 8, report.Load.Games)
		require.Equal(tb, 8, report.Load.Workers)
		require.GreaterOrEqual(tb, report.Window.DurationSeconds, 2700.0)
		workloadDurationSeconds = 2700
	default:
		tb.Fatalf("unknown capacity profile %q", report.Workload.Profile)
	}
	assertTournamentCapacityDigests(tb, report, workloadDurationSeconds)

	seenThresholds := make(map[string]struct{}, len(report.Thresholds))
	for _, threshold := range report.Thresholds {
		_, duplicate := seenThresholds[threshold.Metric]
		require.False(tb, duplicate, threshold.Metric)
		seenThresholds[threshold.Metric] = struct{}{}
		require.Equal(tb, tournamentCapacityThresholdStatus(threshold.Comparator, threshold.Limit, threshold.Observed),
			threshold.Status, threshold.Metric)
	}
	for _, metric := range []string{
		"failed_operations",
		"correctness_failures",
		"latency_p99",
		"throughput",
		"scheduling_lag_max",
		"reconnect_failures",
		"goroutines_max",
		"heap_alloc_bytes_max",
		"database_connections_max",
	} {
		require.Contains(tb, seenThresholds, metric)
	}
}

func assertTournamentCapacityDigests(
	tb testing.TB,
	report tournamentCapacityReport,
	workloadDurationSeconds int64,
) {
	tb.Helper()
	expectedBuildDigest := tournamentCapacityDigest([]byte(strings.Join([]string{
		report.Identity.Revision.Commit,
		strconv.FormatBool(report.Identity.Revision.Dirty),
		report.Identity.Build.GoVersion,
		report.Identity.Build.Target,
	}, "\x00")))
	require.Equal(tb, expectedBuildDigest, report.Identity.Build.Digest)

	workloadBytes, err := json.Marshal(struct {
		Name              string `json:"name"`
		Profile           string `json:"profile"`
		DurationSeconds   int64  `json:"duration_seconds"`
		Participants      int    `json:"participants"`
		Games             int    `json:"games"`
		Workers           int    `json:"workers"`
		OperationInterval int64  `json:"operation_interval_ms"`
	}{
		Name:              report.Workload.Name,
		Profile:           report.Workload.Profile,
		DurationSeconds:   workloadDurationSeconds,
		Participants:      report.Load.Participants,
		Games:             report.Load.Games,
		Workers:           report.Load.Workers,
		OperationInterval: int64(report.Load.OperationIntervalMS),
	})
	require.NoError(tb, err)

	artifacts := make(map[string]string, len(report.Digests.Artifacts))
	for _, artifact := range report.Digests.Artifacts {
		_, duplicate := artifacts[artifact.Name]
		require.False(tb, duplicate, artifact.Name)
		artifacts[artifact.Name] = artifact.Digest
	}
	schemaBytes, err := os.ReadFile(tournamentCapacityTestdataPath(tb, tournamentCapacitySchemaName))
	require.NoError(tb, err)
	require.Equal(tb, tournamentCapacityDigest(schemaBytes), artifacts[tournamentCapacitySchemaName])
	require.Equal(tb, tournamentCapacityDigest(workloadBytes), artifacts[report.Workload.Name+"-workload"])

	require.Len(tb, report.Digests.Images, 1)
	expectedImage := tournamentCapacityRunningPostgresImage(tb)
	for _, image := range report.Digests.Images {
		require.Equal(tb, expectedImage.Reference, image.Reference)
		require.Equal(tb, "content", image.DigestScope)
		require.Equal(tb, expectedImage.Digest, image.Digest)
	}
	require.Equal(tb, report.EnvironmentEquivalence.ContainerImage, expectedImage.Reference)
}
