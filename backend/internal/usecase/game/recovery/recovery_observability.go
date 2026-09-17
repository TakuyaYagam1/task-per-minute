package recovery

import (
	"context"
	"reflect"

	"github.com/google/uuid"
)

const (
	RecoveryOutcomeSuccess = "success"
	RecoveryOutcomeFailure = "failure"
)

// RecoveryEvent is a payload-free terminal outcome for one locally-owned
// execution recovery command.
type RecoveryEvent struct {
	TournamentID uuid.UUID
	Outcome      string
	Transition   string
	ReasonCode   string
	Revision     int64
}

// RecoveryObserver receives execution recovery outcomes after the authoritative
// recovery operation has returned.
type RecoveryObserver interface {
	ObserveExecutionRecovery(ctx context.Context, event RecoveryEvent)
}

func executionRecoverySuccessReason(report RecoveryReport) (string, bool) {
	switch {
	case report.TechnicalReplays > 0:
		return "epoch_replayed", true
	case report.Rearmed > 0:
		return "deadline_rearmed", true
	case report.Changed > 0:
		return "recovery_changed", true
	default:
		return "", false
	}
}

func observeExecutionRecoverySafely(
	ctx context.Context,
	observer RecoveryObserver,
	event RecoveryEvent,
) {
	if nilRecoveryObserver(observer) {
		return
	}
	if ctx == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	observer.ObserveExecutionRecovery(ctx, event)
}

func firstRecoveryObserver(observers ...RecoveryObserver) RecoveryObserver {
	for _, observer := range observers {
		if !nilRecoveryObserver(observer) {
			return observer
		}
	}
	return nil
}

func nilRecoveryObserver(observer RecoveryObserver) bool {
	if observer == nil {
		return true
	}
	value := reflect.ValueOf(observer)
	kind := value.Kind()
	return (kind == reflect.Chan || kind == reflect.Func || kind == reflect.Interface ||
		kind == reflect.Map || kind == reflect.Pointer || kind == reflect.Slice) && value.IsNil()
}
