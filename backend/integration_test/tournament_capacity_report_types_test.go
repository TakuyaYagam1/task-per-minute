//go:build integration && capacity

package integration_test

import (
	"time"
)

const (
	tournamentCapacitySchemaName        = "tournament60-report.schema.json"
	tournamentCapacityReportSchema      = "tournament-capacity-report"
	tournamentCapacityReportVersion     = "1.0.0"
	tournamentCapacityPostgresImage     = "postgres:18-alpine"
	tournamentCapacityEquivalenceScope  = "repository-and-database-major"
	tournamentCapacityPoolLimit         = 50
	tournamentCapacityGoroutineLimit    = 128
	tournamentCapacityHeapLimitBytes    = 512 * 1024 * 1024
	tournamentCapacityOperationTimeout  = 5 * time.Second
	tournamentCapacityDeadlineBuffer    = 2 * time.Minute
	tournamentCapacityGitCommandTimeout = 5 * time.Second
	tournamentCapacityGitOutputLimit    = 4096
	tournamentCapacityGitExecutable     = "/nix/store/6f0qqak4qbcrbw4f750phr88c9yhpf5s-git-2.55.0/bin/git"
	tournamentCapacityGitExecutableSHA  = "d776b30d3f856aca98c8681a249cf8606fd14d4a9dc9debd358014522fa7d067"
)

type tournamentCapacityStatus string

const (
	tournamentCapacityPass tournamentCapacityStatus = "PASS"
	tournamentCapacityFail tournamentCapacityStatus = "FAIL"
)

type tournamentCapacityConfig struct {
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

type tournamentCapacityReport struct {
	Schema                 string                                   `json:"schema"`
	Version                string                                   `json:"version"`
	Identity               tournamentCapacityIdentity               `json:"identity"`
	Workload               tournamentCapacityWorkload               `json:"workload"`
	Window                 tournamentCapacityWindow                 `json:"window"`
	Load                   tournamentCapacityLoad                   `json:"load"`
	Operations             tournamentCapacityOperations             `json:"operations"`
	LatencyMS              tournamentCapacityLatency                `json:"latency_ms"`
	Throughput             tournamentCapacityThroughput             `json:"throughput"`
	Lag                    tournamentCapacityLag                    `json:"lag"`
	Reconnect              tournamentCapacityReconnect              `json:"reconnect"`
	Resources              tournamentCapacityResources              `json:"resources"`
	Database               tournamentCapacityDatabase               `json:"database"`
	Thresholds             []tournamentCapacityThreshold            `json:"thresholds"`
	Digests                tournamentCapacityDigests                `json:"digests"`
	EnvironmentEquivalence tournamentCapacityEnvironmentEquivalence `json:"environment_equivalence"`
}

type tournamentCapacityIdentity struct {
	Revision tournamentCapacityRevision `json:"revision"`
	Build    tournamentCapacityBuild    `json:"build"`
}

type tournamentCapacityRevision struct {
	Commit string `json:"commit"`
	Dirty  bool   `json:"dirty"`
}

type tournamentCapacityBuild struct {
	GoVersion string `json:"go_version"`
	Target    string `json:"target"`
	Digest    string `json:"digest"`
}

type tournamentCapacityWorkload struct {
	Name      string `json:"name"`
	Profile   string `json:"profile"`
	Operation string `json:"operation"`
}

type tournamentCapacityWindow struct {
	StartedAt       string  `json:"started_at"`
	EndedAt         string  `json:"ended_at"`
	DurationSeconds float64 `json:"duration_seconds"`
}

type tournamentCapacityLoad struct {
	Participants        int `json:"participants"`
	Games               int `json:"games"`
	Workers             int `json:"workers"`
	OperationIntervalMS int `json:"operation_interval_ms"`
}

type tournamentCapacityOperations struct {
	Total       int64                         `json:"total"`
	Succeeded   int64                         `json:"succeeded"`
	Failed      int64                         `json:"failed"`
	Correctness tournamentCapacityCorrectness `json:"correctness"`
}

type tournamentCapacityCorrectness struct {
	Checks int64                    `json:"checks"`
	Passed int64                    `json:"passed"`
	Failed int64                    `json:"failed"`
	Status tournamentCapacityStatus `json:"status"`
}

type tournamentCapacityLatency struct {
	P50      float64                  `json:"p50"`
	P95      float64                  `json:"p95"`
	P99      float64                  `json:"p99"`
	Max      float64                  `json:"max"`
	P99Limit float64                  `json:"p99_limit"`
	Status   tournamentCapacityStatus `json:"status"`
}

type tournamentCapacityThroughput struct {
	OperationsPerSecond float64                  `json:"operations_per_second"`
	Minimum             float64                  `json:"minimum"`
	Status              tournamentCapacityStatus `json:"status"`
}

type tournamentCapacityLag struct {
	Samples    int64                    `json:"samples"`
	P95MS      float64                  `json:"p95_ms"`
	MaxMS      float64                  `json:"max_ms"`
	MaxLimitMS float64                  `json:"max_limit_ms"`
	Status     tournamentCapacityStatus `json:"status"`
}

type tournamentCapacityReconnect struct {
	Attempts  int64                    `json:"attempts"`
	Succeeded int64                    `json:"succeeded"`
	Failed    int64                    `json:"failed"`
	Status    tournamentCapacityStatus `json:"status"`
}

type tournamentCapacityResources struct {
	Samples        int64                            `json:"samples"`
	Goroutines     tournamentCapacityBoundedInteger `json:"goroutines"`
	HeapAllocBytes tournamentCapacityBoundedInteger `json:"heap_alloc_bytes"`
}

type tournamentCapacityBoundedInteger struct {
	Limit       int64                    `json:"limit"`
	ObservedMax int64                    `json:"observed_max"`
	Status      tournamentCapacityStatus `json:"status"`
}

type tournamentCapacityDatabase struct {
	PoolLimit   int64                    `json:"pool_limit"`
	ObservedMax int64                    `json:"observed_max"`
	AcquiredEnd int64                    `json:"acquired_end"`
	IdleEnd     int64                    `json:"idle_end"`
	TotalEnd    int64                    `json:"total_end"`
	Status      tournamentCapacityStatus `json:"status"`
}

type tournamentCapacityThreshold struct {
	Metric     string                   `json:"metric"`
	Comparator string                   `json:"comparator"`
	Limit      float64                  `json:"limit"`
	Observed   float64                  `json:"observed"`
	Unit       string                   `json:"unit"`
	Status     tournamentCapacityStatus `json:"status"`
}

type tournamentCapacityDigests struct {
	Artifacts []tournamentCapacityArtifactDigest `json:"artifacts"`
	Images    []tournamentCapacityImageDigest    `json:"images"`
}

type tournamentCapacityArtifactDigest struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

type tournamentCapacityImageDigest struct {
	Reference   string `json:"reference"`
	Digest      string `json:"digest"`
	DigestScope string `json:"digest_scope"`
}

type tournamentCapacityEnvironmentEquivalence struct {
	Status               tournamentCapacityStatus `json:"status"`
	Scope                string                   `json:"scope"`
	ProductionRepository bool                     `json:"production_repository"`
	DatabaseEngine       string                   `json:"database_engine"`
	DatabaseMajor        int                      `json:"database_major"`
	MigrationEngine      string                   `json:"migration_engine"`
	ContainerImage       string                   `json:"container_image"`
	Differences          []string                 `json:"differences"`
}

type tournamentCapacitySummary struct {
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
