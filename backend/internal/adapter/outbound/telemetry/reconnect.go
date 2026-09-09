package telemetry

import (
	"context"

	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

type ReconnectObserver struct {
	observer observability.TournamentEventObserver
}

var _ gameusecase.Observer = (*ReconnectObserver)(nil)

func NewReconnectObserver(observer observability.TournamentEventObserver) *ReconnectObserver {
	observer = observability.FirstTournamentEventObserver(observer)
	if observer == nil {
		return nil
	}
	return &ReconnectObserver{observer: observer}
}

func (o *ReconnectObserver) Observe(
	ctx context.Context,
	event gameusecase.ReconnectEvent,
) {
	if o == nil || o.observer == nil {
		return
	}
	if event.HasDeadlineLag {
		observability.ObserveTournamentLag(o.observer, "deadline", event.DeadlineLag)
	}
	commandID := event.CommandID.String()
	_ = observability.EmitTournamentEvent(ctx, o.observer, observability.TournamentEventInput{
		Event:         event.ReconnectEvent,
		Outcome:       event.Outcome,
		CorrelationID: commandID,
		CommandID:     commandID,
		TournamentID:  event.TournamentID.String(),
		EntityKind:    "participant",
		EntityID:      event.ParticipantID.String(),
		Stage:         event.Stage,
		Transition:    event.Transition,
		Duration:      event.Duration,
		ReasonCode:    event.ReasonCode,
		Revision:      event.Revision,
	})
}
