package telemetry

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	tournamentparticipant "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/participant"
)

const tournamentParticipantCommandEvent = "tournament.participant.command"

// TournamentParticipantObserver adapts durable, payload-free participant
// command outcomes to the shared bounded tournament telemetry stream.
type TournamentParticipantObserver struct {
	observer observability.TournamentEventObserver
}

func NewTournamentParticipantObserver(
	observer observability.TournamentEventObserver,
) *TournamentParticipantObserver {
	observer = observability.FirstTournamentEventObserver(observer)
	if observer == nil {
		return nil
	}
	return &TournamentParticipantObserver{observer: observer}
}

func (observer *TournamentParticipantObserver) ObserveTournamentParticipantOperation(
	ctx context.Context,
	event tournamentparticipant.ParticipantOperationEvent,
) {
	if observer == nil || observer.observer == nil || event.CommandID == uuid.Nil ||
		event.TournamentID == uuid.Nil || event.EntityID == uuid.Nil || event.Revision < 0 {
		return
	}
	metadata, ok := tournamentParticipantOperationMetadata(event.Operation)
	if !ok {
		return
	}
	commandID := event.CommandID.String()
	_ = observability.EmitTournamentEvent(ctx, observer.observer, observability.TournamentEventInput{
		Event:         tournamentParticipantCommandEvent,
		Outcome:       event.Outcome,
		CorrelationID: commandID,
		CommandID:     commandID,
		TournamentID:  event.TournamentID.String(),
		EntityKind:    metadata.entityKind,
		EntityID:      event.EntityID.String(),
		Stage:         metadata.stage,
		Transition:    string(event.Operation),
		Duration:      event.Duration,
		ReasonCode:    event.ReasonCode,
		Revision:      event.Revision,
	})
}

type tournamentParticipantMetadata struct {
	stage      string
	entityKind string
}

func tournamentParticipantOperationMetadata(
	operation tournamentparticipant.ParticipantOperation,
) (tournamentParticipantMetadata, bool) {
	switch operation {
	case tournamentparticipant.ParticipantOperationReady:
		return tournamentParticipantMetadata{stage: "readiness", entityKind: "wave"}, true
	case tournamentparticipant.ParticipantOperationDraft:
		return tournamentParticipantMetadata{stage: "draft", entityKind: "draft"}, true
	case tournamentparticipant.ParticipantOperationSubmission:
		return tournamentParticipantMetadata{stage: "submission", entityKind: "game"}, true
	case tournamentparticipant.ParticipantOperationSurrender:
		return tournamentParticipantMetadata{stage: "settlement", entityKind: "series"}, true
	case tournamentparticipant.ParticipantOperationPostSeries:
		return tournamentParticipantMetadata{stage: "post_series", entityKind: "series"}, true
	default:
		return tournamentParticipantMetadata{}, false
	}
}

var _ tournamentparticipant.ParticipantOperationObserver = (*TournamentParticipantObserver)(nil)
