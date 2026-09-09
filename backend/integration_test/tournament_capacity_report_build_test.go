//go:build integration && capacity

package integration_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
)

func TestTournamentCapacityReport(t *testing.T) {
	schemaPath := tournamentCapacityTestdataPath(t, tournamentCapacitySchemaName)
	fixturePath := tournamentCapacityTestdataPath(t, "tournament60-nominal.json")

	schema := loadTournamentCapacitySchema(t, schemaPath)
	fixture, err := os.ReadFile(fixturePath)
	require.NoError(t, err)

	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(fixture))
	require.NoError(t, err)
	require.NoError(t, schema.Validate(document))

	var report tournamentCapacityReport
	require.NoError(t, json.Unmarshal(fixture, &report))
	assertTournamentCapacityReportSemantics(t, report)

	identity := tournamentCapacityBuildIdentity(t)
	require.Len(t, identity.Revision.Commit, 40)
	require.True(t, tournamentCapacityHex(identity.Revision.Commit))
	require.Equal(t, tournamentCapacityDigest([]byte(strings.Join([]string{
		identity.Revision.Commit,
		strconv.FormatBool(identity.Revision.Dirty),
		identity.Build.GoVersion,
		identity.Build.Target,
	}, "\x00"))), identity.Build.Digest)

	object, ok := document.(map[string]any)
	require.True(t, ok)
	object["unexpected"] = true
	require.Error(t, schema.Validate(object), "strict schema must reject undeclared fields")

	for _, field := range []string{"identity", "reconnect", "thresholds"} {
		var incomplete map[string]any
		require.NoError(t, json.Unmarshal(fixture, &incomplete))
		delete(incomplete, field)
		require.Errorf(t, schema.Validate(incomplete), "schema accepted missing %s", field)
	}
	var missingP95 map[string]any
	require.NoError(t, json.Unmarshal(fixture, &missingP95))
	delete(missingP95["latency_ms"].(map[string]any), "p95")
	require.Error(t, schema.Validate(missingP95), "schema accepted missing latency p95")
	var missingImages map[string]any
	require.NoError(t, json.Unmarshal(fixture, &missingImages))
	delete(missingImages["digests"].(map[string]any), "images")
	require.Error(t, schema.Validate(missingImages), "schema accepted missing image digests")
	var missingEquivalenceScope map[string]any
	require.NoError(t, json.Unmarshal(fixture, &missingEquivalenceScope))
	delete(missingEquivalenceScope["environment_equivalence"].(map[string]any), "scope")
	require.Error(t, schema.Validate(missingEquivalenceScope), "schema accepted missing equivalence scope")
	var widenedEquivalenceScope map[string]any
	require.NoError(t, json.Unmarshal(fixture, &widenedEquivalenceScope))
	widenedEquivalenceScope["environment_equivalence"].(map[string]any)["scope"] = "release-equivalent"
	require.Error(t, schema.Validate(widenedEquivalenceScope), "schema accepted an unbounded equivalence claim")
}

func newTournamentCapacityReport(
	tb testing.TB,
	config tournamentCapacityConfig,
	summary tournamentCapacitySummary,
) tournamentCapacityReport {
	tb.Helper()

	durationSeconds := summary.EndedAt.Sub(summary.StartedAt).Seconds()
	latencyP50, latencyP95, latencyP99, latencyMax := tournamentCapacityPercentiles(summary.Latencies)
	_, lagP95, _, lagMax := tournamentCapacityPercentiles(summary.SchedulingLags)
	throughput := float64(summary.Succeeded) / durationSeconds

	correctnessStatus := tournamentCapacityStatusFor(summary.CorrectnessFailed == 0)
	latencyStatus := tournamentCapacityStatusFor(latencyP99 <= config.LatencyP99LimitMS)
	throughputStatus := tournamentCapacityStatusFor(throughput >= config.MinimumThroughput)
	lagStatus := tournamentCapacityStatusFor(lagMax <= config.SchedulingLagLimitMS)
	goroutineStatus := tournamentCapacityStatusFor(summary.MaxGoroutines <= tournamentCapacityGoroutineLimit)
	heapStatus := tournamentCapacityStatusFor(summary.MaxHeapAllocBytes <= tournamentCapacityHeapLimitBytes)
	databaseStatus := tournamentCapacityStatusFor(summary.MaxDatabaseConnections <= tournamentCapacityPoolLimit)
	reconnectStatus := tournamentCapacityStatusFor(
		summary.ReconnectAttempts > 0 &&
			summary.ReconnectSucceeded > 0 &&
			summary.ReconnectFailed == 0,
	)

	identity := tournamentCapacityBuildIdentity(tb)
	schemaBytes, err := os.ReadFile(tournamentCapacityTestdataPath(tb, tournamentCapacitySchemaName))
	require.NoError(tb, err)
	workloadBytes, err := json.Marshal(struct {
		Name              string `json:"name"`
		Profile           string `json:"profile"`
		DurationSeconds   int64  `json:"duration_seconds"`
		Participants      int    `json:"participants"`
		Games             int    `json:"games"`
		Workers           int    `json:"workers"`
		OperationInterval int64  `json:"operation_interval_ms"`
	}{
		Name:              config.Name,
		Profile:           config.Profile,
		DurationSeconds:   int64(config.Duration / time.Second),
		Participants:      config.Participants,
		Games:             config.Games,
		Workers:           config.Workers,
		OperationInterval: config.OperationInterval.Milliseconds(),
	})
	require.NoError(tb, err)

	report := tournamentCapacityReport{
		Schema:   tournamentCapacityReportSchema,
		Version:  tournamentCapacityReportVersion,
		Identity: identity,
		Workload: tournamentCapacityWorkload{
			Name:      config.Name,
			Profile:   config.Profile,
			Operation: "game_repository_get",
		},
		Window: tournamentCapacityWindow{
			StartedAt:       summary.StartedAt.UTC().Format(time.RFC3339Nano),
			EndedAt:         summary.EndedAt.UTC().Format(time.RFC3339Nano),
			DurationSeconds: durationSeconds,
		},
		Load: tournamentCapacityLoad{
			Participants:        config.Participants,
			Games:               config.Games,
			Workers:             config.Workers,
			OperationIntervalMS: int(config.OperationInterval / time.Millisecond),
		},
		Operations: tournamentCapacityOperations{
			Total:     summary.Operations,
			Succeeded: summary.Succeeded,
			Failed:    summary.Failed,
			Correctness: tournamentCapacityCorrectness{
				Checks: summary.CorrectnessChecks,
				Passed: summary.CorrectnessPassed,
				Failed: summary.CorrectnessFailed,
				Status: correctnessStatus,
			},
		},
		LatencyMS: tournamentCapacityLatency{
			P50:      latencyP50,
			P95:      latencyP95,
			P99:      latencyP99,
			Max:      latencyMax,
			P99Limit: config.LatencyP99LimitMS,
			Status:   latencyStatus,
		},
		Throughput: tournamentCapacityThroughput{
			OperationsPerSecond: throughput,
			Minimum:             config.MinimumThroughput,
			Status:              throughputStatus,
		},
		Lag: tournamentCapacityLag{
			Samples:    int64(len(summary.SchedulingLags)),
			P95MS:      lagP95,
			MaxMS:      lagMax,
			MaxLimitMS: config.SchedulingLagLimitMS,
			Status:     lagStatus,
		},
		Reconnect: tournamentCapacityReconnect{
			Attempts:  summary.ReconnectAttempts,
			Succeeded: summary.ReconnectSucceeded,
			Failed:    summary.ReconnectFailed,
			Status:    reconnectStatus,
		},
		Resources: tournamentCapacityResources{
			Samples: summary.ResourceSamples,
			Goroutines: tournamentCapacityBoundedInteger{
				Limit:       tournamentCapacityGoroutineLimit,
				ObservedMax: summary.MaxGoroutines,
				Status:      goroutineStatus,
			},
			HeapAllocBytes: tournamentCapacityBoundedInteger{
				Limit:       tournamentCapacityHeapLimitBytes,
				ObservedMax: summary.MaxHeapAllocBytes,
				Status:      heapStatus,
			},
		},
		Database: tournamentCapacityDatabase{
			PoolLimit:   tournamentCapacityPoolLimit,
			ObservedMax: summary.MaxDatabaseConnections,
			AcquiredEnd: summary.DatabaseAcquiredEnd,
			IdleEnd:     summary.DatabaseIdleEnd,
			TotalEnd:    summary.DatabaseTotalEnd,
			Status:      databaseStatus,
		},
		Digests: tournamentCapacityDigests{
			Artifacts: []tournamentCapacityArtifactDigest{
				{Name: tournamentCapacitySchemaName, Digest: tournamentCapacityDigest(schemaBytes)},
				{Name: config.Name + "-workload", Digest: tournamentCapacityDigest(workloadBytes)},
			},
			Images: []tournamentCapacityImageDigest{
				tournamentCapacityRunningPostgresImage(tb),
			},
		},
		EnvironmentEquivalence: tournamentCapacityEnvironmentEquivalence{
			Status:               tournamentCapacityPass,
			Scope:                tournamentCapacityEquivalenceScope,
			ProductionRepository: true,
			DatabaseEngine:       "postgresql",
			DatabaseMajor:        18,
			MigrationEngine:      "goose",
			ContainerImage:       tournamentCapacityPostgresImage,
			Differences:          []string{},
		},
	}
	report.Thresholds = []tournamentCapacityThreshold{
		tournamentCapacityLimit("failed_operations", "<=", 0, float64(summary.Failed), "count"),
		tournamentCapacityLimit("correctness_failures", "<=", 0, float64(summary.CorrectnessFailed), "count"),
		tournamentCapacityLimit("latency_p99", "<=", config.LatencyP99LimitMS, latencyP99, "milliseconds"),
		tournamentCapacityLimit("throughput", ">=", config.MinimumThroughput, throughput, "operations_per_second"),
		tournamentCapacityLimit("scheduling_lag_max", "<=", config.SchedulingLagLimitMS, lagMax, "milliseconds"),
		tournamentCapacityLimit("reconnect_failures", "<=", 0, float64(summary.ReconnectFailed), "count"),
		tournamentCapacityLimit("goroutines_max", "<=", tournamentCapacityGoroutineLimit, float64(summary.MaxGoroutines), "count"),
		tournamentCapacityLimit("heap_alloc_bytes_max", "<=", tournamentCapacityHeapLimitBytes, float64(summary.MaxHeapAllocBytes), "bytes"),
		tournamentCapacityLimit("database_connections_max", "<=", tournamentCapacityPoolLimit, float64(summary.MaxDatabaseConnections), "count"),
	}
	return report
}
