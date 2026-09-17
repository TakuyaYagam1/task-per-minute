package replay

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	replayusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/replay"
	operationusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
)

type OperatorIdentity = operationusecase.OperatorIdentity
type RevisionConflictError = operationusecase.RevisionConflictError
type CommandScope = operationusecase.CommandScope

type ReplayTransactionManager interface {
	Do(ctx context.Context, fn func(context.Context) error) error
}

type OperatorReserveAuthority struct {
	TournamentState domain.TournamentState
	Replay          replayusecase.OperatorReserveAuthority
}

type ReplayReplacementAuthority struct {
	TournamentState domain.TournamentState
	Replay          replayusecase.ReplayReplacementAuthority
}

// ReplayWorkflowRepository keeps each admin command on the transaction opened
// by ReplayWorkflow. The command argument carries candidate and idempotency
// evidence that the narrower replay use cases intentionally do not own.
type ReplayWorkflowRepository interface {
	ReadReplayTime(ctx context.Context, commandID uuid.UUID) (time.Time, error)
	LoadOperatorReserveAuthority(
		ctx context.Context,
		command ReserveCommand,
	) (OperatorReserveAuthority, error)
	CommitOperatorReserve(
		ctx context.Context,
		command ReserveCommand,
		requestDigest [32]byte,
		record replayusecase.OperatorReserve,
	) (*replayusecase.OperatorReserve, bool, error)
	LoadReplayReplacementAuthority(
		ctx context.Context,
		command ReplayCommand,
	) (ReplayReplacementAuthority, error)
	CommitReplayReplacement(
		ctx context.Context,
		command ReplayCommand,
		requestDigest [32]byte,
		record replayusecase.ReplayReplacement,
	) (*replayusecase.ReplayReplacement, bool, error)
}

type ReplayWorkflowDependencies struct {
	Transactions ReplayTransactionManager
	Repository   ReplayWorkflowRepository
}
