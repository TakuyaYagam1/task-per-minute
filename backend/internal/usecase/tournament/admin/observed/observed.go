package observed

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/correction"
	executionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
	admininbound "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/inbound"
	incidentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/incident"
	lifecycleworkflow "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/lifecycle"
	observabilityusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/observability"
	operationusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
	pairingusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/pairing"
	replayusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/replay"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/result"
	rosterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
	snapshotusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/snapshot"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

// Service is the canonical inbound admin contract observed by this decorator.
type Service = admininbound.AdminService

// ObservedService emits one payload-free terminal event for each admin mutation
// that does not already own a lower-level production observer.
type ObservedService struct {
	next     Service
	clock    observabilityusecase.OperationClock
	observer observabilityusecase.OperationObserver
}

func NewObservedService(
	next Service,
	clock observabilityusecase.OperationClock,
	observer observabilityusecase.OperationObserver,
) *ObservedService {
	return &ObservedService{next: next, clock: clock, observer: observabilityusecase.FirstOperationObserver(observer)}
}

func (service *ObservedService) GetRoster(ctx context.Context, query rosterusecase.RosterQuery) (rosterusecase.RosterView, error) {
	if service == nil || service.next == nil {
		return rosterusecase.RosterView{}, domain.ErrInternal
	}
	return service.next.GetRoster(ctx, query)
}

func (service *ObservedService) ReplaceRoster(
	ctx context.Context,
	command rosterusecase.ReplaceRosterCommand,
) (view rosterusecase.RosterView, err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		view, err = service.next.ReplaceRoster(ctx, command)
	}
	measurement.Emit(ctx, operationEvent(
		observabilityusecase.OperationRosterReplace,
		command.CommandScope,
		command.TournamentID,
		view.Revision,
	), err)
	return view, err
}

func (service *ObservedService) RunPreflight(
	ctx context.Context,
	command rosterusecase.PreflightCommand,
) (report tournamentpreflight.ReportRevision, err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		report, err = service.next.RunPreflight(ctx, command)
	}
	measurement.Emit(ctx, operationEvent(
		observabilityusecase.OperationPreflightRun,
		command.CommandScope,
		command.TournamentID,
		command.ExpectedProjectionRevision,
	), err)
	return report, err
}

func (service *ObservedService) LockRoster(
	ctx context.Context,
	command rosterusecase.LockRosterCommand,
) (view rosterusecase.RosterView, err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		view, err = service.next.LockRoster(ctx, command)
	}
	measurement.Emit(ctx, operationEvent(
		observabilityusecase.OperationRosterLock,
		command.CommandScope,
		command.TournamentID,
		view.Revision,
	), err)
	return view, err
}

func (service *ObservedService) UnlockRoster(
	ctx context.Context,
	command rosterusecase.UnlockRosterCommand,
) (view rosterusecase.RosterView, err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		view, err = service.next.UnlockRoster(ctx, command)
	}
	measurement.Emit(ctx, operationEvent(
		observabilityusecase.OperationRosterUnlock,
		command.CommandScope,
		command.TournamentID,
		view.Revision,
	), err)
	return view, err
}

func (service *ObservedService) ConfigurePairings(
	ctx context.Context,
	command pairingusecase.PairingCommand,
) (view executionusecase.SwissRoundView, err error) {
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
		observabilityusecase.OperationPairingConfigure,
		command.CommandScope,
		entityID,
		view.Revision,
	), err)
	return view, err
}

func (service *ObservedService) ApplyTournamentAction(
	ctx context.Context,
	command lifecycleworkflow.TournamentActionCommand,
) (view inbound.TournamentView, err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		view, err = service.next.ApplyTournamentAction(ctx, command)
	}
	measurement.Emit(ctx, operationEvent(
		observabilityusecase.OperationTournamentAction,
		command.CommandScope,
		command.TournamentID,
		view.Revision,
	), err)
	return view, err
}

func (service *ObservedService) ControlWave(
	ctx context.Context,
	command executionusecase.WaveCommand,
) (view executionusecase.WaveView, err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		view, err = service.next.ControlWave(ctx, command)
	}
	measurement.Emit(ctx, operationEvent(
		observabilityusecase.OperationWaveControl,
		command.CommandScope,
		command.WaveID,
		view.Revision,
	), err)
	return view, err
}

func (service *ObservedService) ResolveNoShow(ctx context.Context, command resultusecase.NoShowCommand) (err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		err = service.next.ResolveNoShow(ctx, command)
	}
	measurement.Emit(ctx, operationEvent(
		observabilityusecase.OperationNoShowResolve,
		command.CommandScope,
		command.SeriesID,
		command.ExpectedAuthorityRevision,
	), err)
	return err
}

func (service *ObservedService) AssignReserve(ctx context.Context, command replayusecase.ReserveCommand) (err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		err = service.next.AssignReserve(ctx, command)
	}
	measurement.Emit(ctx, operationEvent(
		observabilityusecase.OperationReserveAssign,
		command.CommandScope,
		command.AssignmentID,
		command.ExpectedAuthorityRevision,
	), err)
	return err
}

func (service *ObservedService) RecordForfeit(ctx context.Context, command resultusecase.ForfeitCommand) (err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		err = service.next.RecordForfeit(ctx, command)
	}
	measurement.Emit(ctx, operationEvent(
		observabilityusecase.OperationForfeitRecord,
		command.CommandScope,
		command.SeriesID,
		command.ExpectedAuthorityRevision,
	), err)
	return err
}

func (service *ObservedService) ReplayGame(ctx context.Context, command replayusecase.ReplayCommand) (err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		err = service.next.ReplayGame(ctx, command)
	}
	measurement.Emit(ctx, operationEvent(
		observabilityusecase.OperationGameReplay,
		command.CommandScope,
		command.ReplacementGameID,
		command.ExpectedAuthorityRevision,
	), err)
	return err
}

func (service *ObservedService) CorrectGameResult(
	ctx context.Context,
	command correctionusecase.CorrectionCommand,
) (evidence correctionusecase.CorrectionEvidence, err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		evidence, err = service.next.CorrectGameResult(ctx, command)
	}
	measurement.Emit(ctx, operationEvent(
		observabilityusecase.OperationResultCorrect,
		command.CommandScope,
		command.GameID,
		command.ExpectedProjectionRevision,
	), err)
	return evidence, err
}

func (service *ObservedService) ListAudit(ctx context.Context, query incidentusecase.AuditQuery) (audit.AuditPage, error) {
	if service == nil || service.next == nil {
		return audit.AuditPage{}, domain.ErrInternal
	}
	return service.next.ListAudit(ctx, query)
}

func (service *ObservedService) ExportIncident(
	ctx context.Context,
	query incidentusecase.IncidentQuery,
) (audit.IncidentBundle, error) {
	if service == nil || service.next == nil {
		return audit.IncidentBundle{}, domain.ErrInternal
	}
	return service.next.ExportIncident(ctx, query)
}

func (service *ObservedService) GetOperatorSnapshot(
	ctx context.Context,
	query snapshotusecase.SnapshotQuery,
) (snapshotusecase.OperatorSnapshotView, error) {
	if service == nil || service.next == nil {
		return snapshotusecase.OperatorSnapshotView{}, domain.ErrInternal
	}
	return service.next.GetOperatorSnapshot(ctx, query)
}

func (service *ObservedService) measurement() observabilityusecase.OperationMeasurement {
	if service == nil {
		return observabilityusecase.OperationMeasurement{}
	}
	return observabilityusecase.NewOperationMeasurement(service.clock, service.observer)
}

func operationEvent(
	operation observabilityusecase.Operation,
	scope operationusecase.CommandScope,
	entityID uuid.UUID,
	revision int64,
) observabilityusecase.OperationEvent {
	return observabilityusecase.OperationEvent{
		Operation: operation, CommandID: scope.CommandID, TournamentID: scope.TournamentID,
		EntityID: entityID, Revision: revision,
	}
}

var _ Service = (*ObservedService)(nil)
