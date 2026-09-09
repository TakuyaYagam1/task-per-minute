package participant

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

// ObservedService emits one terminal event only after the idempotent service
// has returned the durable participant command outcome.
type ParticipantObservedService struct {
	next     usecase.TournamentParticipantUseCase
	clock    ParticipantOperationClock
	observer ParticipantOperationObserver
}

func ParticipantNewObservedService(
	next usecase.TournamentParticipantUseCase,
	clock ParticipantOperationClock,
	observer ParticipantOperationObserver,
) *ParticipantObservedService {
	return &ParticipantObservedService{next: next, clock: clock, observer: firstParticipantOperationObserver(observer)}
}

func (service *ParticipantObservedService) GetLobby(ctx context.Context, query usecase.LobbyQuery) (usecase.LobbyView, error) {
	if service == nil || service.next == nil {
		return usecase.LobbyView{}, domain.ErrInternal
	}
	return service.next.GetLobby(ctx, query)
}

func (service *ParticipantObservedService) GetAssignment(ctx context.Context, query usecase.AssignmentQuery) (usecase.AssignmentResult, error) {
	if service == nil || service.next == nil {
		return usecase.AssignmentResult{}, domain.ErrInternal
	}
	return service.next.GetAssignment(ctx, query)
}

func (service *ParticipantObservedService) SetReady(
	ctx context.Context,
	command usecase.ReadyCommand,
) (event usecase.ReadinessEvent, err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		event, err = service.next.SetReady(ctx, command)
	}
	measurement.emit(ctx, participantOperationEvent(
		ParticipantOperationReady,
		command.CommandID,
		command.TournamentID,
		command.WaveID,
		command.ExpectedProjectionRevision,
	), err)
	return event, err
}

func (service *ParticipantObservedService) SubmitDraftAction(
	ctx context.Context,
	command usecase.DraftActionCommand,
) (execution usecase.DraftExecutionView, err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		execution, err = service.next.SubmitDraftAction(ctx, command)
	}
	entityID := execution.ID
	if entityID == uuid.Nil {
		entityID = command.SeriesID
	}
	revision := execution.Revision
	if revision < 1 {
		revision = command.ExpectedProjectionRevision
	}
	measurement.emit(ctx, participantOperationEvent(
		ParticipantOperationDraft,
		command.CommandID,
		command.TournamentID,
		entityID,
		revision,
	), err)
	return execution, err
}

func (service *ParticipantObservedService) SubmitFlag(
	ctx context.Context,
	command usecase.SubmissionCommand,
) (result usecase.SubmissionResult, err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		result, err = service.next.SubmitFlag(ctx, command)
	}
	revision := result.ProjectionRevision
	if revision < 1 {
		revision = command.ExpectedProjectionRevision
	}
	measurement.emit(ctx, participantOperationEvent(
		ParticipantOperationSubmission,
		command.CommandID,
		command.TournamentID,
		command.GameID,
		revision,
	), err)
	return result, err
}

func (service *ParticipantObservedService) Surrender(
	ctx context.Context,
	command usecase.SurrenderCommand,
) (view usecase.OfficialResultView, err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		view, err = service.next.Surrender(ctx, command)
	}
	measurement.emit(ctx, participantOperationEvent(
		ParticipantOperationSurrender,
		command.CommandID,
		command.TournamentID,
		command.SeriesID,
		command.ExpectedProjectionRevision,
	), err)
	return view, err
}

func (service *ParticipantObservedService) ApplyPostSeriesAction(
	ctx context.Context,
	command usecase.PostSeriesCommand,
) (result usecase.PostSeriesResult, err error) {
	measurement := service.measurement()
	if service == nil || service.next == nil {
		err = domain.ErrInternal
	} else {
		result, err = service.next.ApplyPostSeriesAction(ctx, command)
	}
	revision := result.ProjectionRevision
	if revision < 1 {
		revision = command.ExpectedProjectionRevision
	}
	measurement.emit(ctx, participantOperationEvent(
		ParticipantOperationPostSeries,
		command.CommandID,
		command.TournamentID,
		command.SeriesID,
		revision,
	), err)
	return result, err
}

func (service *ParticipantObservedService) GetSnapshot(ctx context.Context, query usecase.SnapshotQuery) (usecase.RecoveryView, error) {
	if service == nil || service.next == nil {
		return usecase.RecoveryView{}, domain.ErrInternal
	}
	return service.next.GetSnapshot(ctx, query)
}

func (service *ParticipantObservedService) measurement() participantOperationMeasurement {
	if service == nil {
		return participantOperationMeasurement{}
	}
	return newParticipantOperationMeasurement(service.clock, service.observer)
}

func participantOperationEvent(
	operation ParticipantOperation,
	commandID uuid.UUID,
	tournamentID uuid.UUID,
	entityID uuid.UUID,
	revision int64,
) ParticipantOperationEvent {
	return ParticipantOperationEvent{
		Operation: operation, CommandID: commandID, TournamentID: tournamentID,
		EntityID: entityID, Revision: revision,
	}
}

var _ usecase.TournamentParticipantUseCase = (*ParticipantObservedService)(nil)
