//go:build integration && capacity

package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/distribution/reference"
	dockerclient "github.com/moby/moby/client"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

const (
	arenaCapacitySchemaName        = "arena60-report.schema.json"
	arenaCapacityReportSchema      = "arena-capacity-report"
	arenaCapacityReportVersion     = "1.0.0"
	arenaCapacityPostgresImage     = "postgres:18-alpine"
	arenaCapacityEquivalenceScope  = "repository-and-database-major"
	arenaCapacityPoolLimit         = 50
	arenaCapacityGoroutineLimit    = 128
	arenaCapacityHeapLimitBytes    = 512 * 1024 * 1024
	arenaCapacityOperationTimeout  = 5 * time.Second
	arenaCapacityDeadlineBuffer    = 2 * time.Minute
	arenaCapacityGitCommandTimeout = 5 * time.Second
	arenaCapacityGitOutputLimit    = 4096
	arenaCapacityGitExecutable     = "/nix/store/6f0qqak4qbcrbw4f750phr88c9yhpf5s-git-2.55.0/bin/git"
	arenaCapacityGitExecutableSHA  = "d776b30d3f856aca98c8681a249cf8606fd14d4a9dc9debd358014522fa7d067"
)

type arenaCapacityStatus string

const (
	arenaCapacityPass arenaCapacityStatus = "PASS"
	arenaCapacityFail arenaCapacityStatus = "FAIL"
)

type arenaCapacityConfig struct {
	Name                 string
	Profile              string
	Duration             time.Duration
	Participants         int
	Games                int
	Workers              int
	OperationInterval    time.Duration
	MinimumThroughput    float64
	LatencyP99LimitMS    float64
	SchedulingLagLimitMS float64
}

type arenaCapacityReport struct {
	Schema                 string                              `json:"schema"`
	Version                string                              `json:"version"`
	Identity               arenaCapacityIdentity               `json:"identity"`
	Workload               arenaCapacityWorkload               `json:"workload"`
	Window                 arenaCapacityWindow                 `json:"window"`
	Load                   arenaCapacityLoad                   `json:"load"`
	Operations             arenaCapacityOperations             `json:"operations"`
	LatencyMS              arenaCapacityLatency                `json:"latency_ms"`
	Throughput             arenaCapacityThroughput             `json:"throughput"`
	Lag                    arenaCapacityLag                    `json:"lag"`
	Reconnect              arenaCapacityReconnect              `json:"reconnect"`
	Resources              arenaCapacityResources              `json:"resources"`
	Database               arenaCapacityDatabase               `json:"database"`
	Thresholds             []arenaCapacityThreshold            `json:"thresholds"`
	Digests                arenaCapacityDigests                `json:"digests"`
	EnvironmentEquivalence arenaCapacityEnvironmentEquivalence `json:"environment_equivalence"`
}

type arenaCapacityIdentity struct {
	Revision arenaCapacityRevision `json:"revision"`
	Build    arenaCapacityBuild    `json:"build"`
}

type arenaCapacityRevision struct {
	Commit string `json:"commit"`
	Dirty  bool   `json:"dirty"`
}

type arenaCapacityBuild struct {
	GoVersion string `json:"go_version"`
	Target    string `json:"target"`
	Digest    string `json:"digest"`
}

type arenaCapacityWorkload struct {
	Name      string `json:"name"`
	Profile   string `json:"profile"`
	Operation string `json:"operation"`
}

type arenaCapacityWindow struct {
	StartedAt       string  `json:"started_at"`
	EndedAt         string  `json:"ended_at"`
	DurationSeconds float64 `json:"duration_seconds"`
}

type arenaCapacityLoad struct {
	Participants        int `json:"participants"`
	Games               int `json:"games"`
	Workers             int `json:"workers"`
	OperationIntervalMS int `json:"operation_interval_ms"`
}

type arenaCapacityOperations struct {
	Total       int64                    `json:"total"`
	Succeeded   int64                    `json:"succeeded"`
	Failed      int64                    `json:"failed"`
	Correctness arenaCapacityCorrectness `json:"correctness"`
}

type arenaCapacityCorrectness struct {
	Checks int64               `json:"checks"`
	Passed int64               `json:"passed"`
	Failed int64               `json:"failed"`
	Status arenaCapacityStatus `json:"status"`
}

type arenaCapacityLatency struct {
	P50      float64             `json:"p50"`
	P95      float64             `json:"p95"`
	P99      float64             `json:"p99"`
	Max      float64             `json:"max"`
	P99Limit float64             `json:"p99_limit"`
	Status   arenaCapacityStatus `json:"status"`
}

type arenaCapacityThroughput struct {
	OperationsPerSecond float64             `json:"operations_per_second"`
	Minimum             float64             `json:"minimum"`
	Status              arenaCapacityStatus `json:"status"`
}

type arenaCapacityLag struct {
	Samples    int64               `json:"samples"`
	P95MS      float64             `json:"p95_ms"`
	MaxMS      float64             `json:"max_ms"`
	MaxLimitMS float64             `json:"max_limit_ms"`
	Status     arenaCapacityStatus `json:"status"`
}

type arenaCapacityReconnect struct {
	Attempts  int64               `json:"attempts"`
	Succeeded int64               `json:"succeeded"`
	Failed    int64               `json:"failed"`
	Status    arenaCapacityStatus `json:"status"`
}

type arenaCapacityResources struct {
	Samples        int64                       `json:"samples"`
	Goroutines     arenaCapacityBoundedInteger `json:"goroutines"`
	HeapAllocBytes arenaCapacityBoundedInteger `json:"heap_alloc_bytes"`
}

type arenaCapacityBoundedInteger struct {
	Limit       int64               `json:"limit"`
	ObservedMax int64               `json:"observed_max"`
	Status      arenaCapacityStatus `json:"status"`
}

type arenaCapacityDatabase struct {
	PoolLimit   int64               `json:"pool_limit"`
	ObservedMax int64               `json:"observed_max"`
	AcquiredEnd int64               `json:"acquired_end"`
	IdleEnd     int64               `json:"idle_end"`
	TotalEnd    int64               `json:"total_end"`
	Status      arenaCapacityStatus `json:"status"`
}

type arenaCapacityThreshold struct {
	Metric     string              `json:"metric"`
	Comparator string              `json:"comparator"`
	Limit      float64             `json:"limit"`
	Observed   float64             `json:"observed"`
	Unit       string              `json:"unit"`
	Status     arenaCapacityStatus `json:"status"`
}

type arenaCapacityDigests struct {
	Artifacts []arenaCapacityArtifactDigest `json:"artifacts"`
	Images    []arenaCapacityImageDigest    `json:"images"`
}

type arenaCapacityArtifactDigest struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

type arenaCapacityImageDigest struct {
	Reference   string `json:"reference"`
	Digest      string `json:"digest"`
	DigestScope string `json:"digest_scope"`
}

type arenaCapacityEnvironmentEquivalence struct {
	Status               arenaCapacityStatus `json:"status"`
	Scope                string              `json:"scope"`
	ProductionRepository bool                `json:"production_repository"`
	DatabaseEngine       string              `json:"database_engine"`
	DatabaseMajor        int                 `json:"database_major"`
	MigrationEngine      string              `json:"migration_engine"`
	ContainerImage       string              `json:"container_image"`
	Differences          []string            `json:"differences"`
}

type arenaCapacitySummary struct {
	StartedAt              time.Time
	EndedAt                time.Time
	Operations             int64
	Succeeded              int64
	Failed                 int64
	CorrectnessChecks      int64
	CorrectnessPassed      int64
	CorrectnessFailed      int64
	ReconnectAttempts      int64
	ReconnectSucceeded     int64
	ReconnectFailed        int64
	Latencies              []time.Duration
	SchedulingLags         []time.Duration
	ResourceSamples        int64
	MaxGoroutines          int64
	MaxHeapAllocBytes      int64
	MaxDatabaseConnections int64
	DatabaseAcquiredEnd    int64
	DatabaseIdleEnd        int64
	DatabaseTotalEnd       int64
}

func TestArenaCapacityReport(t *testing.T) {
	schemaPath := arenaCapacityTestdataPath(t, arenaCapacitySchemaName)
	fixturePath := arenaCapacityTestdataPath(t, "arena60-nominal.json")

	schema := loadArenaCapacitySchema(t, schemaPath)
	fixture, err := os.ReadFile(fixturePath)
	require.NoError(t, err)

	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(fixture))
	require.NoError(t, err)
	require.NoError(t, schema.Validate(document))

	var report arenaCapacityReport
	require.NoError(t, json.Unmarshal(fixture, &report))
	assertArenaCapacityReportSemantics(t, report)

	identity := arenaCapacityBuildIdentity(t)
	require.Len(t, identity.Revision.Commit, 40)
	require.True(t, arenaCapacityHex(identity.Revision.Commit))
	require.Equal(t, arenaCapacityDigest([]byte(strings.Join([]string{
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

func newArenaCapacityReport(
	t testing.TB,
	config arenaCapacityConfig,
	summary arenaCapacitySummary,
) arenaCapacityReport {
	t.Helper()

	durationSeconds := summary.EndedAt.Sub(summary.StartedAt).Seconds()
	latencyP50, latencyP95, latencyP99, latencyMax := arenaCapacityPercentiles(summary.Latencies)
	_, lagP95, _, lagMax := arenaCapacityPercentiles(summary.SchedulingLags)
	throughput := float64(summary.Succeeded) / durationSeconds

	correctnessStatus := arenaCapacityStatusFor(summary.CorrectnessFailed == 0)
	latencyStatus := arenaCapacityStatusFor(latencyP99 <= config.LatencyP99LimitMS)
	throughputStatus := arenaCapacityStatusFor(throughput >= config.MinimumThroughput)
	lagStatus := arenaCapacityStatusFor(lagMax <= config.SchedulingLagLimitMS)
	goroutineStatus := arenaCapacityStatusFor(summary.MaxGoroutines <= arenaCapacityGoroutineLimit)
	heapStatus := arenaCapacityStatusFor(summary.MaxHeapAllocBytes <= arenaCapacityHeapLimitBytes)
	databaseStatus := arenaCapacityStatusFor(summary.MaxDatabaseConnections <= arenaCapacityPoolLimit)
	reconnectStatus := arenaCapacityStatusFor(
		summary.ReconnectAttempts > 0 &&
			summary.ReconnectSucceeded > 0 &&
			summary.ReconnectFailed == 0,
	)

	identity := arenaCapacityBuildIdentity(t)
	schemaBytes, err := os.ReadFile(arenaCapacityTestdataPath(t, arenaCapacitySchemaName))
	require.NoError(t, err)
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
	require.NoError(t, err)

	report := arenaCapacityReport{
		Schema:   arenaCapacityReportSchema,
		Version:  arenaCapacityReportVersion,
		Identity: identity,
		Workload: arenaCapacityWorkload{
			Name:      config.Name,
			Profile:   config.Profile,
			Operation: "arena_game_repository_get",
		},
		Window: arenaCapacityWindow{
			StartedAt:       summary.StartedAt.UTC().Format(time.RFC3339Nano),
			EndedAt:         summary.EndedAt.UTC().Format(time.RFC3339Nano),
			DurationSeconds: durationSeconds,
		},
		Load: arenaCapacityLoad{
			Participants:        config.Participants,
			Games:               config.Games,
			Workers:             config.Workers,
			OperationIntervalMS: int(config.OperationInterval / time.Millisecond),
		},
		Operations: arenaCapacityOperations{
			Total:     summary.Operations,
			Succeeded: summary.Succeeded,
			Failed:    summary.Failed,
			Correctness: arenaCapacityCorrectness{
				Checks: summary.CorrectnessChecks,
				Passed: summary.CorrectnessPassed,
				Failed: summary.CorrectnessFailed,
				Status: correctnessStatus,
			},
		},
		LatencyMS: arenaCapacityLatency{
			P50:      latencyP50,
			P95:      latencyP95,
			P99:      latencyP99,
			Max:      latencyMax,
			P99Limit: config.LatencyP99LimitMS,
			Status:   latencyStatus,
		},
		Throughput: arenaCapacityThroughput{
			OperationsPerSecond: throughput,
			Minimum:             config.MinimumThroughput,
			Status:              throughputStatus,
		},
		Lag: arenaCapacityLag{
			Samples:    int64(len(summary.SchedulingLags)),
			P95MS:      lagP95,
			MaxMS:      lagMax,
			MaxLimitMS: config.SchedulingLagLimitMS,
			Status:     lagStatus,
		},
		Reconnect: arenaCapacityReconnect{
			Attempts:  summary.ReconnectAttempts,
			Succeeded: summary.ReconnectSucceeded,
			Failed:    summary.ReconnectFailed,
			Status:    reconnectStatus,
		},
		Resources: arenaCapacityResources{
			Samples: summary.ResourceSamples,
			Goroutines: arenaCapacityBoundedInteger{
				Limit:       arenaCapacityGoroutineLimit,
				ObservedMax: summary.MaxGoroutines,
				Status:      goroutineStatus,
			},
			HeapAllocBytes: arenaCapacityBoundedInteger{
				Limit:       arenaCapacityHeapLimitBytes,
				ObservedMax: summary.MaxHeapAllocBytes,
				Status:      heapStatus,
			},
		},
		Database: arenaCapacityDatabase{
			PoolLimit:   arenaCapacityPoolLimit,
			ObservedMax: summary.MaxDatabaseConnections,
			AcquiredEnd: summary.DatabaseAcquiredEnd,
			IdleEnd:     summary.DatabaseIdleEnd,
			TotalEnd:    summary.DatabaseTotalEnd,
			Status:      databaseStatus,
		},
		Digests: arenaCapacityDigests{
			Artifacts: []arenaCapacityArtifactDigest{
				{Name: arenaCapacitySchemaName, Digest: arenaCapacityDigest(schemaBytes)},
				{Name: config.Name + "-workload", Digest: arenaCapacityDigest(workloadBytes)},
			},
			Images: []arenaCapacityImageDigest{
				arenaCapacityRunningPostgresImage(t),
			},
		},
		EnvironmentEquivalence: arenaCapacityEnvironmentEquivalence{
			Status:               arenaCapacityPass,
			Scope:                arenaCapacityEquivalenceScope,
			ProductionRepository: true,
			DatabaseEngine:       "postgresql",
			DatabaseMajor:        18,
			MigrationEngine:      "goose",
			ContainerImage:       arenaCapacityPostgresImage,
			Differences:          []string{},
		},
	}
	report.Thresholds = []arenaCapacityThreshold{
		arenaCapacityLimit("failed_operations", "<=", 0, float64(summary.Failed), "count"),
		arenaCapacityLimit("correctness_failures", "<=", 0, float64(summary.CorrectnessFailed), "count"),
		arenaCapacityLimit("latency_p99", "<=", config.LatencyP99LimitMS, latencyP99, "milliseconds"),
		arenaCapacityLimit("throughput", ">=", config.MinimumThroughput, throughput, "operations_per_second"),
		arenaCapacityLimit("scheduling_lag_max", "<=", config.SchedulingLagLimitMS, lagMax, "milliseconds"),
		arenaCapacityLimit("reconnect_failures", "<=", 0, float64(summary.ReconnectFailed), "count"),
		arenaCapacityLimit("goroutines_max", "<=", arenaCapacityGoroutineLimit, float64(summary.MaxGoroutines), "count"),
		arenaCapacityLimit("heap_alloc_bytes_max", "<=", arenaCapacityHeapLimitBytes, float64(summary.MaxHeapAllocBytes), "bytes"),
		arenaCapacityLimit("database_connections_max", "<=", arenaCapacityPoolLimit, float64(summary.MaxDatabaseConnections), "count"),
	}
	return report
}

func writeArenaCapacityReport(t *testing.T, report arenaCapacityReport) {
	t.Helper()
	assertArenaCapacityReportSemantics(t, report)

	data, err := json.MarshalIndent(report, "", "  ")
	require.NoError(t, err)
	validateArenaCapacityJSON(t, data)

	name := report.Workload.Name + "-" + report.Workload.Profile + ".json"
	path := filepath.Join(t.ArtifactDir(), name)
	require.NoError(t, os.WriteFile(path, append(data, '\n'), 0o600))
	compact, err := json.Marshal(report)
	require.NoError(t, err)
	t.Logf("ARENA_CAPACITY_REPORT %s", compact)
	t.Logf("capacity report artifact: %s", name)
}

func assertArenaCapacityReportPass(t testing.TB, report arenaCapacityReport) {
	t.Helper()
	assertArenaCapacityReportSemantics(t, report)
	require.Equal(t, arenaCapacityPass, report.Operations.Correctness.Status)
	require.Equal(t, arenaCapacityPass, report.LatencyMS.Status)
	require.Equal(t, arenaCapacityPass, report.Throughput.Status)
	require.Equal(t, arenaCapacityPass, report.Lag.Status)
	require.Equal(t, arenaCapacityPass, report.Reconnect.Status)
	require.Equal(t, arenaCapacityPass, report.Resources.Goroutines.Status)
	require.Equal(t, arenaCapacityPass, report.Resources.HeapAllocBytes.Status)
	require.Equal(t, arenaCapacityPass, report.Database.Status)
	require.Equal(t, arenaCapacityPass, report.EnvironmentEquivalence.Status)
	for _, threshold := range report.Thresholds {
		require.Equal(t, arenaCapacityPass, threshold.Status, threshold.Metric)
	}
}

func assertArenaCapacityReportSemantics(t testing.TB, report arenaCapacityReport) {
	t.Helper()
	require.Equal(t, arenaCapacityReportSchema, report.Schema)
	require.Equal(t, arenaCapacityReportVersion, report.Version)
	require.Equal(t, report.Operations.Total, report.Operations.Succeeded+report.Operations.Failed)
	require.Equal(t, report.Operations.Correctness.Checks,
		report.Operations.Correctness.Passed+report.Operations.Correctness.Failed)
	require.Equal(t, arenaCapacityStatusFor(report.Operations.Correctness.Failed == 0),
		report.Operations.Correctness.Status)
	require.LessOrEqual(t, report.LatencyMS.P50, report.LatencyMS.P95)
	require.LessOrEqual(t, report.LatencyMS.P95, report.LatencyMS.P99)
	require.LessOrEqual(t, report.LatencyMS.P99, report.LatencyMS.Max)
	require.Equal(t, arenaCapacityStatusFor(report.LatencyMS.P99 <= report.LatencyMS.P99Limit),
		report.LatencyMS.Status)
	require.Equal(t,
		arenaCapacityStatusFor(report.Throughput.OperationsPerSecond >= report.Throughput.Minimum),
		report.Throughput.Status)
	require.Equal(t, arenaCapacityStatusFor(report.Lag.MaxMS <= report.Lag.MaxLimitMS), report.Lag.Status)
	require.Positive(t, report.Reconnect.Attempts)
	require.GreaterOrEqual(t, report.Reconnect.Succeeded, int64(0))
	require.EqualValues(t, report.Load.Games, report.Reconnect.Attempts)
	require.Equal(t, report.Reconnect.Attempts, report.Reconnect.Succeeded+report.Reconnect.Failed)
	require.Equal(t,
		arenaCapacityStatusFor(
			report.Reconnect.Attempts > 0 &&
				report.Reconnect.Succeeded > 0 &&
				report.Reconnect.Failed == 0,
		),
		report.Reconnect.Status,
	)
	require.Equal(t,
		arenaCapacityStatusFor(report.Resources.Goroutines.ObservedMax <= report.Resources.Goroutines.Limit),
		report.Resources.Goroutines.Status)
	require.Equal(t,
		arenaCapacityStatusFor(report.Resources.HeapAllocBytes.ObservedMax <= report.Resources.HeapAllocBytes.Limit),
		report.Resources.HeapAllocBytes.Status)
	require.LessOrEqual(t, report.Database.ObservedMax, report.Database.PoolLimit)
	require.Equal(t,
		arenaCapacityStatusFor(report.Database.ObservedMax <= report.Database.PoolLimit),
		report.Database.Status)
	require.Equal(t, arenaCapacityEquivalenceScope, report.EnvironmentEquivalence.Scope)
	require.True(t, report.EnvironmentEquivalence.ProductionRepository)
	require.Equal(t, "postgresql", report.EnvironmentEquivalence.DatabaseEngine)
	require.Equal(t, 18, report.EnvironmentEquivalence.DatabaseMajor)
	require.Equal(t, "goose", report.EnvironmentEquivalence.MigrationEngine)
	require.Equal(t, arenaCapacityPostgresImage, report.EnvironmentEquivalence.ContainerImage)
	require.Equal(t, arenaCapacityStatusFor(
		report.EnvironmentEquivalence.ProductionRepository &&
			report.EnvironmentEquivalence.DatabaseEngine == "postgresql" &&
			report.EnvironmentEquivalence.DatabaseMajor == 18 &&
			len(report.EnvironmentEquivalence.Differences) == 0,
	),
		report.EnvironmentEquivalence.Status)

	startedAt, err := time.Parse(time.RFC3339Nano, report.Window.StartedAt)
	require.NoError(t, err)
	endedAt, err := time.Parse(time.RFC3339Nano, report.Window.EndedAt)
	require.NoError(t, err)
	require.True(t, endedAt.After(startedAt))
	require.InDelta(t, endedAt.Sub(startedAt).Seconds(), report.Window.DurationSeconds, 0.001)

	var workloadDurationSeconds int64
	switch report.Workload.Profile {
	case "nominal":
		require.Equal(t, "arena60", report.Workload.Name)
		require.Equal(t, 16, report.Load.Participants)
		require.Equal(t, 8, report.Load.Games)
		require.Equal(t, 4, report.Load.Workers)
		require.GreaterOrEqual(t, report.Window.DurationSeconds, 3600.0)
		workloadDurationSeconds = 3600
	case "peak":
		require.Equal(t, "arena45", report.Workload.Name)
		require.Equal(t, 16, report.Load.Participants)
		require.Equal(t, 8, report.Load.Games)
		require.Equal(t, 8, report.Load.Workers)
		require.GreaterOrEqual(t, report.Window.DurationSeconds, 2700.0)
		workloadDurationSeconds = 2700
	default:
		t.Fatalf("unknown capacity profile %q", report.Workload.Profile)
	}
	assertArenaCapacityDigests(t, report, workloadDurationSeconds)

	seenThresholds := make(map[string]struct{}, len(report.Thresholds))
	for _, threshold := range report.Thresholds {
		_, duplicate := seenThresholds[threshold.Metric]
		require.False(t, duplicate, threshold.Metric)
		seenThresholds[threshold.Metric] = struct{}{}
		require.Equal(t, arenaCapacityThresholdStatus(threshold.Comparator, threshold.Limit, threshold.Observed),
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
		require.Contains(t, seenThresholds, metric)
	}
}

func assertArenaCapacityDigests(
	t testing.TB,
	report arenaCapacityReport,
	workloadDurationSeconds int64,
) {
	t.Helper()
	expectedBuildDigest := arenaCapacityDigest([]byte(strings.Join([]string{
		report.Identity.Revision.Commit,
		strconv.FormatBool(report.Identity.Revision.Dirty),
		report.Identity.Build.GoVersion,
		report.Identity.Build.Target,
	}, "\x00")))
	require.Equal(t, expectedBuildDigest, report.Identity.Build.Digest)

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
	require.NoError(t, err)

	artifacts := make(map[string]string, len(report.Digests.Artifacts))
	for _, artifact := range report.Digests.Artifacts {
		_, duplicate := artifacts[artifact.Name]
		require.False(t, duplicate, artifact.Name)
		artifacts[artifact.Name] = artifact.Digest
	}
	schemaBytes, err := os.ReadFile(arenaCapacityTestdataPath(t, arenaCapacitySchemaName))
	require.NoError(t, err)
	require.Equal(t, arenaCapacityDigest(schemaBytes), artifacts[arenaCapacitySchemaName])
	require.Equal(t, arenaCapacityDigest(workloadBytes), artifacts[report.Workload.Name+"-workload"])

	require.Len(t, report.Digests.Images, 1)
	expectedImage := arenaCapacityRunningPostgresImage(t)
	for _, image := range report.Digests.Images {
		require.Equal(t, expectedImage.Reference, image.Reference)
		require.Equal(t, "content", image.DigestScope)
		require.Equal(t, expectedImage.Digest, image.Digest)
	}
	require.Equal(t, report.EnvironmentEquivalence.ContainerImage, expectedImage.Reference)
}

func validateArenaCapacityJSON(t testing.TB, data []byte) {
	t.Helper()
	schema := loadArenaCapacitySchema(t, arenaCapacityTestdataPath(t, arenaCapacitySchemaName))
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	require.NoError(t, err)
	require.NoError(t, schema.Validate(document))
}

func loadArenaCapacitySchema(t testing.TB, path string) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	schema, err := compiler.Compile(path)
	require.NoError(t, err)
	return schema
}

func arenaCapacityPercentiles(samples []time.Duration) (float64, float64, float64, float64) {
	if len(samples) == 0 {
		return 0, 0, 0, 0
	}
	ordered := append([]time.Duration(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	value := func(percentile float64) float64 {
		index := int(float64(len(ordered))*percentile+0.999999999) - 1
		if index < 0 {
			index = 0
		}
		if index >= len(ordered) {
			index = len(ordered) - 1
		}
		return float64(ordered[index]) / float64(time.Millisecond)
	}
	return value(0.50), value(0.95), value(0.99), value(1)
}

func arenaCapacityLimit(metric, comparator string, limit, observed float64, unit string) arenaCapacityThreshold {
	return arenaCapacityThreshold{
		Metric:     metric,
		Comparator: comparator,
		Limit:      limit,
		Observed:   observed,
		Unit:       unit,
		Status:     arenaCapacityThresholdStatus(comparator, limit, observed),
	}
}

func arenaCapacityThresholdStatus(comparator string, limit, observed float64) arenaCapacityStatus {
	switch comparator {
	case "<=":
		return arenaCapacityStatusFor(observed <= limit)
	case ">=":
		return arenaCapacityStatusFor(observed >= limit)
	default:
		return arenaCapacityFail
	}
}

func arenaCapacityStatusFor(ok bool) arenaCapacityStatus {
	if ok {
		return arenaCapacityPass
	}
	return arenaCapacityFail
}

func arenaCapacityBuildIdentity(t testing.TB) arenaCapacityIdentity {
	t.Helper()
	commit := strings.ToLower(arenaCapacityGitOutput(t, "rev-parse", "--verify", "HEAD^{commit}"))
	require.Len(t, commit, 40)
	require.True(t, arenaCapacityHex(commit))

	dirty := arenaCapacityGitOutput(t, "status", "--porcelain=v1", "--untracked-files=all", "--") != ""
	goVersion := runtime.Version()
	target := runtime.GOOS + "/" + runtime.GOARCH
	buildDigest := arenaCapacityDigest([]byte(strings.Join([]string{
		commit,
		strconv.FormatBool(dirty),
		goVersion,
		target,
	}, "\x00")))
	return arenaCapacityIdentity{
		Revision: arenaCapacityRevision{Commit: commit, Dirty: dirty},
		Build: arenaCapacityBuild{
			GoVersion: goVersion,
			Target:    target,
			Digest:    buildDigest,
		},
	}
}

type arenaCapacityBoundedOutput struct {
	data     []byte
	exceeded bool
}

func (output *arenaCapacityBoundedOutput) Write(data []byte) (int, error) {
	written := len(data)
	remaining := arenaCapacityGitOutputLimit - len(output.data)
	if remaining <= 0 {
		output.exceeded = true
		return written, nil
	}
	if len(data) > remaining {
		output.exceeded = true
		data = data[:remaining]
	}
	output.data = append(output.data, data...)
	return written, nil
}

func (output *arenaCapacityBoundedOutput) String() string {
	return string(output.data)
}

func arenaCapacityGitOutput(t testing.TB, args ...string) string {
	t.Helper()
	gitInfo, err := os.Lstat(arenaCapacityGitExecutable)
	require.NoError(t, err)
	require.True(t, gitInfo.Mode().IsRegular())
	require.Zero(t, gitInfo.Mode()&os.ModeSymlink)
	require.Equal(t, os.FileMode(0o555), gitInfo.Mode().Perm())
	gitBytes, err := os.ReadFile(arenaCapacityGitExecutable)
	require.NoError(t, err)
	require.Equal(t, "sha256:"+arenaCapacityGitExecutableSHA, arenaCapacityDigest(gitBytes))

	ctx, cancel := context.WithTimeout(context.Background(), arenaCapacityGitCommandTimeout)
	defer cancel()

	command := exec.CommandContext(ctx, arenaCapacityGitExecutable, args...)
	command.Dir = arenaCapacityRepositoryRoot(t)
	command.Env = []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
		"HOME=/nonexistent",
		"LANG=C",
		"LC_ALL=C",
		"PATH=/nonexistent",
	}
	var stdout arenaCapacityBoundedOutput
	var stderr arenaCapacityBoundedOutput
	command.Stdout = &stdout
	command.Stderr = &stderr
	err = command.Run()
	if ctx.Err() != nil {
		require.NoError(t, ctx.Err(), "bounded git command timed out")
	}
	require.False(t, stdout.exceeded, "git stdout exceeded %d bytes", arenaCapacityGitOutputLimit)
	require.False(t, stderr.exceeded, "git stderr exceeded %d bytes", arenaCapacityGitOutputLimit)
	require.NoErrorf(t, err, "git %s: %s", strings.Join(args, " "), strings.TrimSpace(stderr.String()))
	return strings.TrimSpace(stdout.String())
}

func arenaCapacityRepositoryRoot(t testing.TB) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
}

func arenaCapacityDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", digest)
}

func arenaCapacityRunningPostgresImage(t testing.TB) arenaCapacityImageDigest {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), arenaCapacityOperationTimeout)
	defer cancel()
	require.NotNil(t, sharedPool)
	require.NoError(t, sharedPool.Ping(ctx))
	poolConfig := sharedPool.Config().ConnConfig
	require.NotEmpty(t, strings.TrimSpace(poolConfig.Host))
	require.NotZero(t, poolConfig.Port)

	provider, err := testcontainers.NewDockerProvider()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Close()) })
	containers, err := provider.Client().ContainerList(ctx, dockerclient.ContainerListOptions{})
	require.NoError(t, err)

	matchingIDs := make([]string, 0, 1)
	for _, candidate := range containers.Items {
		if string(candidate.State) != "running" {
			continue
		}
		for _, published := range candidate.Ports {
			if published.PrivatePort == 5432 &&
				published.PublicPort == poolConfig.Port &&
				published.Type == "tcp" &&
				arenaCapacityMappedHostMatches(poolConfig.Host, published.IP) {
				matchingIDs = append(matchingIDs, candidate.ID)
				break
			}
		}
	}
	require.Lenf(t, matchingIDs, 1,
		"expected exactly one running container mapped to sharedPool endpoint %s:%d",
		poolConfig.Host, poolConfig.Port)

	inspection, err := provider.Client().ContainerInspect(
		ctx,
		matchingIDs[0],
		dockerclient.ContainerInspectOptions{},
	)
	require.NoError(t, err)
	container := inspection.Container
	require.Equal(t, matchingIDs[0], container.ID)
	require.NotNil(t, container.State)
	require.True(t, container.State.Running)
	require.Equal(t, "running", string(container.State.Status))
	require.NotNil(t, container.Config)
	require.Equal(t,
		arenaCapacityNormalizedImageReference(t, arenaCapacityPostgresImage),
		arenaCapacityNormalizedImageReference(t, container.Config.Image),
		"running database container was not created from the requested capacity image reference")
	require.True(t, strings.HasPrefix(container.Image, "sha256:"))
	require.Len(t, container.Image, len("sha256:")+64)
	require.True(t, arenaCapacityHex(strings.TrimPrefix(container.Image, "sha256:")))

	return arenaCapacityImageDigest{
		Reference:   arenaCapacityPostgresImage,
		Digest:      container.Image,
		DigestScope: "content",
	}
}

func arenaCapacityNormalizedImageReference(t testing.TB, value string) string {
	t.Helper()
	named, err := reference.ParseNormalizedNamed(value)
	require.NoError(t, err)
	return reference.TagNameOnly(named).String()
}

func arenaCapacityMappedHostMatches(poolHost string, publishedHost netip.Addr) bool {
	if !publishedHost.IsValid() || publishedHost.IsUnspecified() {
		return true
	}

	poolHost = strings.TrimSpace(poolHost)
	poolHost = strings.TrimPrefix(poolHost, "[")
	poolHost = strings.TrimSuffix(poolHost, "]")
	if zoneIndex := strings.LastIndexByte(poolHost, '%'); zoneIndex >= 0 {
		poolHost = poolHost[:zoneIndex]
	}
	poolAddress, err := netip.ParseAddr(poolHost)
	if err == nil {
		return poolAddress.Unmap() == publishedHost.Unmap()
	}
	return strings.EqualFold(poolHost, "localhost") && publishedHost.IsLoopback()
}

func arenaCapacityHex(value string) bool {
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func arenaCapacityTestdataPath(t testing.TB, name string) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Join(filepath.Dir(sourceFile), "..", "testdata", "capacity", name)
}
