package telemetry

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	delivery "github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery"
)

const (
	tournamentEventDeliveryEvent      = "tournament.outbox.delivery"
	tournamentEventDeliveryEntityKind = "outbox_event"
	tournamentEventDeliveryStage      = "delivery"
)

type EventDeliveryObserver struct {
	observer observability.TournamentEventObserver
}

func NewEventDeliveryObserver(
	observer observability.TournamentEventObserver,
) *EventDeliveryObserver {
	observer = observability.FirstTournamentEventObserver(observer)
	if observer == nil {
		return nil
	}
	return &EventDeliveryObserver{observer: observer}
}

func (observer *EventDeliveryObserver) ObserveEventDelivery(
	ctx context.Context,
	event delivery.WorkerEvent,
) {
	if observer == nil || observer.observer == nil {
		return
	}
	if event.EventID == uuid.Nil || event.CorrelationID == uuid.Nil || event.TournamentID == uuid.Nil {
		return
	}
	eventID := event.EventID.String()
	correlationID := event.CorrelationID.String()
	if err := observability.EmitTournamentEvent(ctx, observer.observer, observability.TournamentEventInput{
		Event:         tournamentEventDeliveryEvent,
		Outcome:       event.Outcome,
		CorrelationID: correlationID,
		TournamentID:  event.TournamentID.String(),
		EntityKind:    tournamentEventDeliveryEntityKind,
		EntityID:      eventID,
		Stage:         tournamentEventDeliveryStage,
		Transition:    event.Transition,
		Duration:      event.Duration,
		ReasonCode:    event.ReasonCode,
		Revision:      event.ProjectionRevision,
	}); err != nil {
		return
	}
	observability.ObserveTournamentLag(observer.observer, "delivery", event.Duration)
}

var _ delivery.WorkerObserver = (*EventDeliveryObserver)(nil)
