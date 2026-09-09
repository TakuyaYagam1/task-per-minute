package observability_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	observabilitymocks "github.com/TakuyaYagam1/task-per-minute/internal/observability/mocks"
)

func TestTournamentEventDispatcherIsolatesObserverPanic(t *testing.T) {
	panicking := observabilitymocks.NewMockTournamentEventObserver(t)
	panicking.EXPECT().ObserveTournamentEvent(mock.Anything, mock.Anything).
		Run(func(context.Context, observability.TournamentEvent) { panic("observer failed") }).Once()
	received := make(chan observability.TournamentEvent, 1)
	capture := observabilitymocks.NewMockTournamentEventObserver(t)
	capture.EXPECT().ObserveTournamentEvent(mock.Anything, mock.Anything).
		Run(func(_ context.Context, event observability.TournamentEvent) { received <- event }).Once()
	dispatcher, err := observability.NewTournamentEventDispatcher(
		observability.TournamentEventDispatcherConfig{QueueCapacity: 1},
		panicking,
		capture,
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- dispatcher.Run(ctx) }()
	require.Eventually(t, dispatcher.Ready, time.Second, time.Millisecond)

	dispatcher.ObserveTournamentEvent(t.Context(), validDispatcherEvent())
	select {
	case event := <-received:
		require.Equal(t, "corr-dispatcher", event.CorrelationID)
	case <-time.After(time.Second):
		t.Fatal("healthy observer did not receive event after peer panic")
	}
	cancel()
	require.NoError(t, <-done)
}

func TestTournamentEventDispatcherDropsInsteadOfBlocking(t *testing.T) {
	var calls atomic.Int64
	entered := make(chan struct{})
	release := make(chan struct{})
	blocking := observabilitymocks.NewMockTournamentEventObserver(t)
	blocking.EXPECT().ObserveTournamentEvent(mock.Anything, mock.Anything).
		Run(func(context.Context, observability.TournamentEvent) {
			if calls.Add(1) == 1 {
				close(entered)
				<-release
			}
		}).Maybe()
	queueMetrics := observability.NewTournamentMetrics()
	dispatcher, err := observability.NewTournamentEventDispatcher(
		observability.TournamentEventDispatcherConfig{QueueCapacity: 1, QueueObserver: queueMetrics},
		blocking,
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- dispatcher.Run(ctx) }()
	require.Eventually(t, dispatcher.Ready, time.Second, time.Millisecond)
	dispatcher.ObserveTournamentEvent(t.Context(), validDispatcherEvent())
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("blocking observer was not entered")
	}

	dispatcher.ObserveTournamentEvent(t.Context(), validDispatcherEvent())
	returned := make(chan struct{})
	go func() {
		dispatcher.ObserveTournamentEvent(t.Context(), validDispatcherEvent())
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("full observer queue blocked the caller")
	}
	require.Equal(t, uint64(1), dispatcher.Dropped())
	families, err := queueMetrics.Gatherer().Gather()
	require.NoError(t, err)
	require.InDelta(t, 1, dispatcherCounterValue(
		t,
		families,
		"tpm_tournament_observer_dropped_events_total",
		map[string]string{"reason": "queue_full"},
	), 0)

	close(release)
	require.Eventually(t, func() bool { return calls.Load() >= 2 }, time.Second, time.Millisecond)
	cancel()
	require.NoError(t, <-done)
}

func TestTournamentEventDispatcherRecordsQueueLagWithoutCommandPathWork(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, time.September, 7, 15, 0, 0, 0, time.UTC)
	var nowUnixNano atomic.Int64
	nowUnixNano.Store(startedAt.UnixNano())
	metrics := observability.NewTournamentMetrics()
	dispatcher, err := observability.NewTournamentEventDispatcher(
		observability.TournamentEventDispatcherConfig{
			QueueCapacity: 1,
			QueueObserver: metrics,
			Now: func() time.Time {
				return time.Unix(0, nowUnixNano.Load()).UTC()
			},
		},
		metrics,
	)
	require.NoError(t, err)

	// Enqueue before Run so the lag sample is deterministic and the command
	// path performs no observer work.
	dispatcher.ObserveTournamentEvent(t.Context(), validDispatcherEvent())
	nowUnixNano.Store(startedAt.Add(25 * time.Millisecond).UnixNano())
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- dispatcher.Run(ctx) }()
	require.Eventually(t, func() bool {
		families, gatherErr := metrics.Gatherer().Gather()
		return gatherErr == nil && dispatcherHistogramCount(
			families,
			"tpm_tournament_observer_queue_lag_seconds",
			map[string]string{"queue": "observer"},
		) == 1
	}, time.Second, time.Millisecond)
	cancel()
	require.NoError(t, <-done)
}

func TestTournamentEventDispatcherRejectsInvalidConfigAndSecondRun(t *testing.T) {
	observer := observabilitymocks.NewMockTournamentEventObserver(t)
	_, err := observability.NewTournamentEventDispatcher(
		observability.TournamentEventDispatcherConfig{QueueCapacity: -1},
		observer,
	)
	require.ErrorIs(t, err, observability.ErrTournamentEventDispatcherConfig)
	_, err = observability.NewTournamentEventDispatcher(
		observability.TournamentEventDispatcherConfig{QueueCapacity: 1},
	)
	require.ErrorIs(t, err, observability.ErrTournamentEventDispatcherConfig)

	dispatcher, err := observability.NewTournamentEventDispatcher(
		observability.TournamentEventDispatcherConfig{QueueCapacity: 1},
		observer,
	)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- dispatcher.Run(ctx) }()
	require.Eventually(t, dispatcher.Ready, time.Second, time.Millisecond)
	require.ErrorIs(t, dispatcher.Run(t.Context()), observability.ErrTournamentEventDispatcherRunning)
	cancel()
	require.NoError(t, <-done)
}

func validDispatcherEvent() observability.TournamentEvent {
	event, err := observability.NewTournamentEvent(observability.TournamentEventInput{
		Event:         "tournament.recovery",
		Outcome:       observability.TournamentOutcomeSuccess,
		CorrelationID: "corr-dispatcher",
		TournamentID:  "73000000-0000-4000-8000-000000000001",
		EntityKind:    "tournament",
		EntityID:      "73000000-0000-4000-8000-000000000001",
		Stage:         "recovery",
		Transition:    "pending_to_rearmed",
		ReasonCode:    "recovered",
		Revision:      1,
	})
	if err != nil {
		panic(err)
	}
	return event
}

func dispatcherCounterValue(
	t *testing.T,
	families []*dto.MetricFamily,
	name string,
	labels map[string]string,
) float64 {
	t.Helper()
	metric := dispatcherMetricWithLabels(t, families, name, labels)
	require.NotNil(t, metric.Counter)
	return metric.Counter.GetValue()
}

func dispatcherHistogramCount(
	families []*dto.MetricFamily,
	name string,
	labels map[string]string,
) uint64 {
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			if dispatcherMetricLabelsMatch(metric, labels) && metric.Histogram != nil {
				return metric.Histogram.GetSampleCount()
			}
		}
	}
	return 0
}

func dispatcherMetricWithLabels(
	t *testing.T,
	families []*dto.MetricFamily,
	name string,
	labels map[string]string,
) *dto.Metric {
	t.Helper()
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			if dispatcherMetricLabelsMatch(metric, labels) {
				return metric
			}
		}
	}
	t.Fatalf("metric %s with labels %v not found", name, labels)
	return nil
}

func dispatcherMetricLabelsMatch(metric *dto.Metric, expected map[string]string) bool {
	if len(metric.Label) != len(expected) {
		return false
	}
	for _, label := range metric.Label {
		if expected[label.GetName()] != label.GetValue() {
			return false
		}
	}
	return true
}
