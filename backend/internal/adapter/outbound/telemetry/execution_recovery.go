package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

const (
	executionRecoveryEvent      = "tournament.execution.recovery"
	executionRecoveryEntityKind = "tournament"
	executionRecoveryStage      = "recovery"
)

// ExecutionRecoveryObserver adapts payload-free execution recovery events to
// the shared bounded tournament telemetry stream.
type ExecutionRecoveryObserver struct {
	observer observability.TournamentEventObserver
}

func NewExecutionRecoveryObserver(
	observer observability.TournamentEventObserver,
) *ExecutionRecoveryObserver {
	observer = observability.FirstTournamentEventObserver(observer)
	if observer == nil {
		return nil
	}
	return &ExecutionRecoveryObserver{observer: observer}
}

func (observer *ExecutionRecoveryObserver) ObserveExecutionRecovery(
	ctx context.Context,
	event gameusecase.RecoveryEvent,
) {
	if observer == nil || observer.observer == nil || event.TournamentID == uuid.Nil ||
		event.Revision < 0 {
		return
	}
	tournamentID := event.TournamentID.String()
	_ = observability.EmitTournamentEvent(ctx, observer.observer, observability.TournamentEventInput{
		Event:         executionRecoveryEvent,
		Outcome:       event.Outcome,
		CorrelationID: executionRecoveryCorrelation(event),
		TournamentID:  tournamentID,
		EntityKind:    executionRecoveryEntityKind,
		EntityID:      tournamentID,
		Stage:         executionRecoveryStage,
		Transition:    event.Transition,
		ReasonCode:    event.ReasonCode,
		Revision:      event.Revision,
	})
}

func executionRecoveryCorrelation(event gameusecase.RecoveryEvent) string {
	input := event.TournamentID.String() + ":" + strconv.FormatInt(event.Revision, 10)
	digest := sha256.Sum256([]byte(input))
	return "execution-recovery-" + hex.EncodeToString(digest[:12])
}

var _ gameusecase.RecoveryObserver = (*ExecutionRecoveryObserver)(nil)
