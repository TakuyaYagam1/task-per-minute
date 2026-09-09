package telemetry

import (
	"context"

	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

const (
	tournamentRecoveryEvent      = "tournament.recovery"
	tournamentRecoveryEntityKind = "tournament"
)

type RecoveryObserver struct {
	observer observability.TournamentEventObserver
}

var _ recovery.RecoveryObserver = (*RecoveryObserver)(nil)

func NewRecoveryObserver(observer observability.TournamentEventObserver) *RecoveryObserver {
	observer = observability.FirstTournamentEventObserver(observer)
	if observer == nil {
		return nil
	}
	return &RecoveryObserver{observer: observer}
}

func (o *RecoveryObserver) ObserveRecovery(
	ctx context.Context,
	event recovery.RecoveryEvent,
) {
	if o == nil || o.observer == nil {
		return
	}
	tournamentID := event.TournamentID.String()
	_ = observability.EmitTournamentEvent(ctx, o.observer, observability.TournamentEventInput{
		Event:         tournamentRecoveryEvent,
		Outcome:       event.Outcome,
		CorrelationID: event.CorrelationID,
		TournamentID:  tournamentID,
		EntityKind:    tournamentRecoveryEntityKind,
		EntityID:      tournamentID,
		Stage:         event.Stage,
		Transition:    event.Transition,
		ReasonCode:    event.ReasonCode,
		Revision:      event.Revision,
	})
}
