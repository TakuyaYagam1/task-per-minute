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
	tournamentMetricNamespace = "tpm"
	tournamentMetricSubsystem = "tournament"

	tournamentMetricOperationLifecycle   = "lifecycle"
	tournamentMetricOperationMaintenance = "maintenance"
	tournamentMetricOperationPairing     = "pairing"
	tournamentMetricOperationWave        = "wave"
	tournamentMetricOperationGame        = "game"
	tournamentMetricOperationSettlement  = "settlement"
	tournamentMetricOperationReplay      = "replay"
	tournamentMetricOperationCorrection  = "correction"
	tournamentMetricOperationSubmission  = "submission"
	tournamentMetricOperationReconnect   = "reconnect"
	tournamentMetricOperationReserve     = "reserve"
	tournamentMetricOperationGolden      = "golden"
	tournamentMetricOperationRecovery    = "recovery"
	tournamentMetricOperationDeadline    = "deadline"
	tournamentMetricOperationProjection  = "projection"
	tournamentMetricOperationRealtime    = "realtime"
	tournamentMetricOperationDelivery    = "delivery"
	tournamentMetricOperationHTTP        = "http"
	tournamentMetricOperationOther       = "other"

	tournamentMetricRecentLimit = 2048
)

var ErrTournamentMetricInput = errors.New("invalid tournament metric input")

// TournamentMetrics owns one private Prometheus registry and accepts only bounded labels.
type TournamentMetrics struct {
	registry *prometheus.Registry

	operations *prometheus.CounterVec
	durations  *prometheus.HistogramVec
	lags       *prometheus.HistogramVec
	clockDrift *prometheus.GaugeVec
	eventDrops *prometheus.CounterVec
	queueLag   *prometheus.HistogramVec

	mu          sync.Mutex
	recent      map[[sha256.Size]byte]struct{}
	recentOrder [][sha256.Size]byte
}

// NewTournamentMetrics constructs an isolated registry so tests and application wiring
// cannot double-register collectors in the process-global registry.
func NewTournamentMetrics() *TournamentMetrics {
	registry := prometheus.NewRegistry()
	metrics := &TournamentMetrics{
		registry: registry,
		operations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: tournamentMetricNamespace,
			Subsystem: tournamentMetricSubsystem,
			Name:      "operations_total",
			Help:      "Tournament operations by bounded operation and outcome.",
		}, []string{"operation", "outcome"}),
		durations: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: tournamentMetricNamespace,
			Subsystem: tournamentMetricSubsystem,
			Name:      "operation_duration_seconds",
			Help:      "Tournament operation duration in seconds by bounded operation and outcome.",
			Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"operation", "outcome"}),
		lags: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: tournamentMetricNamespace,
			Subsystem: tournamentMetricSubsystem,
			Name:      "lag_seconds",
			Help:      "Tournament deadline, delivery, or projection lag in seconds.",
			Buckets:   []float64{0.001, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 15, 30, 60},
		}, []string{"kind"}),
		clockDrift: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: tournamentMetricNamespace,
			Subsystem: tournamentMetricSubsystem,
			Name:      "clock_drift_seconds",
			Help:      "Signed tournament server clock drift in seconds by bounded source.",
		}, []string{"source"}),
		eventDrops: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: tournamentMetricNamespace,
			Subsystem: tournamentMetricSubsystem,
			Name:      "observer_dropped_events_total",
			Help:      "Tournament observations dropped because the bounded observer queue was full.",
		}, []string{"reason"}),
		queueLag: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: tournamentMetricNamespace,
			Subsystem: tournamentMetricSubsystem,
			Name:      "observer_queue_lag_seconds",
			Help:      "Time spent waiting in the bounded tournament observer queue.",
			Buckets:   []float64{0.0001, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5},
		}, []string{"queue"}),
		recent: make(map[[sha256.Size]byte]struct{}, tournamentMetricRecentLimit),
	}
	registry.MustRegister(
		metrics.operations,
		metrics.durations,
		metrics.lags,
		metrics.clockDrift,
		metrics.eventDrops,
		metrics.queueLag,
	)
	return metrics
}

// Gatherer exposes only this instance's collectors.
func (metrics *TournamentMetrics) Gatherer() prometheus.Gatherer {
	if metrics == nil {
		return nil
	}
	return metrics.registry
}

// ObserveTournamentEvent implements TournamentEventObserver.
func (metrics *TournamentMetrics) ObserveTournamentEvent(_ context.Context, event TournamentEvent) {
	_ = metrics.Record(event)
}

// Record validates and records one canonical tournament event.
func (metrics *TournamentMetrics) Record(event TournamentEvent) error {
	if metrics == nil || metrics.operations == nil || metrics.durations == nil {
		return ErrTournamentMetricInput
	}
	validated, err := NewTournamentEvent(TournamentEventInput(event))
	if err != nil {
		return ErrTournamentMetricInput
	}
	if tournamentMetricReplay(validated) {
		return nil
	}
	if validated.Outcome != TournamentOutcomeRetry && metrics.seen(validated) {
		return nil
	}
	operation := tournamentMetricOperation(validated)
	metrics.operations.WithLabelValues(operation, validated.Outcome).Inc()
	metrics.durations.WithLabelValues(operation, validated.Outcome).Observe(validated.Duration.Seconds())
	return nil
}

func tournamentMetricReplay(event TournamentEvent) bool {
	if event.Outcome != TournamentOutcomeSuccess {
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
func (metrics *TournamentMetrics) ObserveLag(kind string, lag time.Duration) error {
	if metrics == nil || metrics.lags == nil || lag < 0 || !validTournamentLagKind(kind) {
		return ErrTournamentMetricInput
	}
	metrics.lags.WithLabelValues(kind).Observe(lag.Seconds())
	return nil
}

// ObserveTournamentLag implements TournamentLagObserver and drops invalid input.
func (metrics *TournamentMetrics) ObserveTournamentLag(kind string, lag time.Duration) {
	_ = metrics.ObserveLag(kind, lag)
}

func (metrics *TournamentMetrics) ObserveTournamentEventDrop() {
	if metrics == nil || metrics.eventDrops == nil {
		return
	}
	metrics.eventDrops.WithLabelValues("queue_full").Inc()
}

func (metrics *TournamentMetrics) ObserveTournamentEventQueueLag(lag time.Duration) {
	if metrics == nil || metrics.queueLag == nil || lag < 0 {
		return
	}
	metrics.queueLag.WithLabelValues("observer").Observe(lag.Seconds())
}

// SetClockDrift records signed drift from one fixed server-side source.
func (metrics *TournamentMetrics) SetClockDrift(source string, drift time.Duration) error {
	if metrics == nil || metrics.clockDrift == nil || !validTournamentClockSource(source) {
		return ErrTournamentMetricInput
	}
	metrics.clockDrift.WithLabelValues(source).Set(drift.Seconds())
	return nil
}

func (metrics *TournamentMetrics) seen(event TournamentEvent) bool {
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
	if len(metrics.recentOrder) > tournamentMetricRecentLimit {
		oldest := metrics.recentOrder[0]
		delete(metrics.recent, oldest)
		metrics.recentOrder = metrics.recentOrder[1:]
	}
	return false
}

func tournamentMetricOperation(event TournamentEvent) string {
	if operation := tournamentMetricStageOperation(event.Stage); operation != "" {
		return operation
	}
	return tournamentMetricEventOperation(event.Event)
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func tournamentMetricStageOperation(stage string) string {
	switch stage {
	case "tournament_lifecycle":
		return tournamentMetricOperationLifecycle
	case "maintenance":
		return tournamentMetricOperationMaintenance
	case "pairing":
		return tournamentMetricOperationPairing
	case "wave_start", "wave":
		return tournamentMetricOperationWave
	case "game":
		return tournamentMetricOperationGame
	case "settlement":
		return tournamentMetricOperationSettlement
	case "replay":
		return tournamentMetricOperationReplay
	case "correction":
		return tournamentMetricOperationCorrection
	case "submission":
		return tournamentMetricOperationSubmission
	case "readiness":
		return tournamentMetricOperationWave
	case "draft":
		return tournamentMetricOperationGame
	case "post_series":
		return tournamentMetricOperationSettlement
	case "reconnect":
		return tournamentMetricOperationReconnect
	case "reserve":
		return tournamentMetricOperationReserve
	case "golden":
		return tournamentMetricOperationGolden
	case "startup_recovery", "recovery":
		return tournamentMetricOperationRecovery
	case "deadline":
		return tournamentMetricOperationDeadline
	case "projection":
		return tournamentMetricOperationProjection
	case "delivery":
		return tournamentMetricOperationDelivery
	case "participant", "public", "operator", "realtime":
		return tournamentMetricOperationRealtime
	case "operator_mutation", "participant_mutation", "public_read", "unclassified":
		return tournamentMetricOperationHTTP
	default:
		return ""
	}
}

func tournamentMetricEventOperation(event string) string {
	switch event {
	case "tournament.lifecycle.transition":
		return tournamentMetricOperationLifecycle
	case "tournament.command.wave_start":
		return tournamentMetricOperationWave
	case "tournament.recovery":
		return tournamentMetricOperationRecovery
	case "tournament.http":
		return tournamentMetricOperationHTTP
	case "tournament.websocket":
		return tournamentMetricOperationRealtime
	default:
		return tournamentMetricOperationOther
	}
}

func validTournamentLagKind(kind string) bool {
	switch kind {
	case "deadline", "delivery", "projection":
		return true
	default:
		return false
	}
}

func validTournamentClockSource(source string) bool {
	switch source {
	case "database", "server":
		return true
	default:
		return false
	}
}

var _ TournamentEventObserver = (*TournamentMetrics)(nil)
var _ TournamentLagObserver = (*TournamentMetrics)(nil)
var _ TournamentEventQueueObserver = (*TournamentMetrics)(nil)
