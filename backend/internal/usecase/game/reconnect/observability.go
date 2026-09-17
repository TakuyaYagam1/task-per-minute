package reconnect

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	OutcomeSuccess  = "success"
	OutcomeRetry    = "retry"
	OutcomeRejected = "rejected"
	OutcomeFailure  = "failure"
)

type ReconnectEvent struct {
	ReconnectEvent string
	Outcome        string
	CommandID      uuid.UUID
	TournamentID   uuid.UUID
	ParticipantID  uuid.UUID
	Stage          string
	Transition     string
	ReasonCode     string
	Duration       time.Duration
	Revision       int64
	HasDeadlineLag bool
	DeadlineLag    time.Duration
}

type reconnectEventMeasurement struct {
	observer  Observer
	clock     ReconnectClock
	startedAt time.Time
}

func newReconnectEventMeasurement(clock ReconnectClock, observer Observer) reconnectEventMeasurement {
	measurement := reconnectEventMeasurement{clock: clock, observer: observer}
	if clock != nil && !nilReconnectObserver(observer) {
		measurement.startedAt = clock.Now()
	}
	return measurement
}

func (m reconnectEventMeasurement) duration() time.Duration {
	if nilReconnectObserver(m.observer) || m.clock == nil || m.startedAt.IsZero() {
		return 0
	}
	duration := m.clock.Now().Sub(m.startedAt)
	if duration < 0 {
		return 0
	}
	return duration
}

func emitReconnectEvent(
	ctx context.Context,
	measurement reconnectEventMeasurement,
	event ReconnectEvent,
) {
	if nilReconnectObserver(measurement.observer) {
		return
	}
	event.Duration = measurement.duration()
	if event.Duration < 0 {
		event.Duration = 0
	}
	if event.Revision < 0 {
		event.Revision = 0
	}
	ObserveEvent(ctx, measurement.observer, event)
}

// ObserveEvent isolates an observer from the authority path. Event delivery is
// best effort and may never turn a committed reconnect result into a failure.
func ObserveEvent(ctx context.Context, observer Observer, event ReconnectEvent) {
	if nilReconnectObserver(observer) {
		return
	}
	if ctx == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	observer.Observe(ctx, event)
}

func reconnectEventResult(changed bool, err error, conflicts ...error) (string, string) {
	if err == nil {
		if changed {
			return OutcomeSuccess, "committed"
		}
		return OutcomeSuccess, "idempotent_replay"
	}
	if errors.Is(err, domain.ErrValidation) {
		return OutcomeRejected, "invalid_command"
	}
	for _, conflict := range conflicts {
		if errors.Is(err, conflict) {
			return OutcomeFailure, "conflict_exhausted"
		}
	}
	if errors.Is(err, domain.ErrConflict) {
		return OutcomeFailure, "conflict_exhausted"
	}
	return OutcomeFailure, "operation_failed"
}

func firstReconnectObserver(observers ...Observer) Observer {
	for _, observer := range observers {
		if !nilReconnectObserver(observer) {
			return observer
		}
	}
	return nil
}

func nilReconnectObserver(observer Observer) bool {
	if observer == nil {
		return true
	}
	value := reflect.ValueOf(observer)
	kind := value.Kind()
	return (kind == reflect.Chan || kind == reflect.Func || kind == reflect.Interface ||
		kind == reflect.Map || kind == reflect.Pointer || kind == reflect.Slice) && value.IsNil()
}
