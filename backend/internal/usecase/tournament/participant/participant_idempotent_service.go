package participant

import (
	"context"
	"strings"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
)

const (
	participantReadyReceiptNamespace      = "participant-ready"
	participantDraftReceiptNamespace      = "participant-draft-action"
	participantSubmissionReceiptNamespace = "participant-flag-submit"
	participantSurrenderReceiptNamespace  = "participant-surrender"
	participantPostSeriesReceiptNamespace = "participant-post-series"
)

// IdempotentService adds shared receipt admission to participant mutations.
// Completed receipt calls delegate to the ordinary use case service so the
// durable CommandID result remains the sole source of replayed outcomes.
type ParticipantIdempotentService struct {
	next        usecase.TournamentParticipantUseCase
	coordinator *idempotency.Coordinator
}

func ParticipantNewIdempotentService(next usecase.TournamentParticipantUseCase, coordinator *idempotency.Coordinator) *ParticipantIdempotentService {
	return &ParticipantIdempotentService{next: next, coordinator: coordinator}
}

func (service *ParticipantIdempotentService) GetLobby(ctx context.Context, query usecase.LobbyQuery) (usecase.LobbyView, error) {
	if service == nil || service.next == nil {
		return usecase.LobbyView{}, domain.ErrInternal
	}
	return service.next.GetLobby(ctx, query)
}

func (service *ParticipantIdempotentService) GetAssignment(
	ctx context.Context,
	query usecase.AssignmentQuery,
) (usecase.AssignmentResult, error) {
	if service == nil || service.next == nil {
		return usecase.AssignmentResult{}, domain.ErrInternal
	}
	return service.next.GetAssignment(ctx, query)
}

func (service *ParticipantIdempotentService) SetReady(
	ctx context.Context,
	command usecase.ReadyCommand,
) (usecase.ReadinessEvent, error) {
	return executeParticipantMutation(service, ctx, validReadyCommand(command), func() (idempotency.Command, error) {
		return readyReceipt(command)
	}, func() (usecase.ReadinessEvent, error) {
		return service.next.SetReady(ctx, command)
	})
}

func (service *ParticipantIdempotentService) SubmitDraftAction(
	ctx context.Context,
	command usecase.DraftActionCommand,
) (usecase.DraftExecutionView, error) {
	return executeParticipantMutation(service, ctx, validDraftActionCommand(command), func() (idempotency.Command, error) {
		return draftActionReceipt(command)
	}, func() (usecase.DraftExecutionView, error) {
		return service.next.SubmitDraftAction(ctx, command)
	})
}

func (service *ParticipantIdempotentService) SubmitFlag(
	ctx context.Context,
	command usecase.SubmissionCommand,
) (usecase.SubmissionResult, error) {
	return executeParticipantMutation(service, ctx, validSubmissionCommand(command), func() (idempotency.Command, error) {
		return submissionReceipt(command)
	}, func() (usecase.SubmissionResult, error) {
		return service.next.SubmitFlag(ctx, command)
	})
}

func (service *ParticipantIdempotentService) Surrender(
	ctx context.Context,
	command usecase.SurrenderCommand,
) (usecase.OfficialResultView, error) {
	command.Reason = strings.TrimSpace(command.Reason)
	return executeParticipantMutation(service, ctx, validSurrenderCommand(command), func() (idempotency.Command, error) {
		return surrenderReceipt(command)
	}, func() (usecase.OfficialResultView, error) {
		return service.next.Surrender(ctx, command)
	})
}

func (service *ParticipantIdempotentService) ApplyPostSeriesAction(
	ctx context.Context,
	command usecase.PostSeriesCommand,
) (usecase.PostSeriesResult, error) {
	return executeParticipantMutation(service, ctx, validPostSeriesCommand(command), func() (idempotency.Command, error) {
		return postSeriesReceipt(command)
	}, func() (usecase.PostSeriesResult, error) {
		return service.next.ApplyPostSeriesAction(ctx, command)
	})
}

func (service *ParticipantIdempotentService) GetSnapshot(
	ctx context.Context,
	query usecase.SnapshotQuery,
) (usecase.RecoveryView, error) {
	if service == nil || service.next == nil {
		return usecase.RecoveryView{}, domain.ErrInternal
	}
	return service.next.GetSnapshot(ctx, query)
}

func executeParticipantMutation[T any](
	service *ParticipantIdempotentService,
	ctx context.Context,
	valid bool,
	receipt func() (idempotency.Command, error),
	invoke func() (T, error),
) (T, error) {
	var zero T
	if ctx == nil || !valid {
		return zero, domain.ErrValidation
	}
	if service == nil || service.next == nil || service.coordinator == nil {
		return zero, domain.ErrInternal
	}
	command, err := receipt()
	if err != nil {
		return zero, domain.ErrInternal
	}
	return idempotency.Execute(ctx, service.coordinator, command, func(context.Context) (T, error) {
		return invoke()
	})
}

var _ usecase.TournamentParticipantUseCase = (*ParticipantIdempotentService)(nil)
