package bootstrap

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	logkit "github.com/wahrwelt-kit/go-logkit"

	telemetryadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/telemetry"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

type eventTelemetry struct {
	metrics    *observability.TournamentMetrics
	observer   observability.TournamentEventObserver
	dispatcher *observability.TournamentEventDispatcher
}

type privateMetricsHandler struct {
	http.Handler
}

func provideEventTelemetry(log logkit.Logger) (eventTelemetry, error) {
	metrics := observability.NewTournamentMetrics()
	dispatcher, err := observability.NewTournamentEventDispatcher(
		observability.TournamentEventDispatcherConfig{QueueObserver: metrics},
		metrics,
		observability.NewTournamentStructuredLogger(log),
	)
	if err != nil {
		return eventTelemetry{}, err
	}
	return eventTelemetry{
		metrics:    metrics,
		observer:   dispatcher,
		dispatcher: dispatcher,
	}, nil
}

func provideTournamentEventDispatcher(
	telemetry eventTelemetry,
) *observability.TournamentEventDispatcher {
	return telemetry.dispatcher
}

func provideRecoveryObserver(telemetry eventTelemetry) *telemetryadapter.RecoveryObserver {
	return telemetryadapter.NewRecoveryObserver(telemetry.observer)
}

func provideExecutionRecoveryObserver(
	telemetry eventTelemetry,
) *telemetryadapter.ExecutionRecoveryObserver {
	return telemetryadapter.NewExecutionRecoveryObserver(telemetry.observer)
}

func provideReconnectObserver(telemetry eventTelemetry) *telemetryadapter.ReconnectObserver {
	return telemetryadapter.NewReconnectObserver(telemetry.observer)
}

func provideEventDeliveryObserver(telemetry eventTelemetry) *telemetryadapter.EventDeliveryObserver {
	return telemetryadapter.NewEventDeliveryObserver(telemetry.observer)
}

func provideTournamentAdminObserver(telemetry eventTelemetry) *telemetryadapter.TournamentAdminObserver {
	return telemetryadapter.NewTournamentAdminObserver(telemetry.observer)
}

func provideTournamentParticipantObserver(
	telemetry eventTelemetry,
) *telemetryadapter.TournamentParticipantObserver {
	return telemetryadapter.NewTournamentParticipantObserver(telemetry.observer)
}

func providePrivateMetricsHandler(telemetry eventTelemetry) privateMetricsHandler {
	if telemetry.metrics == nil || telemetry.metrics.Gatherer() == nil {
		return privateMetricsHandler{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		})}
	}
	return privateMetricsHandler{Handler: promhttp.HandlerFor(
		telemetry.metrics.Gatherer(),
		promhttp.HandlerOpts{EnableOpenMetrics: true},
	)}
}
