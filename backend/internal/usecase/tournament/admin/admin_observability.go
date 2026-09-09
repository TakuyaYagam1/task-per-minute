package admin

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	OperationOutcomeSuccess  = "success"
	OperationOutcomeRetry    = "retry"
	OperationOutcomeRejected = "rejected"
	OperationOutcomeFailure  = "failure"
)

type Operation string

const (
	OperationRosterReplace    Operation = "roster_replace"
	OperationPreflightRun     Operation = "preflight_run"
	OperationRosterLock       Operation = "roster_lock"
	OperationRosterUnlock     Operation = "roster_unlock"
	OperationPairingConfigure Operation = "pairing_configure"
	OperationTournamentAction Operation = "tournament_action"
	OperationWaveControl      Operation = "wave_control"
	OperationNoShowResolve    Operation = "no_show_resolve"
	OperationReserveAssign    Operation = "reserve_assign"
	OperationForfeitRecord    Operation = "forfeit_record"
	OperationGameReplay       Operation = "game_replay"
	OperationResultCorrect    Operation = "result_correct"
)

// OperationEvent is a bounded, payload-free terminal outcome for one operator command.
type OperationEvent struct {
	Operation    Operation
	CommandID    uuid.UUID
	TournamentID uuid.UUID
	EntityID     uuid.UUID
	Outcome      string
	ReasonCode   string
	Revision     int64
	Duration     time.Duration
}

type OperationObserver interface {
	ObserveTournamentAdminOperation(ctx context.Context, event OperationEvent)
}

type OperationClock interface {
	Now() time.Time
}

type operationMeasurement struct {
	clock     OperationClock
	observer  OperationObserver
	startedAt time.Time
}

func newOperationMeasurement(clock OperationClock, observer OperationObserver) operationMeasurement {
	measurement := operationMeasurement{clock: clock, observer: firstOperationObserver(observer)}
	if measurement.observer != nil {
		measurement.startedAt, _ = operationNow(clock)
	}
	return measurement
}

func (measurement operationMeasurement) emit(ctx context.Context, event OperationEvent, err error) {
	if measurement.observer == nil {
		return
	}
	event.Outcome, event.ReasonCode = operationResult(err)
	event.Duration = measurement.duration()
	if event.Revision < 0 {
		event.Revision = 0
	}
	observeOperationSafely(ctx, measurement.observer, event)
}

func (measurement operationMeasurement) duration() time.Duration {
	if measurement.startedAt.IsZero() {
		return 0
	}
	finishedAt, ok := operationNow(measurement.clock)
	if !ok || finishedAt.Before(measurement.startedAt) {
		return 0
	}
	return finishedAt.Sub(measurement.startedAt)
}

func operationNow(clock OperationClock) (value time.Time, ok bool) {
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

func operationResult(err error) (string, string) {
	switch {
	case err == nil:
		return OperationOutcomeSuccess, "completed"
	case errors.Is(err, domain.ErrValidation):
		return OperationOutcomeRejected, "invalid_command"
	case errors.Is(err, domain.ErrForbidden):
		return OperationOutcomeRejected, "forbidden"
	case errors.Is(err, domain.ErrConflict):
		return OperationOutcomeRejected, "stale_revision"
	case errors.Is(err, domain.ErrRateLimited):
		return OperationOutcomeRetry, "rate_limited"
	default:
		return OperationOutcomeFailure, "operation_failed"
	}
}

func observeOperationSafely(ctx context.Context, observer OperationObserver, event OperationEvent) {
	if ctx == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	observer.ObserveTournamentAdminOperation(ctx, event)
}

func firstOperationObserver(observers ...OperationObserver) OperationObserver {
	for _, observer := range observers {
		if !nilOperationObserver(observer) {
			return observer
		}
	}
	return nil
}

func nilOperationObserver(observer OperationObserver) bool {
	if observer == nil {
		return true
	}
	value := reflect.ValueOf(observer)
	kind := value.Kind()
	return (kind == reflect.Chan || kind == reflect.Func || kind == reflect.Interface ||
		kind == reflect.Map || kind == reflect.Pointer || kind == reflect.Slice) && value.IsNil()
}
