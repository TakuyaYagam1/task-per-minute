package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"

	"github.com/google/uuid"
)

const (
	RecoveryOutcomeSuccess  = "success"
	RecoveryOutcomeRejected = "rejected"
	RecoveryOutcomeFailure  = "failure"

	RecoveryStageStartup  = "startup_recovery"
	RecoveryStageDeadline = "deadline"
)

type recoveryEventReason string

const (
	recoveryReasonNoWork        recoveryEventReason = "nothing_to_rearm"
	recoveryReasonWorkRearmed   recoveryEventReason = "work_rearmed"
	recoveryReasonRearmerFailed recoveryEventReason = "rearmer_failed"
)

type RecoveryEvent struct {
	Outcome       string
	CorrelationID string
	TournamentID  uuid.UUID
	Stage         string
	Transition    string
	ReasonCode    string
	Revision      int64
}

type RecoveryObserver interface {
	ObserveRecovery(ctx context.Context, event RecoveryEvent)
}

func (r *RecoveryReconciler) emitRecoveryEvent(
	ctx context.Context,
	graph RecoveryGraph,
	outcome string,
	transition string,
	reason recoveryEventReason,
) {
	if r == nil || nilRecoveryObserver(r.observer) {
		return
	}
	revision := graph.Cursor.ProjectionRevision
	if revision < 0 {
		revision = 0
	}
	observeRecoverySafely(ctx, r.observer, RecoveryEvent{
		Outcome:       outcome,
		CorrelationID: recoveryCorrelation(graph),
		TournamentID:  graph.TournamentID,
		Stage:         RecoveryStageStartup,
		Transition:    transition,
		ReasonCode:    string(reason),
		Revision:      revision,
	})
}

func recoveryCorrelation(graph RecoveryGraph) string {
	identity := fmt.Sprintf(
		"%s:%d:%d:%d:%s",
		graph.TournamentID,
		graph.Cursor.SchemaVersion,
		graph.Cursor.LastSequence,
		graph.Cursor.ProjectionRevision,
		graph.Cursor.DerivedRevisionID.UUID(),
	)
	return recoveryCorrelationIdentity(identity)
}

func (sweep *DeadlineSweep) emitDeadlineEvent(
	ctx context.Context,
	deadline PendingDeadline,
	outcome string,
	transition string,
	reason string,
) {
	if sweep == nil || nilRecoveryObserver(sweep.observer) {
		return
	}
	identity := fmt.Sprintf("%s:%s:%d", deadline.Kind, deadline.ID, deadline.ExpectedRevision)
	event := RecoveryEvent{
		Outcome:       outcome,
		CorrelationID: recoveryCorrelationIdentity(identity),
		TournamentID:  deadline.TournamentID,
		Stage:         RecoveryStageDeadline,
		Transition:    transition,
		ReasonCode:    reason,
		Revision:      deadline.ExpectedRevision,
	}
	observeRecoverySafely(ctx, sweep.observer, event)
}

func recoveryCorrelationIdentity(identity string) string {
	digest := sha256.Sum256([]byte(identity))
	return "recovery-" + hex.EncodeToString(digest[:12])
}

func observeRecoverySafely(ctx context.Context, observer RecoveryObserver, event RecoveryEvent) {
	defer func() {
		_ = recover()
	}()
	observer.ObserveRecovery(ctx, event)
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
