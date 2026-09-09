package participant

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	ParticipantOutcomeSuccess  = "success"
	ParticipantOutcomeRetry    = "retry"
	ParticipantOutcomeRejected = "rejected"
	ParticipantOutcomeFailure  = "failure"
)

type ParticipantOperation string

const (
	ParticipantOperationReady      ParticipantOperation = "ready_set"
	ParticipantOperationDraft      ParticipantOperation = "draft_action"
	ParticipantOperationSubmission ParticipantOperation = "submission"
	ParticipantOperationSurrender  ParticipantOperation = "surrender"
	ParticipantOperationPostSeries ParticipantOperation = "post_series"
)

// ParticipantOperationEvent is a bounded, payload-free terminal outcome for
// one participant command after its outer transaction has returned.
type ParticipantOperationEvent struct {
	Operation    ParticipantOperation
	CommandID    uuid.UUID
	TournamentID uuid.UUID
	EntityID     uuid.UUID
	Outcome      string
	ReasonCode   string
	Revision     int64
	Duration     time.Duration
}

type ParticipantOperationObserver interface {
	ObserveTournamentParticipantOperation(ctx context.Context, event ParticipantOperationEvent)
}

type ParticipantOperationClock interface {
	Now() time.Time
}

type participantOperationMeasurement struct {
	clock     ParticipantOperationClock
	observer  ParticipantOperationObserver
	startedAt time.Time
}

func newParticipantOperationMeasurement(
	clock ParticipantOperationClock,
	observer ParticipantOperationObserver,
) participantOperationMeasurement {
	measurement := participantOperationMeasurement{clock: clock, observer: firstParticipantOperationObserver(observer)}
	if measurement.observer != nil {
		measurement.startedAt, _ = participantOperationNow(clock)
	}
	return measurement
}

func (measurement participantOperationMeasurement) emit(
	ctx context.Context,
	event ParticipantOperationEvent,
	err error,
) {
	if measurement.observer == nil {
		return
	}
	event.Outcome, event.ReasonCode = participantOperationResult(err)
	event.Duration = measurement.duration()
	if event.Revision < 0 {
		event.Revision = 0
	}
	observeParticipantOperationSafely(ctx, measurement.observer, event)
}

func (measurement participantOperationMeasurement) duration() time.Duration {
	if measurement.startedAt.IsZero() {
		return 0
	}
	finishedAt, ok := participantOperationNow(measurement.clock)
	if !ok || finishedAt.Before(measurement.startedAt) {
		return 0
	}
	return finishedAt.Sub(measurement.startedAt)
}

func participantOperationNow(clock ParticipantOperationClock) (value time.Time, ok bool) {
	if clock == nil {
		return time.Time{}, false
	}
	defer func() {
		if recover() != nil {
			value = time.Time{}
			ok = false
		}
	}()
	return clock.Now(), true
}

func participantOperationResult(err error) (string, string) {
	switch {
	case err == nil:
		return ParticipantOutcomeSuccess, "completed"
	case errors.Is(err, domain.ErrValidation):
		return ParticipantOutcomeRejected, "invalid_command"
	case errors.Is(err, domain.ErrForbidden):
		return ParticipantOutcomeRejected, "forbidden"
	case errors.Is(err, domain.ErrConflict):
		return ParticipantOutcomeRejected, "stale_revision"
	case errors.Is(err, domain.ErrRateLimited):
		return ParticipantOutcomeRetry, "rate_limited"
	default:
		return ParticipantOutcomeFailure, "operation_failed"
	}
}

func observeParticipantOperationSafely(
	ctx context.Context,
	observer ParticipantOperationObserver,
	event ParticipantOperationEvent,
) {
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() {
		_ = recover()
	}()
	observer.ObserveTournamentParticipantOperation(ctx, event)
}

func firstParticipantOperationObserver(
	observers ...ParticipantOperationObserver,
) ParticipantOperationObserver {
	for _, observer := range observers {
		if !nilParticipantOperationObserver(observer) {
			return observer
		}
	}
	return nil
}

func nilParticipantOperationObserver(observer ParticipantOperationObserver) bool {
	if observer == nil {
		return true
	}
	value := reflect.ValueOf(observer)
	kind := value.Kind()
	return (kind == reflect.Chan || kind == reflect.Func || kind == reflect.Interface ||
		kind == reflect.Map || kind == reflect.Pointer || kind == reflect.Slice) && value.IsNil()
}
