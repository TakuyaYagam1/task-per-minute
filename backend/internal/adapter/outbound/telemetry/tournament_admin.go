package telemetry

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

const tournamentAdminCommandEvent = "tournament.admin.command"

type TournamentAdminObserver struct {
	observer observability.TournamentEventObserver
}

func NewTournamentAdminObserver(
	observer observability.TournamentEventObserver,
) *TournamentAdminObserver {
	observer = observability.FirstTournamentEventObserver(observer)
	if observer == nil {
		return nil
	}
	return &TournamentAdminObserver{observer: observer}
}

func (observer *TournamentAdminObserver) ObserveTournamentAdminOperation(
	ctx context.Context,
	event tournamentadmin.OperationEvent,
) {
	if observer == nil || observer.observer == nil || event.CommandID == uuid.Nil ||
		event.TournamentID == uuid.Nil || event.EntityID == uuid.Nil || event.Revision < 0 {
		return
	}
	metadata, ok := tournamentAdminOperationMetadata(event.Operation)
	if !ok {
		return
	}
	commandID := event.CommandID.String()
	_ = observability.EmitTournamentEvent(ctx, observer.observer, observability.TournamentEventInput{
		Event:         tournamentAdminCommandEvent,
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

type tournamentAdminMetadata struct {
	stage      string
	entityKind string
}

func tournamentAdminOperationMetadata(operation tournamentadmin.Operation) (tournamentAdminMetadata, bool) {
	switch operation {
	case tournamentadmin.OperationRosterReplace,
		tournamentadmin.OperationRosterLock,
		tournamentadmin.OperationRosterUnlock:
		return tournamentAdminMetadata{stage: "maintenance", entityKind: "roster"}, true
	case tournamentadmin.OperationPreflightRun:
		return tournamentAdminMetadata{stage: "maintenance", entityKind: "tournament"}, true
	case tournamentadmin.OperationPairingConfigure:
		return tournamentAdminMetadata{stage: "pairing", entityKind: "swiss_round"}, true
	case tournamentadmin.OperationTournamentAction:
		return tournamentAdminMetadata{stage: "tournament_lifecycle", entityKind: "tournament"}, true
	case tournamentadmin.OperationWaveControl:
		return tournamentAdminMetadata{stage: "wave", entityKind: "wave"}, true
	case tournamentadmin.OperationNoShowResolve, tournamentadmin.OperationForfeitRecord:
		return tournamentAdminMetadata{stage: "settlement", entityKind: "series"}, true
	case tournamentadmin.OperationReserveAssign:
		return tournamentAdminMetadata{stage: "reserve", entityKind: "assignment"}, true
	case tournamentadmin.OperationGameReplay:
		return tournamentAdminMetadata{stage: "replay", entityKind: "game"}, true
	case tournamentadmin.OperationResultCorrect:
		return tournamentAdminMetadata{stage: "correction", entityKind: "game"}, true
	default:
		return tournamentAdminMetadata{}, false
	}
}

var _ tournamentadmin.OperationObserver = (*TournamentAdminObserver)(nil)
