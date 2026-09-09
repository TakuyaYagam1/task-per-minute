package observability

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

const (
	DefaultTournamentEventQueueCapacity = 1024
	maximumTournamentEventQueueCapacity = 65536
)

var (
	ErrTournamentEventDispatcherConfig  = errors.New("invalid tournament event dispatcher config")
	ErrTournamentEventDispatcherRunning = errors.New("tournament event dispatcher already started")
)

// TournamentEventQueueObserver receives bounded internal queue measurements.
// It is deliberately separate from tournament events so reporting a dropped
// event cannot recurse through the same full queue.
type TournamentEventQueueObserver interface {
	ObserveTournamentEventDrop()
	ObserveTournamentEventQueueLag(time.Duration)
}

type TournamentEventDispatcherConfig struct {
	QueueCapacity int
	Now           func() time.Time
	QueueObserver TournamentEventQueueObserver
}

type tournamentObservation struct {
	ctx        context.Context
	event      *TournamentEvent
	lagKind    string
	lag        time.Duration
	enqueuedAt time.Time
}

// TournamentEventDispatcher removes observability backpressure from command
// paths. A full queue drops the new observation and records that loss through
// the direct queue observer. Observer panics are isolated per consumer.
type TournamentEventDispatcher struct {
	observers     []TournamentEventObserver
	queueObserver TournamentEventQueueObserver
	queue         chan tournamentObservation
	now           func() time.Time

	started atomic.Bool
	running atomic.Bool
	dropped atomic.Uint64
}

func NewTournamentEventDispatcher(
	config TournamentEventDispatcherConfig,
	observers ...TournamentEventObserver,
) (*TournamentEventDispatcher, error) {
	if config.QueueCapacity == 0 {
		config.QueueCapacity = DefaultTournamentEventQueueCapacity
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.QueueCapacity < 1 || config.QueueCapacity > maximumTournamentEventQueueCapacity {
		return nil, ErrTournamentEventDispatcherConfig
	}
	filtered := make([]TournamentEventObserver, 0, len(observers))
	queueObserver := config.QueueObserver
	for _, observer := range observers {
		if nilTournamentEventObserver(observer) {
			continue
		}
		filtered = append(filtered, observer)
		if queueObserver == nil {
			queueObserver, _ = observer.(TournamentEventQueueObserver)
		}
	}
	if len(filtered) == 0 {
		return nil, ErrTournamentEventDispatcherConfig
	}
	return &TournamentEventDispatcher{
		observers:     filtered,
		queueObserver: queueObserver,
		queue:         make(chan tournamentObservation, config.QueueCapacity),
		now:           config.Now,
	}, nil
}

func (dispatcher *TournamentEventDispatcher) Run(ctx context.Context) error {
	if ctx == nil || dispatcher == nil || len(dispatcher.observers) == 0 ||
		dispatcher.queue == nil || dispatcher.now == nil {
		return ErrTournamentEventDispatcherConfig
	}
	if !dispatcher.started.CompareAndSwap(false, true) {
		return ErrTournamentEventDispatcherRunning
	}
	dispatcher.running.Store(true)
	defer dispatcher.running.Store(false)

	for {
		select {
		case <-ctx.Done():
			return nil
		case observation := <-dispatcher.queue:
			dispatcher.dispatch(observation)
		}
	}
}

func (dispatcher *TournamentEventDispatcher) Ready() bool {
	return dispatcher != nil && dispatcher.running.Load()
}

func (dispatcher *TournamentEventDispatcher) Dropped() uint64 {
	if dispatcher == nil {
		return 0
	}
	return dispatcher.dropped.Load()
}

func (dispatcher *TournamentEventDispatcher) ObserveTournamentEvent(
	ctx context.Context,
	event TournamentEvent,
) {
	if dispatcher == nil || dispatcher.queue == nil || dispatcher.now == nil {
		return
	}
	validated, err := NewTournamentEvent(TournamentEventInput(event))
	if err != nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	} else {
		ctx = context.WithoutCancel(ctx)
	}
	dispatcher.enqueue(tournamentObservation{
		ctx: ctx, event: &validated, enqueuedAt: dispatcher.now().Round(0).UTC(),
	})
}

func (dispatcher *TournamentEventDispatcher) ObserveTournamentLag(kind string, lag time.Duration) {
	if dispatcher == nil || dispatcher.queue == nil || dispatcher.now == nil || lag < 0 {
		return
	}
	dispatcher.enqueue(tournamentObservation{
		ctx: context.Background(), lagKind: kind, lag: lag,
		enqueuedAt: dispatcher.now().Round(0).UTC(),
	})
}

func (dispatcher *TournamentEventDispatcher) enqueue(observation tournamentObservation) {
	select {
	case dispatcher.queue <- observation:
	default:
		dispatcher.dropped.Add(1)
		dispatcher.observeDropSafely()
	}
}

func (dispatcher *TournamentEventDispatcher) dispatch(observation tournamentObservation) {
	if lag := dispatcher.now().Round(0).UTC().Sub(observation.enqueuedAt); lag >= 0 {
		dispatcher.observeQueueLagSafely(lag)
	}
	for _, observer := range dispatcher.observers {
		if observation.event != nil {
			observeTournamentEventSafely(observer, observation.ctx, *observation.event)
			continue
		}
		lagObserver, ok := observer.(TournamentLagObserver)
		if ok {
			observeTournamentLagSafely(lagObserver, observation.lagKind, observation.lag)
		}
	}
}

func (dispatcher *TournamentEventDispatcher) observeDropSafely() {
	if dispatcher.queueObserver == nil {
		return
	}
	defer func() { _ = recover() }()
	dispatcher.queueObserver.ObserveTournamentEventDrop()
}

func (dispatcher *TournamentEventDispatcher) observeQueueLagSafely(lag time.Duration) {
	if dispatcher.queueObserver == nil {
		return
	}
	defer func() { _ = recover() }()
	dispatcher.queueObserver.ObserveTournamentEventQueueLag(lag)
}

func observeTournamentEventSafely(
	observer TournamentEventObserver,
	ctx context.Context,
	event TournamentEvent,
) {
	defer func() { _ = recover() }()
	observer.ObserveTournamentEvent(ctx, event)
}

func observeTournamentLagSafely(observer TournamentLagObserver, kind string, lag time.Duration) {
	defer func() { _ = recover() }()
	observer.ObserveTournamentLag(kind, lag)
}

var (
	_ TournamentEventObserver = (*TournamentEventDispatcher)(nil)
	_ TournamentLagObserver   = (*TournamentEventDispatcher)(nil)
)
