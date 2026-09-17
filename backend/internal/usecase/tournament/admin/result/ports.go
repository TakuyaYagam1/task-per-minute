package result

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	operationusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
)

type CommandScope = operationusecase.CommandScope
type RevisionConflictError = operationusecase.RevisionConflictError

type OperatorResultTransactionManager interface {
	Do(ctx context.Context, fn func(context.Context) error) error
}

type OperatorResultAction string

const (
	OperatorResultActionNoShow  OperatorResultAction = "no_show"
	OperatorResultActionForfeit OperatorResultAction = "forfeit"
)

type OperatorResultAuthority struct {
	TournamentID         uuid.UUID
	RosterID             uuid.UUID
	SeriesID             uuid.UUID
	TournamentState      domain.TournamentState
	AuthorityRevision    int64
	ProjectionRevisionID uuid.UUID
	ProjectionRevision   int64
}

type OperatorResultCommandRecord struct {
	CommandScope

	SeriesID                  uuid.UUID
	Action                    OperatorResultAction
	ExpectedAuthorityRevision int64
	RequestDigest             [32]byte
	CommitID                  uuid.UUID
	ResultEventID             uuid.UUID
	ExecutedAt                time.Time
}

type OperatorResultWorkflowRepository interface {
	LockOperatorResultAuthority(
		ctx context.Context,
		tournamentID uuid.UUID,
		seriesID uuid.UUID,
	) (OperatorResultAuthority, error)
	FindOperatorResultCommand(
		ctx context.Context,
		commandID uuid.UUID,
	) (*OperatorResultCommandRecord, error)
	ReadOperatorResultTime(ctx context.Context) (time.Time, error)
	LoadOperatorNoShowAuthority(
		ctx context.Context,
		command NoShowCommand,
	) (gameusecase.NoShowAuthority, error)
	CommitOperatorNoShow(
		ctx context.Context,
		command NoShowCommand,
		requestDigest [32]byte,
		resolution gameusecase.NoShowResolution,
	) (*gameusecase.NoShowResolution, bool, error)
	LoadOperatorForfeitAuthority(
		ctx context.Context,
		command ForfeitCommand,
	) (gameusecase.ForfeitAuthority, error)
	CommitOperatorForfeit(
		ctx context.Context,
		command ForfeitCommand,
		requestDigest [32]byte,
		resolution gameusecase.ForfeitResolution,
	) (*gameusecase.ForfeitResolution, bool, error)
}

// PostseasonWorkflow advances the tournament after an operator terminal
// result has committed while the same transaction still owns result evidence.
type PostseasonWorkflow interface {
	AdvanceAfterSeriesSettlement(
		ctx context.Context,
		command playoff.TerminalSeriesCommand,
	) (playoff.TerminalReceipt, error)
}

type OperatorResultWorkflowDependencies struct {
	Transactions OperatorResultTransactionManager
	Repository   OperatorResultWorkflowRepository
	Postseason   PostseasonWorkflow
}
