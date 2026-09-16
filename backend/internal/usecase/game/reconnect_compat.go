package game

import (
	"context"

	reconnectusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
)

var (
	ErrInvalidMutation = reconnectusecase.ErrInvalidMutation
	ErrConflict        = reconnectusecase.ErrConflict
	ErrCommandReuse    = reconnectusecase.ErrCommandReuse
	ErrDeadline        = reconnectusecase.ErrDeadline
	ErrUnavailable     = reconnectusecase.ErrUnavailable
)

type MutationKind = reconnectusecase.MutationKind

const (
	MutationReconnect  = reconnectusecase.MutationReconnect
	MutationTimeout    = reconnectusecase.MutationTimeout
	MutationDisconnect = reconnectusecase.MutationDisconnect

	OutcomeSuccess  = reconnectusecase.OutcomeSuccess
	OutcomeRetry    = reconnectusecase.OutcomeRetry
	OutcomeRejected = reconnectusecase.OutcomeRejected
	OutcomeFailure  = reconnectusecase.OutcomeFailure
)

type ReconnectAuthority = reconnectusecase.ReconnectAuthority
type ReconnectCommand = reconnectusecase.ReconnectCommand
type SettlementIDs = reconnectusecase.SettlementIDs
type TerminalOutcome = reconnectusecase.TerminalOutcome
type TimeoutCommand = reconnectusecase.TimeoutCommand
type DisconnectCommand = reconnectusecase.DisconnectCommand
type ReconnectRecord = reconnectusecase.ReconnectRecord
type ReconnectUseCase = reconnectusecase.ReconnectUseCase
type DisconnectUseCase = reconnectusecase.DisconnectUseCase
type TimeoutUseCase = reconnectusecase.TimeoutUseCase
type ReconnectEvent = reconnectusecase.ReconnectEvent

func ReconnectNewUseCase(
	repository ReconnectRepository,
	clock ReconnectClock,
	observers ...Observer,
) *ReconnectUseCase {
	return reconnectusecase.ReconnectNewUseCase(repository, clock, observers...)
}

func NewDisconnectUseCase(repository ReconnectRepository, clock ReconnectClock) *DisconnectUseCase {
	return reconnectusecase.NewDisconnectUseCase(repository, clock)
}

func NewTimeoutUseCase(
	repository ReconnectRepository,
	clock ReconnectClock,
	observers ...Observer,
) *TimeoutUseCase {
	return reconnectusecase.NewTimeoutUseCase(repository, clock, observers...)
}

func TimeoutTerminalEvent(
	command TimeoutCommand,
	record *ReconnectRecord,
	changed bool,
	err error,
) ReconnectEvent {
	return reconnectusecase.TimeoutTerminalEvent(command, record, changed, err)
}

func ObserveEvent(ctx context.Context, observer Observer, event ReconnectEvent) {
	reconnectusecase.ObserveEvent(ctx, observer, event)
}
