package participant

import (
	"context"

	"github.com/google/uuid"

	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
)

// StateReader owns the complete participant projection. Implementations must
// return one consistent snapshot at ExpectedProjectionRevision and must never
// include private flag material or undisclosed reserve content.
type StateReader interface {
	ReadParticipantState(ctx context.Context, query StateQuery) (usecase.RecoveryView, error)
}

type StateQuery struct {
	TournamentID               uuid.UUID
	PlayerID                   uuid.UUID
	ExpectedProjectionRevision int64
}

// CommandExecutor bridges participant commands to the decomposed readiness,
// draft, submission, forfeit, and post-series workflows. Each method must
// revalidate the projection and aggregate ownership in its durable commit.
type CommandExecutor interface {
	SetReady(ctx context.Context, command usecase.ReadyCommand) (readiness.ReadinessEvent, error)
	SubmitDraftAction(ctx context.Context, command usecase.DraftActionCommand) (draftusecase.Execution, error)
	SubmitFlag(ctx context.Context, command usecase.SubmissionCommand) (usecase.SubmissionResult, error)
	Surrender(ctx context.Context, command usecase.SurrenderCommand) (usecase.OfficialResultView, error)
	ApplyPostSeriesAction(ctx context.Context, command usecase.PostSeriesCommand) (usecase.PostSeriesResult, error)
}

type ParticipantDependencies struct {
	Snapshots usecase.TournamentSnapshotUseCase
	States    StateReader
	Commands  CommandExecutor
}

type ParticipantUseCase struct {
	snapshots usecase.TournamentSnapshotUseCase
	states    StateReader
	commands  CommandExecutor
}

func ParticipantNewUseCase(deps ParticipantDependencies) *ParticipantUseCase {
	return &ParticipantUseCase{
		snapshots: deps.Snapshots,
		states:    deps.States,
		commands:  deps.Commands,
	}
}

var _ usecase.TournamentParticipantUseCase = (*ParticipantUseCase)(nil)
