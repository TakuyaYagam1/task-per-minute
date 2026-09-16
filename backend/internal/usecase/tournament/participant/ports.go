package participant

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	gamesettlement "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/settlement"
	gamesubmission "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/submission"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
)

// ParticipantTransactionManager makes the projection lock, ownership
// resolution, leaf use case, and durable commit one atomic participant command.
type ParticipantTransactionManager interface {
	Do(ctx context.Context, fn func(context.Context) error) error
}

// CommandAuthority resolves authenticated players to internal participant IDs
// while holding the current published projection and aggregate ownership locks.
type CommandAuthority interface {
	ResolveReady(ctx context.Context, command usecase.ReadyCommand) (ResolvedReadyCommand, error)
	ResolveDraftAction(ctx context.Context, command usecase.DraftActionCommand) (ResolvedDraftAction, error)
	ResolveSubmission(ctx context.Context, command usecase.SubmissionCommand) (ResolvedSubmission, error)
	ResolveSurrender(ctx context.Context, command usecase.SurrenderCommand) (ResolvedSurrender, error)
	ResolvePostSeries(ctx context.Context, command usecase.PostSeriesCommand) (ResolvedPostSeries, error)
}

type ParticipantCommandAuthority struct {
	TournamentID         uuid.UUID
	RosterID             uuid.UUID
	PlayerID             uuid.UUID
	ParticipantID        uuid.UUID
	TournamentState      domain.TournamentState
	ProjectionRevisionID uuid.UUID
	ProjectionRevision   int64
}

type ResolvedReadyCommand struct {
	Authority ParticipantCommandAuthority
	Command   readiness.ParticipantCommand
}

type ResolvedDraftAction struct {
	Authority ParticipantCommandAuthority
	SeriesID  uuid.UUID
	Command   draftusecase.PlayerActionCommand
}

type ResolvedSubmission struct {
	Authority ParticipantCommandAuthority
	Command   gamesubmission.SubmissionCommand
	Replay    *usecase.SubmissionResult
}

type ResolvedSurrender struct {
	Authority ParticipantCommandAuthority
	Reason    string
	Command   gameusecase.SurrenderCommand
}

type ResolvedPostSeries struct {
	Authority               ParticipantCommandAuthority
	SeriesID                uuid.UUID
	SeriesState             domain.SeriesState
	CurrentResultRevisionID domain.OfficialResultRevisionID
	CommandID               uuid.UUID
	Action                  usecase.PostSeriesAction
}

type ReadinessWorkflow interface {
	SetParticipantReady(
		ctx context.Context,
		command readiness.ParticipantCommand,
	) (*readiness.ReadinessRecord, bool, error)
}

type DraftActionWorkflow interface {
	Apply(ctx context.Context, command draftusecase.PlayerActionCommand) (draftusecase.ActionResult, error)
}

type SubmissionWorkflow interface {
	Submit(
		ctx context.Context,
		command gamesubmission.SubmissionCommand,
	) (record gamedomain.Submission, changed bool, err error)
}

// SettlementWorkflow finalizes a correct submission while the participant
// command coordinator still owns the PostgreSQL transaction.
type SettlementWorkflow interface {
	Settle(
		ctx context.Context,
		command gamesettlement.SettlementCommand,
	) (*gamesettlement.SettlementRecord, bool, error)
}

type SurrenderWorkflow interface {
	Surrender(
		ctx context.Context,
		resolved ResolvedSurrender,
	) (usecase.OfficialResultView, bool, error)
}

type PostSeriesWorkflow interface {
	ApplyPostSeries(
		ctx context.Context,
		resolved ResolvedPostSeries,
	) (usecase.PostSeriesResult, bool, error)
}

// ParticipantPostseasonWorkflow consumes only authoritative internal identities
// after a result or draft write has succeeded in the caller's transaction.
type ParticipantPostseasonWorkflow interface {
	AdvanceAfterSeriesSettlement(
		ctx context.Context,
		command playoff.TerminalSeriesCommand,
	) (playoff.TerminalReceipt, error)
	ActivateFinalAfterDraft(
		ctx context.Context,
		command playoff.TerminalDraftCommand,
	) (playoff.TerminalReceipt, error)
}
