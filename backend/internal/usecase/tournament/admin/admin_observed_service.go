package admin

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
	observabilityusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/observability"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

// ObservedService emits one payload-free terminal event for each admin mutation
// that does not already own a lower-level production observer.
type AdminObservedService struct {
	next     AdminService
	clock    OperationClock
	observer OperationObserver
}

func AdminNewObservedService(next AdminService, clock OperationClock, observer OperationObserver) *AdminObservedService {
	return &AdminObservedService{next: next, clock: clock, observer: observabilityusecase.FirstOperationObserver(observer)}
}

func (service *AdminObservedService) GetRoster(ctx context.Context, query RosterQuery) (RosterView, error) {
	if service == nil || service.next == nil {
		return RosterView{}, domain.ErrInternal
	}
	return service.next.GetRoster(ctx, query)
}

func (service *AdminObservedService) ReplaceRoster(
	ctx context.Context,
	command ReplaceRosterCommand,
) (view RosterView, err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		view, err = service.next.ReplaceRoster(ctx, command)
	}
	measurement.Emit(ctx, operationEvent(
		OperationRosterReplace,
		command.CommandScope,
		command.TournamentID,
		view.Revision,
	), err)
	return view, err
}

func (service *AdminObservedService) RunPreflight(
	ctx context.Context,
	command PreflightCommand,
) (report tournamentpreflight.ReportRevision, err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		report, err = service.next.RunPreflight(ctx, command)
	}
	measurement.Emit(ctx, operationEvent(
		OperationPreflightRun,
		command.CommandScope,
		command.TournamentID,
		command.ExpectedProjectionRevision,
	), err)
	return report, err
}

func (service *AdminObservedService) LockRoster(
	ctx context.Context,
	command LockRosterCommand,
) (view RosterView, err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		view, err = service.next.LockRoster(ctx, command)
	}
	measurement.Emit(ctx, operationEvent(
		OperationRosterLock,
		command.CommandScope,
		command.TournamentID,
		view.Revision,
	), err)
	return view, err
}

func (service *AdminObservedService) UnlockRoster(
	ctx context.Context,
	command UnlockRosterCommand,
) (view RosterView, err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		view, err = service.next.UnlockRoster(ctx, command)
	}
	measurement.Emit(ctx, operationEvent(
		OperationRosterUnlock,
		command.CommandScope,
		command.TournamentID,
		view.Revision,
	), err)
	return view, err
}

func (service *AdminObservedService) ConfigurePairings(
	ctx context.Context,
	command PairingCommand,
) (view SwissRoundView, err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		view, err = service.next.ConfigurePairings(ctx, command)
	}
	entityID := view.ID
	if entityID == uuid.Nil {
		entityID = command.TournamentID
	}
	measurement.Emit(ctx, operationEvent(
		OperationPairingConfigure,
		command.CommandScope,
		entityID,
		view.Revision,
	), err)
	return view, err
}

func (service *AdminObservedService) ApplyTournamentAction(
	ctx context.Context,
	command TournamentActionCommand,
) (view usecase.TournamentView, err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		view, err = service.next.ApplyTournamentAction(ctx, command)
	}
	measurement.Emit(ctx, operationEvent(
		OperationTournamentAction,
		command.CommandScope,
		command.TournamentID,
		view.Revision,
	), err)
	return view, err
}

func (service *AdminObservedService) ControlWave(
	ctx context.Context,
	command WaveCommand,
) (view WaveView, err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		view, err = service.next.ControlWave(ctx, command)
	}
	measurement.Emit(ctx, operationEvent(
		OperationWaveControl,
		command.CommandScope,
		command.WaveID,
		view.Revision,
	), err)
	return view, err
}

func (service *AdminObservedService) ResolveNoShow(ctx context.Context, command NoShowCommand) (err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		err = service.next.ResolveNoShow(ctx, command)
	}
	measurement.Emit(ctx, operationEvent(
		OperationNoShowResolve,
		command.CommandScope,
		command.SeriesID,
		command.ExpectedAuthorityRevision,
	), err)
	return err
}

func (service *AdminObservedService) AssignReserve(ctx context.Context, command ReserveCommand) (err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		err = service.next.AssignReserve(ctx, command)
	}
	measurement.Emit(ctx, operationEvent(
		OperationReserveAssign,
		command.CommandScope,
		command.AssignmentID,
		command.ExpectedAuthorityRevision,
	), err)
	return err
}

func (service *AdminObservedService) RecordForfeit(ctx context.Context, command ForfeitCommand) (err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		err = service.next.RecordForfeit(ctx, command)
	}
	measurement.Emit(ctx, operationEvent(
		OperationForfeitRecord,
		command.CommandScope,
		command.SeriesID,
		command.ExpectedAuthorityRevision,
	), err)
	return err
}

func (service *AdminObservedService) ReplayGame(ctx context.Context, command ReplayCommand) (err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		err = service.next.ReplayGame(ctx, command)
	}
	measurement.Emit(ctx, operationEvent(
		OperationGameReplay,
		command.CommandScope,
		command.ReplacementGameID,
		command.ExpectedAuthorityRevision,
	), err)
	return err
}

func (service *AdminObservedService) CorrectGameResult(
	ctx context.Context,
	command CorrectionCommand,
) (evidence CorrectionEvidence, err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		evidence, err = service.next.CorrectGameResult(ctx, command)
	}
	measurement.Emit(ctx, operationEvent(
		OperationResultCorrect,
		command.CommandScope,
		command.GameID,
		command.ExpectedProjectionRevision,
	), err)
	return evidence, err
}

func (service *AdminObservedService) ListAudit(ctx context.Context, query AuditQuery) (audit.AuditPage, error) {
	if service == nil || service.next == nil {
		return audit.AuditPage{}, domain.ErrInternal
	}
	return service.next.ListAudit(ctx, query)
}

func (service *AdminObservedService) ExportIncident(
	ctx context.Context,
	query IncidentQuery,
) (audit.IncidentBundle, error) {
	if service == nil || service.next == nil {
		return audit.IncidentBundle{}, domain.ErrInternal
	}
	return service.next.ExportIncident(ctx, query)
}

func (service *AdminObservedService) GetOperatorSnapshot(
	ctx context.Context,
	query SnapshotQuery,
) (OperatorSnapshotView, error) {
	if service == nil || service.next == nil {
		return OperatorSnapshotView{}, domain.ErrInternal
	}
	return service.next.GetOperatorSnapshot(ctx, query)
}

func (service *AdminObservedService) measurement() observabilityusecase.OperationMeasurement {
	if service == nil {
		return observabilityusecase.OperationMeasurement{}
	}
	return observabilityusecase.NewOperationMeasurement(service.clock, service.observer)
}

func operationEvent(
	operation Operation,
	scope CommandScope,
	entityID uuid.UUID,
	revision int64,
) OperationEvent {
	return OperationEvent{
		Operation: operation, CommandID: scope.CommandID, TournamentID: scope.TournamentID,
		EntityID: entityID, Revision: revision,
	}
}

var _ AdminService = (*AdminObservedService)(nil)
