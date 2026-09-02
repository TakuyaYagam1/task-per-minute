package observability

import (
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	arenaMetricNamespace = "tpm"
	arenaMetricSubsystem = "arena"

	arenaMetricOperationLifecycle  = "lifecycle"
	arenaMetricOperationWave       = "wave"
	arenaMetricOperationGame       = "game"
	arenaMetricOperationSubmission = "submission"
	arenaMetricOperationReconnect  = "reconnect"
	arenaMetricOperationReserve    = "reserve"
	arenaMetricOperationGolden     = "golden"
	arenaMetricOperationRecovery   = "recovery"
	arenaMetricOperationDeadline   = "deadline"
	arenaMetricOperationProjection = "projection"
	arenaMetricOperationRealtime   = "realtime"
	arenaMetricOperationHTTP       = "http"
	arenaMetricOperationOutbox     = "outbox"
	arenaMetricOperationOther      = "other"

	arenaMetricRecentLimit = 2048
)

var ErrArenaMetricInput = errors.New("invalid Arena metric input")

// ArenaMetrics owns one private Prometheus registry and accepts only bounded labels.
type ArenaMetrics struct {
	registry *prometheus.Registry

	operations *prometheus.CounterVec
	durations  *prometheus.HistogramVec
	lags       *prometheus.HistogramVec
	clockDrift *prometheus.GaugeVec

	mu          sync.Mutex
	recent      map[[sha256.Size]byte]struct{}
	recentOrder [][sha256.Size]byte
}

// NewArenaMetrics constructs an isolated registry so tests and application wiring
// cannot double-register collectors in the process-global registry.
func NewArenaMetrics() *ArenaMetrics {
	registry := prometheus.NewRegistry()
	metrics := &ArenaMetrics{
		registry: registry,
		operations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: arenaMetricNamespace,
			Subsystem: arenaMetricSubsystem,
			Name:      "operations_total",
			Help:      "Arena operations by bounded operation and outcome.",
		}, []string{"operation", "outcome"}),
		durations: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: arenaMetricNamespace,
			Subsystem: arenaMetricSubsystem,
			Name:      "operation_duration_seconds",
			Help:      "Arena operation duration in seconds by bounded operation and outcome.",
			Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"operation", "outcome"}),
		lags: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: arenaMetricNamespace,
			Subsystem: arenaMetricSubsystem,
			Name:      "lag_seconds",
			Help:      "Arena deadline, delivery, or projection lag in seconds.",
			Buckets:   []float64{0.001, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 15, 30, 60},
		}, []string{"kind"}),
		clockDrift: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: arenaMetricNamespace,
			Subsystem: arenaMetricSubsystem,
			Name:      "clock_drift_seconds",
			Help:      "Signed Arena server clock drift in seconds by bounded source.",
		}, []string{"source"}),
		recent: make(map[[sha256.Size]byte]struct{}, arenaMetricRecentLimit),
	}
	registry.MustRegister(metrics.operations, metrics.durations, metrics.lags, metrics.clockDrift)
	return metrics
}

// Gatherer exposes only this instance's collectors.
func (metrics *ArenaMetrics) Gatherer() prometheus.Gatherer {
	if metrics == nil {
		return nil
	}
	return metrics.registry
}

// ObserveArenaEvent implements ArenaEventObserver.
func (metrics *ArenaMetrics) ObserveArenaEvent(_ context.Context, event ArenaEvent) {
	_ = metrics.Record(event)
}

// Record validates and records one canonical Arena event.
func (metrics *ArenaMetrics) Record(event ArenaEvent) error {
	if metrics == nil || metrics.operations == nil || metrics.durations == nil {
		return ErrArenaMetricInput
	}
	validated, err := NewArenaEvent(ArenaEventInput(event))
	if err != nil {
		return ErrArenaMetricInput
	}
	if arenaMetricReplay(validated) {
		return nil
	}
	if validated.Outcome != ArenaOutcomeRetry && metrics.seen(validated) {
		return nil
	}
	operation := arenaMetricOperation(validated)
	metrics.operations.WithLabelValues(operation, validated.Outcome).Inc()
	metrics.durations.WithLabelValues(operation, validated.Outcome).Observe(validated.Duration.Seconds())
	return nil
}

func arenaMetricReplay(event ArenaEvent) bool {
	if event.Outcome != ArenaOutcomeSuccess {
		return false
	}
	switch event.ReasonCode {
	case "idempotent_replay", "already_committed":
		return true
	default:
		return false
	}
}

// ObserveLag records one nonnegative lag using a fixed label allowlist.
func (metrics *ArenaMetrics) ObserveLag(kind string, lag time.Duration) error {
	if metrics == nil || metrics.lags == nil || lag < 0 || !validArenaLagKind(kind) {
		return ErrArenaMetricInput
	}
	metrics.lags.WithLabelValues(kind).Observe(lag.Seconds())
	return nil
}

// ObserveArenaLag implements ArenaLagObserver and drops invalid input.
func (metrics *ArenaMetrics) ObserveArenaLag(kind string, lag time.Duration) {
	_ = metrics.ObserveLag(kind, lag)
}

// SetClockDrift records signed drift from one fixed server-side source.
func (metrics *ArenaMetrics) SetClockDrift(source string, drift time.Duration) error {
	if metrics == nil || metrics.clockDrift == nil || !validArenaClockSource(source) {
		return ErrArenaMetricInput
	}
	metrics.clockDrift.WithLabelValues(source).Set(drift.Seconds())
	return nil
}

func (metrics *ArenaMetrics) seen(event ArenaEvent) bool {
	digest := sha256.Sum256([]byte(strings.Join([]string{
		event.Event,
		event.Outcome,
		event.CorrelationID,
		event.CommandID,
		event.TournamentID,
		event.EntityKind,
		event.EntityID,
		event.Stage,
		event.Transition,
		event.ReasonCode,
		strconv.FormatInt(event.Revision, 10),
	}, "\x00")))

	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	if _, exists := metrics.recent[digest]; exists {
		return true
	}
	metrics.recent[digest] = struct{}{}
	metrics.recentOrder = append(metrics.recentOrder, digest)
	if len(metrics.recentOrder) > arenaMetricRecentLimit {
		oldest := metrics.recentOrder[0]
		delete(metrics.recent, oldest)
		metrics.recentOrder = metrics.recentOrder[1:]
	}
	return false
}

func arenaMetricOperation(event ArenaEvent) string {
	if operation := arenaMetricStageOperation(event.Stage); operation != "" {
		return operation
	}
	return arenaMetricEventOperation(event.Event)
}

func arenaMetricStageOperation(stage string) string {
	switch stage {
	case "tournament_lifecycle":
		return arenaMetricOperationLifecycle
	case "wave_start", "wave":
		return arenaMetricOperationWave
	case "game", "settlement":
		return arenaMetricOperationGame
	case "submission":
		return arenaMetricOperationSubmission
	case "reconnect":
		return arenaMetricOperationReconnect
	case "reserve":
		return arenaMetricOperationReserve
	case "golden":
		return arenaMetricOperationGolden
	case "startup_recovery", "recovery":
		return arenaMetricOperationRecovery
	case "deadline":
		return arenaMetricOperationDeadline
	case "projection":
		return arenaMetricOperationProjection
	case "participant", "public", "operator", "realtime":
		return arenaMetricOperationRealtime
	case "operator_mutation", "participant_mutation", "public_read", "unclassified":
		return arenaMetricOperationHTTP
	case "outbox":
		return arenaMetricOperationOutbox
	default:
		return ""
	}
}

func arenaMetricEventOperation(event string) string {
	switch event {
	case "arena.lifecycle.transition":
		return arenaMetricOperationLifecycle
	case "arena.command.wave_start":
		return arenaMetricOperationWave
	case "arena.recovery":
		return arenaMetricOperationRecovery
	case "arena.http":
		return arenaMetricOperationHTTP
	case "arena.websocket":
		return arenaMetricOperationRealtime
	case "arena.outbox":
		return arenaMetricOperationOutbox
	default:
		return arenaMetricOperationOther
	}
}

func validArenaLagKind(kind string) bool {
	switch kind {
	case "deadline", "delivery", "projection":
		return true
	default:
		return false
	}
}

func validArenaClockSource(source string) bool {
	switch source {
	case "database", "server":
		return true
	default:
		return false
	}
}

var _ ArenaEventObserver = (*ArenaMetrics)(nil)
var _ ArenaLagObserver = (*ArenaMetrics)(nil)
